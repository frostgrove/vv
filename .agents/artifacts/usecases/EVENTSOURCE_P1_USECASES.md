# EVENTSOURCE PHASE 1 — USECASES, INVARIANTS & DX

> **Reading rule for this document.** Nothing here names a symbol that exists in
> the tree *as this subsystem's own API*. Every Go-shaped fragment uses
> deliberately chosen placeholder names and communicates **shape and obligation**,
> not an approved API. Where an existing framework contract is named — `crud.Source`,
> `crud.KeyOf`, `crud.SameDataSource`, `crudsql.TransactionFor`, `errs`' kinds,
> `jobs.Codec`, `jobs.Upcast`, `jobs.TransactionBinding` — it is named because it
> was read, and because a second subsystem answering one question a second way is
> itself a defect ([[D-118]] is the case in point).
>
> Three documents govern and are cited throughout:
> **[ES]** — `docs/roadmaps/2026-09-01-postgres-event-sourcing-roadmap.md`;
> **[EXT]** — `docs/roadmaps/2026-09-01-extension-architecture-roadmap.md`;
> **[HOUSE]** — `CLAUDE.md` plus the binding decision docs it points at.
> Decision references use the repository's own `[[D-nnn]]` spelling.
>
> **One half of [ES] is superseded and this document says so once.** [ES] decides
> "no root `event` package" and puts the vocabulary inside a PostgreSQL module.
> That half is overturned by [[D-116]]: *a module boundary is a third-party
> dependency boundary, not an optionality boundary.* An event vocabulary that
> costs a consumer no `require` line does not earn a module, exactly as `jobs`,
> `storage`, `cache`, `port` and `tenancy` do not. `event` also does **not** join
> the closed contract manifest of [[D-048]] — it is an ordinary root package with
> ordinary first-party dependencies, the shape `jobs` already has. **Everything
> else in [ES] remains binding**: the non-negotiable invariant list, the forbidden
> package names, the capability and wrapper obligations, and the initial
> non-goals. Landing phase 1 owes a decision doc that records this supersession
> and the second-implementation argument that justifies the seam
> (`eventmemory` is that second implementation, present on day one).

---

## How this document got its shape

Nine rounds of audit preceded this one and their dispositions are in
`.agents/artifacts/gaps/EVENTSOURCE_P1_USECASES_GAPS.md`, not restated here. What
is worth carrying forward is the one root cause and the shape changes it produced,
because each makes a mistake **unaskable** rather than answered — which is the
order this design takes throughout: inexpressible, then refused at declaration or
at `Bind`, then refused at the call with a named error, and a rule in prose only
when the three above are unavailable, in which case the conformance suite gets a
control that proves the obligation is real.

**The root cause.** The spec had grown a live, mutable `View` holding five jobs at
once: a fold accumulator, the value handed to the application, a self-advancing
copy of the store's version, the transaction binding, and a party to a
storage-backing comparison.

**The thesis.** *The kernel retains no value the application owns.* `Load` hands
the state over outright and forgets it. `Append` is a repository call taking an
unforgeable `At[S]` that carries only (stream, version, backing). Nothing sealed
crosses the store boundary in the store's direction.

| The shape change | What it makes unaskable |
|---|---|
| The `View` is deleted; a state, a token and a receipt are three inert values | *What state is a view in?* — an append has outcomes, not a receiver that remembers them (§INV-040) |
| The codec moves from the store to the declaration | *When is "can this codec encode this type" answered?* — before `main`, per revision (§D.2, §INV-023) |
| `Fact.New` encodes at the moment of decision | *May a payload mutated after the decision change the recorded fact?* (§UC-063) |
| A `Change` retains no decoded value; every fold decodes a clone of the frozen bytes | *Can two folds of one change disagree?* (§UC-065, §INV-021, §INV-042) |
| `New` and `Fold` take the aggregate's **own identity**, so a change names its stream | *Can one instance's facts land in another's history?* (§UC-064, §INV-044) |
| `Within` returns a **context**, and every store question is a required method | *Can a decorator be tunnelled for the duration of a transaction?* (§UC-048, §INV-030) |
| An authority **is** its transaction's identity, held by reference | *What evicts a token memoised per live transaction?* (§INV-028) |
| A store classifies its own failure in the kernel's closed `Outcome` vocabulary | *How does a store spell a conflict without adding a sentinel?* (§D.14, §INV-045) |

The alternative each of those replaced is in §D.12 with the argument it was
rejected on, so nobody re-proposes one in phase 2.

**The last shape change is the one that took five rounds to get total, and the way
it was finally got total is the method this document now uses for any ownership
question: enumerate the domain first, then make the rule total over it, and leave
the enumeration where the rule is.** §INV-021 is stated over **hand-offs** rather
than over holders — *whoever hands mutable memory over says whether anybody will
write it again* — and it carries two enumerations, seven outbound hand-offs and
seven inbound rows, so no party and no value can be left out by describing the
design from the two stores and one codec that happen to exist. §INV-020's
compile-time guarantee is likewise stated at its real scope, two aggregates over
two **state types**, with the runtime refusal beside it and the one crossing that
has neither named as a residual (§UC-002, §D.10).

---

## 1. Scope

Phase 1 delivers a trustworthy, append-only, per-aggregate fact history in the
**root module** and nothing that touches a database. Three packages: `event`
holds the vocabulary — stream identity (`Stream`, `Key`, `Version`, `Position`,
all four written out in §D.14), the event envelope, the declaration seam
for aggregates, their event types and their codecs, bounded rehydration, the
expected-version append contract, the store contract (written out in §D.14) and
the classification channel a store answers it through (§D.14, §INV-045), the
bounded read and its resume contract, the transaction seam and the refusal
vocabulary (enumerated in §2.3);
`event/eventmemory` is a complete, concurrency-safe, transaction-capable
in-memory store that is a first-class implementation for unit-testing an
application's aggregates without a database, and which states in its own
capability report what it cannot certify; `event/eventtest` is an exported
conformance suite that any store runs to prove it satisfies the store contract,
which falsifies itself on every run, and which phase 2's PostgreSQL store will
run **verbatim**. The defining architectural test for the whole phase is stated
once, checked at the end, and is **executable**: **adding a second store
implementation requires zero diffs to `event/`** (§INV-019).

### What phase 2's `event/eventpg` must and must not write

Stated here, in Scope, because the phase boundary is a contract and not an
aside. §D.15 writes the proof out.

**`eventpg` must write:** its own `go.mod` (it requires a driver, which is why it
is a module — [[D-033]], [[D-116]]); a `New(Spec) (*Store, error)` that verifies
the settings row the data depends on ([[D-101]]'s managed/verified split); the
eight methods of §D.14's contract; its own SQL, schema and migrations; its own
cursor format; its classification of its own driver failures — `errs/sqlerr` to a
code, a code to an `event.Outcome`, and `event.Failure` to say it (§D.14,
§INV-045); and a test file that hands `eventtest.Run` a factory.

**`eventpg` must not write, and must not need:** any error sentinel of its own on
the seam — it selects an `Outcome` and the kernel maps it (§2.3, §INV-045); any
change to any file under `event/`; a registration row in any kernel table; a type
switch anywhere in the kernel; a `Codec`; a fold, an upcaster or any knowledge
that aggregates exist; a `Version` assignment rule the kernel dictates; or a
single line of the conformance suite, whose test is `eventmemory`'s with a
different factory.

**How the claim is held rather than promised:** `git diff --stat <phase-1-tag> --
event/ ':!event/eventpg'` must be empty, run as an arm of `make check`
(§INV-019). `scripts/checks.sh` is exactly where structural checks `go test`
cannot see already live.

### Non-goals — named, not specified

Phase 1 refuses all of the following. Each is named so that a later reader can
see it was decided rather than forgotten. The first block is [ES]'s own initial
non-goal list, restated; the second is phase-specific.

1. **PostgreSQL, any SQL, any schema, any migration, any driver.** No statement
   text is written in phase 1. Schema management as a deployment profile
   ([[D-101]]) is phase 2's obligation, not this phase's.
2. **A generic database-neutral event store beyond the contract itself.** The
   contract exists because there are two implementations on day one; no third
   backend is promised, and no backend-portability claim is made.
3. **Projections, checkpoints, subscriptions as a running activity.** Nothing in
   phase 1 runs. A projector is a `runtime.Runner` in a later phase ([[D-092]]).
4. **Snapshots.** Full replay is the only authority in phase 1, and snapshots are
   [ES]-deferred until measured replay cost justifies them.
5. **Outbox, brokers, senders, integration contracts.** [[D-118]] already decides
   that a transactional enqueue is this framework's outbox; phase 1 adds no
   second durable-intent table and no `eventkafka`/`eventnats`.
6. **OpenTelemetry.** No import, no span, no metric. `eventotel` is forbidden by
   name in [ES] and by [[D-114]].
7. **Tenancy and audit.** No import in either direction. Composition of the two
   is an application fixture, never a package ([[D-116]], [ES] forbidden list).
8. **Container / fx bindings.** An `eventfx` is a later, separate module on the
   `jobsfx` pattern ([[D-074]], [[D-108]]). Phase 1 needs none and builds none.
9. **Codegen.** No `cmd/vv` extension, no generated metamodel, no generated DTO.
10. **Exactly-once delivery, hidden retry, hidden background work, a saga or
    workflow engine, automatic aggregate registration, arbitrary metadata maps,
    raw event-history CRUD or search endpoints, payload/identity export for
    debugging convenience.** All are [ES] initial non-goals and all stay.

Phase-specific refusals, each argued where it first arises below:

11. **Multi-stream atomic append.** One append names exactly one stream and one
    expected version. Atomicity across streams exists only inside a transaction
    the caller owns (§UC-028, §INV-003).
12. **Point-in-time (`as of version N`) loads.** Cheap to add and dangerous to
    add first: an as-of token that can append is a deliberate stale write. Phase 1
    has no spelling for it; the paged read makes it a five-line addition later.
13. **Retention, archival and reader-set narrowing.** [ES] puts "retained-reader
    policy before beta" in E2. Phase 1 neither implements nor tests archival and
    ships **no spelling for it either**: a chain always starts at revision 1.
    Retention brings its own constructor, refusal and tests, and adding one is
    additive. What phase 1 must not do is export a floor nobody tests — the only
    hand-typed number in the design, whose off-by-one shifts every revision and
    misdecodes silently (§D.2).
14. **An append idempotency key.** Argued in §UC-035 and left open rather than
    closed: it is on the reconciliation agenda as Q16, and phase 1 ships no
    parameter for it.
15. **Envelope metadata, causation and correlation identifiers.** Argued and
    rejected in §2.2.
16. **A generated per-event identifier.** Argued and rejected in §2.2.
17. **A framework-owned `Decide` / `Do` helper that loads, decides and appends in
    one call.** Argued and rejected in §D.12.
18. **A per-store key byte domain.** Deleted in round 4: one kernel-wide text
    rule covers keys and declared identifiers alike (§INV-033, §D.1).
19. **A caller-supplied read page size.** The kernel fills every read. A caller
    that wants smaller pages gets an additive option in the phase that has a
    consumer needing one; shipping the dial now re-opens GAP-51 (§D.7).
20. **A position range on an append receipt.** Phase 2's outbox or projector will
    want one; it arrives then, additively, with a reader. Shipping it now is the
    "for later" public API `restrictions.md` §1 forbids (§D.6).
21. **A `port.Service` adapter and a `storage.Store` adapter.** [ES]'s capability
    obligations **8** and **9** are conditioned on exactly those two wrappers — a
    restore preserved as `port.RestorableOf` discovers it, and `storage.Store`
    capabilities forwarded exactly with `Open`'s stream ownership intact — and
    phase 1 builds neither, so both are **vacuous rather than unmet**. Named here
    because [ES] requires "all of these before release" and an obligation with no
    home reads as forgotten. *What would reopen them:* the first `event` wrapper
    that adapts either port. The other eight are mapped in §D.16.

---

## 2. Vocabulary this document fixes

[ES] supplies most of these words; the ones it does not are marked **new**. Where
this document narrows [ES]'s wording, it says so.

### 2.1 Identity and order

| Term | Meaning fixed here |
|---|---|
| **aggregate** | One consistency boundary that decides facts from its own state and a command. Its Go state type's **zero value is the origin of every state** (§UC-009, §INV-008). *Usable* means readable, never non-empty: `type Ledger map[string]int64` zeroes to a nil map, which §UC-052 blesses, and the fold owns the guard that makes the first event work — an unguarded write into it panics out of `Load` and is not recovered (§UC-067) |
| **stream family** | A **declared** identifier naming the kind of stream, one per aggregate declaration, e.g. `accounts.account`. Declared once, never derived from a Go type name. **One family names one aggregate, forever** — across processes and deployments, not only within one binary (§INV-039) |
| **stream key** | The aggregate's identity rendered into the store's key space by a mapper declared beside the family. `Key` is a defined `string` (§D.14). **New**: [ES] says "identity"; this document makes the rendering a declared, single-place format concern and an **injective** one — two identities must never render as one key (§INV-033) — and a **wire format**: once a stream has been written, that identity's rendering may never change (§INV-005). The mapper is the *only* place a key is produced, and it runs wherever an identity is named: `Repo.Load`, `Fact.New` and `Aggregate.Fold` (§UC-064, §INV-044). `Repo.Append` names no identity — it takes a token and changes, both of which already carry their stream — so nothing renders there, and it validates rather than produces: **a key is checked at every door it crosses**, so the kernel's non-empty and text rules run at all four calls and the store's `MaxKey` at the two that know a store, `Load` and `Append` (§UC-051, §INV-015) |
| **text rule** | **New, and it is one rule for the whole kernel.** A key and a declared identifier are legal text when they are non-empty, valid UTF-8, contain no NUL and no control character, and are within their cap. Keys compare as bytes; no layer folds case, trims, or normalises Unicode. Round 4 deleted the per-store key domain: a store whose column cannot hold that text cannot host this framework's data, which is a fact about that store and not a setting (§INV-033) |
| **limits** | **New.** The numbers the kernel enforces on this store's behalf, readable from any store value. They are configuration the store carries and never applies itself (§D.14) |
| **stream** | The pair (family, key), compared byte-exact. `Stream` is a struct of two exported fields and is comparable with `==`. The unit of ordering and of optimistic concurrency (§D.14) |
| **stream version** | A dense counter over one stream: the first event is version 1, the n-th is version n, no gaps, no reuse, no reassignment. Version 0 is the version of a stream that has never been appended to, and is not an event. `Version` is a defined `uint64` (§D.14) |
| **global position** | A store-wide ordinal, strictly increasing over committed events, never reassigned. **Gaps are permitted** (§INV-009); dense-ness is not a promise any real store can keep. It is an identity, **not** a resume point — see cursor. `Position` is a defined `uint64` (§D.14) |
| **cursor** | **New.** A store-minted, opaque, persistable resume point for the global read. No arithmetic on it is defined and no caller derives one from a position. Resuming a read from a cursor the store returned skips nothing, on every store the contract admits (§INV-035) |
| **backing** | **New.** What a store writes to, as opposed to the store value: the log a memory store owns, the database a SQL store writes to. Two store values over one backing are one store for every purpose in this document, with **one stated exception** — the bound-family set (§UC-054, §UC-059, §INV-016). Backings compare with `Equal`, never with `==`, and an invalid one matches nothing, including another invalid one. `Equal` **is** `crud.SameDataSource`: `event` imports it rather than restating its rule, because "are these two things writing to the same place" is a question the framework already answers once (§INV-016, §D.15) |
| **event type** | A **declared** wire identifier such as `accounts.credited`. Never a Go type name, never reflection output, never a translated label |
| **payload revision** | An integer versioning the *meaning* of one event type's payload. Orthogonal to stream version and to global position |
| **reader** | The Go type one retained revision's bytes decode into, **its codec**, plus the path that carries that value forward to the current type. **New**: [ES] says "the reader for each"; this document makes it a per-revision decode type with its own codec, plus an upcaster chain |
| **upcaster** | A pure, total-or-refusing function from one revision's reader type to the next revision's reader type |
| **change** | **New.** A decided-but-not-yet-appended fact: the payload **already encoded** by the declaring fact's current codec, **the stream it was decided for**, and an adapter that decodes those bytes and folds them. A `Change` retains **no application value at all** — not the caller's payload and not a value decoded from it — so the value a fold receives is produced at the moment of the fold and is reachable from nothing the framework holds (§INV-042, §UC-065) |
| **at-token** | **New**, and it replaces the `view`. The observed position of one stream: which stream, at which version, over which backing. Minted only by `Load` and by a successful `Append`. Inert: it holds no state, no store, no transaction and no accumulator, and it can be copied freely because copying changes no answer it gives (§INV-038). Its stream is what an append compares each change against (§INV-044) |
| **append authority** | The exact transaction (or the store's own autocommit, which is the invalid authority) an append ran under, proved by the transaction's **own identity held by reference**, never by a datasource pointer and never by minted entropy. It has `jobs.TransactionBinding`'s *properties* — opaque, unserialisable, no readable data — and a better derivation: `jobs`'s token is fresh entropy per call and is therefore not comparable even with itself (§UC-030, §INV-028) |
| **commit uncertainty** | A connection or process lost around commit: the outcome is unknown and no layer guesses it |
| **outcome** | **New.** What a store says about a failure of its own, in the kernel's closed vocabulary: *conflict*, *certainly not written*, *unconfirmed*, *closed*, *foreign cursor*, or nothing said at all. It is a **classification, not a sentinel** — a store adds no error to §2.3's twenty-two, it selects among rows that already exist, and the kernel maps its selection (§D.14, §INV-045) |
| **fail-safe default** | What the kernel does with a failure a store classified as nothing. From an append it is commit uncertainty, because guessing the safe way costs one reload and guessing the other way is a silent double write; from a read it is a backend failure, because a read wrote nothing there is anything to be uncertain about (§UC-060, §INV-045) |

### 2.2 The envelope, and what is deliberately not in it

A stored envelope carries exactly:

| Field | Why it is there |
|---|---|
| stream (family + key) | What the fact is about |
| stream version | Where it sits in that aggregate's history; the concurrency ordinal |
| global position | Where it sits in the store's total order; a stable identity for one event, and a deduplication key. **Not** what a consumer resumes from — that is the cursor the read returns (§UC-053) |
| declared event type | Which reader table to consult |
| payload revision | Which reader in that table |
| payload bytes | The fact, opaque to the store, canonical for that revision's codec |
| recorded instant | Store-assigned. Explicitly **not** an ordering key |

Four things a reflexive design would add and this one refuses.

**A generated event identifier — rejected.** `(family, key, version)` is already
a globally unique, derived, stable name for the event, and so is the global
position. A second identity would need randomness injected into the write path,
would create two answers to "which event is this", and would violate the "one
fact, one source of truth, one representation" rule. A consumer that needs a
deduplication key for at-least-once delivery uses the global position or the
triple. *What would reopen it:* a store whose positions are not comparable across
a restore, which is a phase-2 question about a real backend.

**Causation and correlation identifiers — rejected.** They are facts about the
*request* that produced the fact, not facts about the domain, and an append-only
history makes them permanent and uncorrectable. They are also precisely the class
of value [ES] forbids from becoming default log, span or metric fields, and the
telemetry seam that would give them meaning is deferred by [ES] and [[D-114]].
Phase 1 has no reader for them — no projector, no outbox, no tracer — and a field
with no reader is speculative generality ([[D-048]]). *What the application does
instead, and it is the better answer:* if "who caused this" matters to the domain
it is a domain fact and belongs **in the payload**, versioned by the revision
mechanism like everything else. *What would reopen it:* a projector or outbox
correlating an emitted integration message with its cause — a phase-2/E4
question, answered then by a **declared, typed, revisioned metadata payload**,
never by a `map[string]any`, which [ES] already forbids.

**A metadata map — rejected outright.** [ES] lists "arbitrary metadata maps" as
an initial non-goal. A map is an unversioned, unvalidated, untyped second payload
that every reader interprets differently; it is the ambiguous-state defect with a
friendly name.

**A caller-supplied timestamp — rejected; a store-assigned one is kept.** A
caller-supplied instant is a lie waiting to be told (clock skew, replay, tests
that forgot). A store-assigned one is the only honest one, and the store's clock
is injected so tests are deterministic. It is recorded because a fact history with
no time is unusable for forensics, and it is **not** an ordering key and carries
no monotonicity promise (§INV-009).

### 2.3 The refusal vocabulary — six classes, one rule

A pairwise matrix over N sentinels has N² opportunities to contradict itself and
no mechanism that prevents any of them; round 3's did, in two cells (GAP-48). A
**partition** makes that contradiction unspellable rather than restated.

**The rule, and there is exactly one:** every sentinel belongs to exactly one
class, and **`errors.Is` never crosses a class**. Within a class, one sentinel may
wrap another — that is how a specific refusal stays reachable from a general one —
and no claim is made about row-to-row distinguishability, because no caller needs
one. Two closed lists complete it and there is nothing outside them: **two
intra-class wraps**, both into `ErrDeclaration`, and **four declared class wraps**
out of the vocabulary into classes a transport already maps. Nothing else *wraps a
class*; a refusal built from somebody else's error additionally carries it as a
**cause**, which is a different thing and is defined below.

| Class | What the caller learns | Sentinels |
|---|---|---|
| **declaration** | *A programmer wrote the declaration wrong.* Raised as a **panic**, never returned as an error. Normally before `main`; the one route that reaches a request goroutine is a late declaration, and the framework deliberately does not soften it (§UC-005) | `ErrDeclaration`, `ErrSealed`, `ErrCodecType` |
| **wiring** | *This program was assembled from values that do not belong together* — at the composition root, or by pairing two framework values that were minted apart. Raised at `Bind`, at `Read`, at `Within`, or before any statement of an operation. A programmer or operator problem, never a client's | `ErrFamily`, `ErrWrongStore`, `ErrWrongStream`, `ErrNoTransaction`, `ErrNoTransactionBinding`, `ErrAmbientNotTransaction`, `ErrTransactionMismatch` |
| **request** | *The data this operation was given cannot be used.* The ordinary cause is a request; a transport answers a client error | `ErrKey`, `ErrEncode`, `ErrTooLarge` |
| **history** | *A fact's recorded bytes cannot be read by this declaration.* Neither the caller's fault nor the store's; a deploy, a retention or a broken-codec question. It is stated over the **bytes** and not over the store, because `Fact.New` records a fact at the moment of decision (§UC-063): a `Change`'s frozen array and the envelope it later becomes are the same bytes, so one decode failure has one sentinel wherever it surfaces (§UC-015, §UC-041) | `ErrUnknownType`, `ErrRevision`, `ErrPayload`, `ErrUpcast` |
| **write** | *The append did not do what you asked, and here is your obligation.* Two obligations, two sentinels, and no third | `ErrConflict`, `ErrUncertain` |
| **store** | *The store itself refused or failed.* | `ErrBackend`, `ErrClosed`, `ErrCursor` |

Twenty-two sentinels. The one-line meaning of each is in the use case that raises
it; every refusing use case carries a **Refusal** line naming its sentinel and its
class.

**Why the wiring class covers a pairing mistake and not only a composition one.**
`ErrWrongStore` and `ErrWrongStream` are the same shape: two values the framework
minted apart, put together by a hand. Neither arrived with a request and neither
is anything a client can influence, so a transport must answer the same way for
both — which is [[D-040]]'s line that the class decides the status and the retry
posture. `ErrKey` is the counter-example that fixes the boundary: a blank identity
*did* arrive with a request, so it is in the request class (§UC-051).

**The intra-class wraps, and there are exactly two.** §INV-024's falsification
needs a pairwise ground truth. For a cross-class pair the answer is always
*false*; within a class it needs this list, which is closed:

| Intra-class wrap | Why |
|---|---|
| `ErrSealed` → `ErrDeclaration` | A late declaration is a malformed declaration discovered later, so a test that matches the general sentinel still catches it (§UC-005) |
| `ErrCodecType` → `ErrDeclaration` | §UC-004 lists "a codec that cannot encode its own reader type" among the declaration's own triggers, so the specific refusal must stay reachable from the general one (§UC-056) |

**No other sentinel wraps another.** The wiring, request, history, write and store
classes each hold a flat set: no general sentinel, no specialisation, therefore no
edge. If an edge is ever added it is added here, and §INV-024's table test reads
this list rather than inferring one (GAP-48, GAP-62).

**The four declared class wraps, and there are no others.** A *class* wrap is a
refusal reaching out of this vocabulary to a class **a transport already maps**,
which is [[D-015]]'s rule; it is what a caller's `errors.Is` is meant to find.

| Wrap | Why |
|---|---|
| `ErrTooLarge` → the framework's too-large class (`errs.KindTooLarge`) | A transport that already maps that class needs no new registration ([[D-015]]) |
| `ErrConflict` → the framework's conflict class (`crud.ErrConflict`) | The same, and [ES] says so by name: "An append conflict is that class. It is not a new code and not a retry signal" |
| `ErrBackend` → the framework's retryable class, when and only when the store classified the failure retryable | [[D-040]]'s taxonomy: a serialisation failure, a deadlock and a lock timeout are retryable and are never a client error |
| `ErrUpcast` → the application's own error | The one place an application-supplied pure function may say "no", and the application must be able to match its own error with `errors.Is` |

**A cause is not a wrap, and the distinction has to be written down.** A refusal
produced from somebody else's error keeps that error as its **cause**: reachable
with `errors.Is`, rendered opaquely so its text does not travel (§INV-025).
`ErrBackend`, `ErrConflict`, `ErrCursor`, `ErrClosed`, `ErrUncertain` and
`ErrAmbientNotTransaction` may all carry a store's error as a cause, because all
six are produced from one; the four rows above are about **classes** and stay
closed at four. Calling the two one thing is how a round-5 list gave one row two
jobs.

**The one obligation a cause carries:** a store's own error must not itself be a
sentinel of this vocabulary. If it were, `errors.Is` from a refusal of one class
could reach a sentinel of another through the cause, and §INV-024's partition would
be false through a back door. A store has no reason to hold one — it never adds a
sentinel, and its classification is an `Outcome` — so this is a checkable
prohibition rather than a burden (§INV-024, §INV-045).

> **Superseded by [PLAN] S1 and S6 ([[D-122]]).** The prohibition is **not
> checked; it is made harmless.** The refusal wrapper gates the *target* of the
> traversal rather than the promotion of a wrap: no wrap answers for a sentinel
> of this vocabulary, whatever a cause carries. Checking it instead cost the
> decorator's and the upcaster's own error its `errors.Is` reachability
> (GAP-147, GAP-159), and that error is the whole point of `ErrRefused` and
> `ErrUpcast`.

`context.Canceled` and `context.DeadlineExceeded` are **not** sentinels of this
subsystem and are never wrapped by one: they travel as themselves (§INV-029).

**How a store reaches this table, since it may not add to it.** It **classifies**
its own failure through §D.14's `Outcome` — a closed exported set of six values —
and the kernel maps that classification into exactly one row; §D.14 carries the
map and §INV-045 the invariant. That is why `ErrBackend` exists and why
`ErrUncertain` is not something a store spells. The store's own error rides along
as the wrapped cause, so `errors.Is` reaches it while §INV-025 keeps its text from
travelling.

> **Superseded by [PLAN] S1 and S6 ([[D-122]], GAP-135).** The cause rides along
> and **neither `errors.Is` nor `errors.As` reaches it**; `event.CauseOf` is the
> one reader that does. `port.KindOf` falls through `errs.AsFault` into six
> `errors.Is` branches over `crud` sentinels, so a cause that happened to carry
> `crud.ErrUnavailable` — an ordinary driver class — would render an **uncertain
> commit** as *retry me*, and retrying an unconfirmed append writes the same
> decision twice. The two exceptions are `ErrRefused` and `ErrUpcast`, whose
> cause **is** their declared wrap.

**How the set grows, since it is frozen with the contract.** Adding a sentinel to an existing class
is additive and safe: nobody matched a sentinel that did not exist. Adding a
**class** is breaking, because the class is what a caller matches. Moving a
sentinel between classes is breaking for the same reason. That is the whole
compatibility rule, and it is three sentences instead of a matrix.

**Two rules the classes are written to keep.** A refusal names declared
identifiers and never data (§INV-025), so no sentinel's message may carry a key, a
payload, a version, a position or a cursor. And a wrapped class is never replaced
([[D-015]]).

---

## 3. Actors and entry modes

### Actors

| # | Actor | What it is | What it may hold |
|---|---|---|---|
| **A1** | **Application author** | Writes the aggregate: its state type, its event types, its revisions, its codecs, its folds, its decision functions | Go types and codecs. Never a store, never a version, never a position |
| **A2** | **Composition root** | Constructs the store, binds declarations to it, owns lifetime and close | The store. Never a decision |
| **A3** | **App-usecase** | Loads, calls the decision, appends; owns the transaction boundary when there is one | A repository, an aggregate identity, a state, an at-token, a context — sometimes one `Within` marked — and sometimes a transaction it opened. **Never a *bound* repository**: `Within` returns a context, not a second object (§UC-028) |
| **A4** | **Bounded consumer** | Reads envelopes in global order within a bound. Phase 1 gives it the read; it is not a running activity | A cursor it obtained from a previous read, and a `*Reader` over a read-only `Log` — never a value it can append through (§UC-036) |
| **A5** | **Store implementer** | Writes a `Store`: `eventmemory` now, `eventpg` in phase 2, anything later | The contract and the conformance suite |
| **A6** | **Test author** | Unit-tests an aggregate with no database; runs the conformance suite | The in-memory store |
| **A7** | **Decorator author** | Wraps a store to observe or police it | The wrapped store, and the obligation to forward exactly |

Every actor has at least one happy path before any failure path, and every row
below resolves to a use case whose actor matches and whose tag is `[happy]`:
A1 → UC-001, UC-002, UC-003; A2 → UC-006, UC-007; A3 → UC-019, UC-028;
A4 → UC-036; A5 → UC-043; A6 → UC-040; A7 → UC-048.

### Entry modes

| Mode | Description | When failure must be visible |
|---|---|---|
| **E1 Declaration** | Package-level construction of aggregate and event declarations, **including the codec** | At declaration, before `main` runs ([[D-021]]) |
| **E2 Composition** | Store construction, binding, and opening a read at start-up | At construction, at `Bind` or at `Read`, as a returned error; never as a panic, because a store's spec carries deployment values (§UC-006, §INV-022) |
| **E3 Request** | Load, decide, append inside one operation | Before any write, as a typed refusal |
| **E4 Caller transaction** | The same, inside a transaction the caller opened | Before any statement is issued on that transaction |
| **E5 Bounded read** | A consumer reading envelopes by cursor | Before any envelope is returned |
| **E6 Test** | Fold-only replay, memory-store use, conformance | At the assertion, with a control that proves the assertion was not vacuous |

---

## 4. Use cases

Format: **Actor / Precondition / Trigger / Flow / Observed / Must not happen**,
with **Why** where a governing document is the reason and **Control** where the
use case could otherwise pass vacuously ([[D-020]]). A use case that refuses also
carries **Refusal**, naming its sentinel and its class in §2.3. A use case whose
outcome decides what the caller's next at-token is carries **Token**, naming its
row in §INV-040.

Numbering runs UC-001…UC-049 from the first draft, UC-050…UC-058 from round 2 and
UC-059…UC-060 from round 3, appended rather than inserted so that no existing
citation moved. Round 4 adds UC-061…UC-063 the same way and marks UC-023
`WITHDRAWN` in place rather than deleting the number; round 5 adds
UC-064…UC-066, round 6 adds UC-067 and round 9 adds UC-068.

### Group A — Declaration (A1, mode E1)

#### UC-001 An application declares an aggregate, its state and three event types, one of them with two revisions  [happy]
- **Actor** A1.
- **Precondition** A Go state type with a usable zero value; three payload
  structs; a stream family string the application chose; a codec per retained
  revision — in the ordinary case one call to the framework's JSON codec, reused.
- **Trigger** Package-level declaration: one aggregate declaration, then one
  event declaration per type, each naming its wire identifier, its reader chain
  and its fold.
- **Flow** The aggregate declaration records the family and the identity-to-key
  mapper. Each event declaration records a wire name, the reader type **and codec**
  for every retained revision, the upcaster between consecutive revisions, the
  current revision (derived from the chain length, never stated twice) and the
  fold. One type declares two revisions: revision 1's reader is the historical
  payload struct with the codec it was written by, revision 2's reader is the
  current struct with its codec, and one upcaster joins them.
- **Observed** Three typed fact handles, each usable to emit a change **for a
  named identity of this aggregate** — a fact handle knows its aggregate, so it
  knows the mapper, so a change it mints carries the stream it was decided for
  (§UC-064). The current revision of each type is a derived fact, not a literal
  the author maintains. Because each revision carries its own codec, a revision
  bump may change the encoding without a data migration.
- **Must not happen** No package-level registry is written to. No reflection
  derives a wire name from a Go type name. Nothing is registered by import side
  effect. Declaring the same aggregate twice in one process must not interfere.
- **Why** [ES]: "Event type and revision are declared wire identifiers, not Go
  type names, translated labels, tenant-derived strings or reflection output."
  The codec's placement follows the tree — `jobs.DefinitionSpec[P].Codec` and
  `jobs.Upcast(from Codec[A], to Codec[B], fn)` already put the codec on the data
  (§D.2).

#### UC-002 A second, unrelated aggregate is declared in the same process  [happy]
- **Actor** A1.
- **Precondition** UC-001's aggregate exists.
- **Trigger** A second declaration with a different family, taken twice: once
  with its own state type and once **over the first aggregate's state type**,
  which is what two bounded contexts reusing one value type or a generated
  declaration set produces.
- **Flow** Two independent declaration values exist. Neither can see the other's
  types.
- **Observed** Two answers, because the caller-seam types are parameterised by the
  **state type** and not by the aggregate (§INV-020). With two state types, a
  change emitted for aggregate A is not assignable to a slice of changes for
  aggregate B: a **compile error**, no runtime. With one state type the two are
  the same Go types throughout, the assignment compiles, and what refuses it is
  the call-time comparison — a change carries its `Stream`, whose family is A's,
  so `repoB.Append` returns `ErrWrongStream` and writes nothing (§INV-044).
- **Must not happen** The two must not share a type table, a name space or any
  mutable state. A wire name reused across two aggregates must not collide,
  because the tables are per-aggregate — but a wire name reused *within* one
  aggregate must be refused (UC-004). **The family is the opposite case and the
  dangerous one:** two declarations sharing a *family* string are two aggregates
  over one history, refused where it can be seen (UC-059) and stated as an
  obligation where it cannot (§INV-039). A reused wire name is harmless because
  the tables are per-aggregate; a reused family is not, for the same reason — the
  tables being separate is what stops the merged stream refusing anything.
- **Control** Two, one per answer. A build-failure fixture over the two-state-type
  pair, with the matching combination asserted to build, so the compile error is
  not "nothing compiles". And a runtime assertion over the shared-state-type pair
  that the crossing is `ErrWrongStream` with zero events, with the matching
  combination asserted to succeed — without which §INV-020 is proved only for the
  shape it was written from.

#### UC-003 The application declares a fact whose payload shape stayed the same across a revision bump  [happy]
- **Actor** A1.
- **Precondition** A revision whose *meaning* changed while its fields did not.
- **Trigger** The reader chain declares an upcaster whose input and output types
  are the same Go type.
- **Flow** Revision r and r+1 both decode into the same struct, each with its own
  codec value; the upcaster carries semantics, not shape.
- **Observed** Legal and unremarkable; both revisions have one reader path.
- **Must not happen** The framework must not refuse this, and must not collapse
  the two revisions into one.

#### UC-004 A declaration is malformed  [edge]
- **Actor** A1.
- **Precondition** None.
- **Trigger** Any of: an empty or over-long family; an empty or over-long wire
  type name; a family or wire type name that breaks the kernel's text rule
  (§INV-033) — the cap and the rule are kernel constants, not deployment
  settings, because a declared identifier must be legal in *every* store's
  identifier space; the same wire name declared twice on one aggregate; a payload
  type that is not a struct; a nil fold; a nil upcaster; a nil identity mapper; a
  nil codec on any link of the chain; **a codec that cannot encode its own reader
  type** (UC-056).
- **Flow** The declaration refuses.
- **Observed** A panic at declaration time — before `main`, before any store
  exists, before any request — naming which rule was broken and which declared
  identifier broke it. The panic's value is an error carrying `ErrDeclaration`,
  so a test that recovers it matches with `errors.Is` rather than by string.
- **Must not happen** No malformed declaration may survive to be discovered at
  append or load time. No malformed declaration may be "fixed" by a default.
  **There is no error-returning sibling of the declaration calls.** A declaration
  binds Go types at compile time, so there is no dynamic path that could use one:
  an error return would exist only to be discarded by an `init`, and it would give
  a second, unsealed way to build a table (§INV-013). A test asserts a refusal by
  recovering the panic and matching the sentinel.

  > **Superseded by Q21's answer in [PLAN] and by [[D-123]].** There **is** a
  > sibling and it is spelled `TryDefine`/`TryDeclare`, not `MustDefine`. The
  > clause above was written against the `Define`/`MustDefine` **inversion**,
  > which is not what shipped: the panicking name stays the short one an
  > application writes at package level, and the `Try…` name is what the
  > twenty-two negative cases are written against instead of twenty-two
  > `recover()` blocks. §INV-013 survives because both calls answer the *same*
  > value through one code path — the panicking one is three lines over the
  > returning one and has no body of its own — so there is no second, unsealed
  > way to build a table.
- **Why** [[D-021]]: "the magic must fail early ... magic at the call site,
  strictness at the declaration". A reflective path that reports at request time
  is the worst of both worlds. The codec being here is what makes the one check
  that used to be homeless — "can this codec encode this type" — a
  declaration-time check like every other.
- **Why a payload must be a struct, provisionally** Not because a codec could not
  encode `type Amount int64` — every codec this document imagines can — but
  because a non-struct payload has no evolution path: it cannot gain a field, so
  its only revision bump is a whole new reader type and the chain buys it nothing.
  Whether that is reason enough to refuse rather than to let the codec answer is
  deferred (§8, GAP-41).
- **Refusal** `ErrDeclaration`, declaration class (§2.3).
- **Control** The valid declaration in the same test builds, so the test cannot
  pass by refusing everything.

#### UC-050 An aggregate's identity is composite and not a string  [happy]
- **Actor** A1.
- **Precondition** An identity that is a struct — a tenant and a number, a
  region and an account — which is the ordinary case outside a sample. It may
  hold a `[]byte` hash or any non-comparable field: an identity is never a map
  key in this design, so nothing constrains it to be comparable.
- **Trigger** Declaring the aggregate with a mapper from that identity to a key.
- **Flow** The mapper renders the parts through the framework's composing helper,
  which length-prefixes each part, so no two distinct part lists render to one
  key. The author does not concatenate.
- **Observed** Identities `{tenant: "ab", number: "c1"}` and
  `{tenant: "a", number: "bc1"}` are two streams, and stay two streams under
  every store the contract admits.
- **Must not happen** The framework must not offer a mapper helper that
  concatenates without delimiting, must not derive a key from `fmt` formatting of
  a struct, and must not case-fold or Unicode-normalise anything on the way. It
  must not require the identity type to be comparable — that would refuse a
  composite identity holding a hash, and it would buy nothing.
- **Why** Nothing in a "render my identity" seam naturally forbids *two identities
  rendering as one key*, and the result is two aggregates sharing one history,
  folding each other's facts, with no error at any point. Injectivity is an
  obligation the application owes and the framework cannot verify in general
  (§INV-033) — so the framework's job is to make the correct rendering the
  shortest to write and to give the obligation a testable proxy.
- **An identity whose parts are not text** hex- or base64-encodes them in one line
  before composing. That is the whole cost of the kernel text rule, and it buys
  keys that are greppable in a database (§INV-033).
- **Control** The same test maps a one-field identity with the plain conversion
  and asserts it still works, so the helper is not the only expressible mapper —
  **and asserts the two spellings of that one identity produce two different
  keys**, because the helper length-prefixes and the conversion does not. Both are
  legal and they are not interchangeable: switching after a stream exists rewrites
  where that identity's history lives (§INV-005), and a control reading as "either
  is fine" would teach the trap.

#### UC-052 An aggregate's state reaches a slice or a map, at any depth — and it costs no declaration  [happy]
- **Actor** A1, then A3.
- **Precondition** Any of the shapes a real aggregate has, and they are not all
  flat: `{ Lines []Line; Applied map[string]bool }`; a nested one,
  `{ Header CartHeader; Totals Totals }` where `CartHeader` holds a `[]string`;
  an array of structs, `{ Items [8]Item }` where `Item` holds a map; a state type
  that **is** a reference kind, `type Ledger map[string]int64`; **and a state
  holding a foreign value type — `time.Time`, `netip.Addr`, `big.Int`,
  `decimal.Decimal` — every one of which reaches a pointer or a slice through
  unexported fields the application does not own.**
- **Trigger** Declaring it, loading it, calling a decision on it, appending, and
  deciding again in the same operation.
- **Flow** Nothing happens. There is no walk over the state type, no copier
  argument, no option and no refusal. `Load` folds a private accumulator and
  returns it **by value, once, on the way out**, retaining nothing; `Append` takes
  an at-token and a change list and never sees a state; the caller's local advance
  is `agg.Fold(id, state, changes...)`, over the caller's own memory (§UC-041).
- **Observed** All seven shapes above declare in exactly the same number of
  lines. A decision function that writes into `s.Applied`, into `s.Header.Tags`,
  into `s.Items[0].Meta` or into a `Ledger` mutates **the caller's own value**,
  which nothing in the framework can see and nothing in the framework retains. A
  reload returns the fold of the stream, which is the authority.
- **The one place the framework does fold the caller's value, and it is not a
  declaration concern** `Aggregate.Fold` runs the application's own folds over the
  state the caller handed it, so it writes through every reference kind that state
  reaches — by contract, stated once in §UC-041 and enumerated once in §INV-021's
  inbound table. That is a property of `Fold`'s **arguments**, not of the state
  type, so all seven shapes still declare in the same lines and need no copier.
- **Must not happen** The framework must not walk the application's type graph;
  must not require, accept or apply a copier; must not deep-copy by reflection;
  and must not refuse a state type for the kinds it contains. It must not retain
  the state value it handed out (§INV-042) — that is the property that makes all
  of the above true, and it is the only thing that has to hold.
- **Why round 3's mechanism is deleted rather than fixed** The transitive copier
  walk **refused** `struct{ PlacedAt time.Time; Total int64 }`, the most ordinary
  aggregate state there is; the only copier writable for it was
  `func(o Order) Order { return o }`, the exact value the mechanism's own control
  called wrong; its proxy could not falsify it, because nothing outside `time` can
  mutate a `time.Time`; and it was satisfied once per aggregate rather than once
  per kind. A mechanism whose escape hatch permanently disarms it is worse than
  none: it teaches the author that the question is answered. Deleting it is safe
  because the object it protected — a live accumulator the kernel keeps and hands
  out repeatedly — no longer exists.
- **The residual, stated plainly** A caller who mutates the state it was handed
  and then folds changes onto that mutated value gets a state no event sequence
  produces. That is true of any value in any Go program, the framework holds none
  of it, and a reload disagrees with it — which is the point: **the reload is the
  authority, and it is one line away** (§UC-061 is the control that pins it). The
  second route to the same place, `Fold` advancing the caller's own value in
  place, is §UC-041's contract and §D.10's sixth residual.
- **Control** The declaration of a `time.Time`-holding state in the same test is
  accepted with no extra argument, so the case cannot pass by refusing everything;
  and §UC-061 mutates a handed-out state and asserts a reload is unaffected, so
  "the framework retains nothing" is falsifiable rather than asserted.

#### UC-056 A payload type its own codec cannot encode is declared  [edge]
- **Actor** A1 — the codec is on the declaration, so the actor and the mode are
  the declaration's.
- **Precondition** A declaration whose current or retained reader types include
  one the codec declared beside it cannot encode deterministically — a map with a
  key type it will not order, an interface field, a type whose custom marshaller
  it rejects.
- **Trigger** Declaring the fact.
- **Flow** `From` and `Then` each hold a `Codec[V]` for a known `V`, so the
  question "can *this* codec encode *this* type" has both halves at declaration
  time. The declaration asks it once per retained revision, against a zero value
  of that revision's reader type.
- **Observed** A panic at declaration, before `main`, naming the declared type
  name and the field.
- **Must not happen** This check must not be deferred to `Bind` — a store-owned
  codec forces `Bind` to interrogate one through the store contract, which is what
  makes `Store.Codec()` necessary and makes the per-request store of UC-055 pay
  for the walk on the request path (§D.2). It must not be deferred to the first
  append, which is request time. And the kernel must not hold a hardcoded
  encodability rule of its own — the codec answers, which is why `Codec` carries
  the question.
- **Why** `microkernel.md`'s policy-in-the-kernel case: with the codec on the
  data, both halves of the question exist before `main`, so [[D-021]] is satisfied
  outright rather than at the next-best moment.
- **What this costs a deployment, and it is not free** §D.2 prices it: three
  consequences, of which the sharpest is that a team wanting JSON in development
  and a compact binary format in production now performs a data migration instead
  of changing one spec field.
- **Refusal** `ErrCodecType`, declaration class (§2.3), wrapping `ErrDeclaration`
  — the vocabulary's second intra-class wrap, and it exists because §UC-004 lists
  this trigger among the declaration's own, so a test that matches the general
  sentinel must reach this one.
- **Control** The same declaration with a codec that accepts the type builds, so
  the check cannot pass by refusing everything.

#### UC-005 A fact is declared after the aggregate's table has been read  [edge]
- **Actor** A1 (by mistake, e.g. from an `init` in another package or at runtime).
- **Precondition** Any reader has observed the aggregate's type table.
- **Trigger** A further event declaration on the same aggregate.
- **Flow** The table is sealed by the **first observation**, whatever performs it;
  the late declaration is refused.
- **Observed** A panic naming the aggregate family and the wire name that arrived
  late, carrying `ErrSealed`.
- **Must not happen** The type table must not be mutated after any reader has been
  able to observe it, and a late declaration must not silently succeed and race a
  concurrent load. **Sealing must not be triggered by binding alone**: two of the
  three testing entry points read the table without ever binding it — the
  store-free fold reads the folds, the round-trip helper the reader chain and its
  codec — so a bind-triggered seal leaves the table mutable while it is read.
- **The readers that seal**, exhaustively for phase 1: binding an aggregate to a
  store; `Aggregate.Fold`; the round-trip helper; any read of a fact's current
  revision or reader chain — **which includes `Fact.New`, because it encodes and
  because it renders the key through the aggregate's mapper**; and the conformance
  suite. Adding a reader without adding it here is the defect (§INV-013).
- **Why** Sealing on first observation — not on first bind — is what makes the
  table read-only for its whole observable life, which is the property §INV-013's
  own **Why** names the tree's precedent for.
- **What a late declaration on a request goroutine does, said rather than denied**
  It panics, there and then, unrecovered. This is the one route by which the
  declaration class reaches a request goroutine, which is why §2.3's row says a
  declaration refusal is *never returned as an error* rather than *never reachable
  at request time* (GAP-61). Taking the process down for a programmer error is
  deliberate — the alternative is a table mutable while it is being read — and a
  transport that recovers panics sees it like any other.
- **Refusal** `ErrSealed`, declaration class (§2.3), wrapping `ErrDeclaration` —
  one of the vocabulary's **two** intra-class wraps (GAP-48).

### Group B — Composition (A2, mode E2)

#### UC-006 The composition root constructs a store and binds two aggregates to it  [happy]
- **Actor** A2.
- **Precondition** Two declarations from group A.
- **Trigger** Store construction with an explicit spec (bounds, clock, and what
  it writes to), then `Open` over the store and one `Bind` per aggregate.
- **Flow** `Open` starts nothing and cannot fail. Each `Bind` yields a typed
  repository. The store is owned by the composition root and closed by it.
- **Observed** Two repositories, each of which will only accept its own
  aggregate's changes, its own aggregate's identity type and its own aggregate's
  at-tokens.
- **One `Binding` per store value, and it is an obligation rather than a
  mechanism** `Open` may be called more than once, so the `Binding` is the
  composition root's single binding table — `jobs.NewCatalog`'s shape — threaded
  to each feature module that binds an aggregate. What a second one costs is
  §INV-039's.
- **Must not happen** The constructor must start no goroutine, no ticker, no
  reaper and no publisher. It must register nothing process-wide. Importing the
  package must activate nothing. **The store constructor returns an error** and
  never panics: it carries deployment values a deployment can get wrong, and E2's
  failure shape for those is a returned error. The rule is stated here once, and
  `Bind`, `Read` and every sketch in this document obey it.
- **Why** [ES]: "No constructor starts a goroutine or mutates a global registry."
  [EXT]: "Importing it must not register globals, discover application components
  or activate unused adapters."
- **Control** A goroutine-count probe before and after construction; a
  process-global enumeration before and after import.

#### UC-007 Two stores over two backings exist in one process  [happy]
- **Actor** A2.
- **Precondition** Two stores over **different backings** (in phase 1, two memory
  stores each owning its own log; in phase 2 a memory store beside a real one).
- **Trigger** The same aggregate declaration is bound to both, through two
  `Binding` values.
- **Flow** Two repositories over one declaration. The two backings are different,
  so the two stores answer different identities.
- **Observed** Streams in one are invisible in the other. An at-token minted by
  one cannot be appended through the other (UC-031).
- **Must not happen** No ambient or default store may exist anywhere. There must
  be no spelling for "the current store".
- **Note** The invisibility is a property of *two backings*, not of two store
  values; two stores over **one** backing are UC-054 and behave the opposite way.

#### UC-008 The composition root publishes a readiness answer  [happy]
- **Actor** A2.
- **Precondition** A constructed store.
- **Trigger** The root wraps the store's probe in a health contribution.
- **Flow** The store answers one question — can it serve — as one error or nil,
  naming nothing about itself.
- **Observed** The composition root chooses the importance, the public code and
  whether the answer is exposed at all.
- **Must not happen** The store must not name its own importance, choose a status
  code, register anywhere, or render a driver's or codec's message text.
- **Why** [[D-091]] and [[D-096]]: the subsystem owns the seam, the composition
  root owns the choice, and there is no health package per subsystem.

#### UC-054 Two store values share one backing  [happy]
- **Actor** A2.
- **Precondition** Two store values constructed over the same backing: two
  memory stores over one explicitly shared log, or two SQL stores over one
  database.
- **Trigger** An at-token minted through the first store's repository is appended
  through a repository bound to the second.
- **Flow** Each store answers a backing identity derived from **what it writes
  to**, not from the store value: the memory store from its log, a SQL store from
  `crud.KeyOf(source)`. The two answers compare `Equal`, so the append proceeds.
- **Observed** The append succeeds and lands in the one history. Streams written
  through either store are visible through the other.
- **What the two values must agree on** Visibility is a claim about the *data*, so
  the two must agree on everything the data depends on, and that is exactly two
  numbers: `MaxPayload` and `MaxKey`. A store constructed over a non-empty backing
  that disagrees on either is refused at construction, because otherwise a key
  legal through one value is illegal through the other and "visible through the
  other" is false for reasons nothing reports. They need **not** agree on the
  operational numbers — batch bound, stream page size, global read cap — since
  none of those makes data written through one unreadable through the other. The
  codec is not on the list: it belongs to the declaration (§D.2, §D.4).
- **The one exception to "one store for every purpose", named where it bites**
  The set of **bound families** is not shared: it lives on a `*Binding`, and two
  store values over one backing are opened separately, so §UC-059's collision
  check does not fire across them. That is not a loophole in the identity rule but
  the honest scope of a per-value check (§INV-039), and the rule gives way because
  it is about *data* — a bound-family set is not data.
- **Must not happen** The comparison must not be pointer equality of the store
  value, must not be pointer equality of a value a decorator may have replaced,
  and must not depend on which of the two values minted the token. Two values that
  disagree on a data-bearing setting must not silently become "one store".
- **Why** Store identity as *instance* identity is wrong the moment a deployment
  constructs a store per request over a borrowed tenant lease, which is [ES]'s own
  tenancy composition (§INV-016, §UC-055).
- **Control** The two-backings case (UC-007) must still refuse, so a store that
  answers a constant identity fails the control instead of passing this case.

#### UC-055 A store's lifetime is one request  [happy]
- **Actor** A2, in a tenancy deployment.
- **Precondition** A per-request datasource the caller borrowed and owns — a
  tenant lease.
- **Trigger** Construct a store over it, open a binding, bind the aggregate,
  serve the request, close the store, release the lease.
- **Flow** Construction acquires nothing but what its spec names; it opens no
  connection and starts nothing, so it is cheap enough to be per request. Binding
  reads the sealed declaration's family and allocates the repository.
- **What a bind costs, stated so an implementer can be held to it** §D.4's four
  constant-time checks and one allocation. **No codec walk**: an O(retained
  readers) codec interrogation here is the work a per-request store can least
  afford, and the codec being on the declaration deletes it (§D.2, GAP-35).
- **Observed** The pattern is ordinary rather than exotic. An at-token may cross a
  store boundary within one backing (UC-054) and may not across backings.
- **Must not happen** A per-request store must not re-seal a declaration, must not
  mutate it, and must not take a lock on state shared between requests — those are
  what put contention on the request path. Close must not close a datasource the
  store did not open. An at-token must not outlive the correctness of its version
  — it does not, because the version is checked at append against the backing, not
  against the store value.
- **Why** [ES] shows this exact shape: `New(Spec{Source: lease.Source()})` per
  request over a borrowed lease. A contract that only works for a
  process-lifetime store is wrong for the framework's own delivered tenancy
  design.

#### UC-059 Two aggregates declare the same stream family  [edge]
- **Actor** A1 by mistake, caught by A2.
- **Precondition** The ordinary route: an aggregate declaration copied to start a
  second one, with the state type changed and the family string not. Because the
  event type names were copied too, every wire name in the merged stream is
  declared on *both* sides, so UC-013's unknown-type refusal — the one that
  catches the cross-family case — never fires.
- **Trigger** The composition root binds both aggregates through one `Binding` —
  which is the shape §UC-006 makes the composition root's obligation, and the
  scope of everything below.
- **Flow** The second `Bind` refuses. A `Binding` value remembers which families
  have been bound **through it**, which is per-value state and not the
  package-level registry §INV-013 forbids.
- **Observed** A refusal at start-up naming the family and both aggregates'
  declared identifiers. Nothing reaches a request.
- **Must not happen** Two aggregates must not share a history. If they do, both
  load each other's facts, both fold them, both append at versions derived from
  the other's history, and there is no error at any point — byte for byte the
  corruption §INV-033 is written for, reached by the one route injectivity does
  not cover.
- **What the framework cannot catch, said plainly** Two escapes — two families
  equal across two *processes*, and two equal in one process bound through two
  `Binding` values — neither closable by a mechanism. §INV-039 carries the
  argument, the scope and what is offered instead; §UC-054 names where the
  per-value scope contradicts the identity rule; Q19 carries the residual and its
  additive answer.
- **Refusal** `ErrFamily`, wiring class (§2.3).
- **Control** Two aggregates with two families bound through one `Binding` must
  both succeed. And binding the **same** declaration twice must also succeed,
  returning an equivalent repository — one family, one type table, one history, so
  refusing it would fail the ordinary composition root that binds per feature
  module. The refusal is for two *different* declarations sharing a family, which
  is what the check must tell apart.

### Group C — Loading and folding (A3, mode E3)

#### UC-009 A fresh, never-written stream is loaded  [happy]
- **Actor** A3.
- **Precondition** No events for this stream.
- **Trigger** Load by aggregate identity.
- **Flow** The store returns no envelopes; the fold runs zero times.
- **Observed** The **zero value** of the state type and an at-token at version 0.
  **No error.**
- **Must not happen** There must be no "stream not found" refusal, and no
  distinction of any kind between "never existed" and "exists with no events" —
  they are the same thing and the framework must not invent a difference.
  Whether the aggregate *exists* is a domain question the aggregate's own state
  answers (a flag its first event sets), never a framework error.
- **Why** A not-found refusal forces every caller to write the same branch and
  tempts them to treat framework absence as domain absence — the "creating an
  aggregate is a special path" defect that then needs a blind create-only append.

#### UC-010 A stream with history is loaded and folded  [happy]
- **Actor** A3.
- **Precondition** n events at revisions the declaration knows.
- **Trigger** Load.
- **Flow** Envelopes arrive in ascending stream version. For each: the reader for
  (type, revision) decodes the bytes **with that revision's own codec**, the
  upcaster chain carries the value to the current type, the fold applies it to a
  private accumulator.
- **Observed** The fold of all n events over the zero value, returned by value,
  and an at-token at version exactly n.
- **Must not happen** The fold must perform no I/O, no clock read, no random
  draw, no telemetry emission and no side effect of any kind. No partially folded
  state may be returned if any step fails (§UC-013..UC-016) — the **zero** state
  is returned with the error, never the accumulator. The accumulator must not be
  reachable by anything but the fold loop, and must not be retained after it
  returns (§INV-042).
- **Why** [ES]: "Rehydration and upcasting perform no I/O, telemetry, clock,
  randomness, broker send or application side effect."

#### UC-011 A long stream is loaded  [happy]
- **Actor** A3.
- **Precondition** A stream far longer than any sensible page (tens or hundreds
  of thousands of events).
- **Trigger** Load.
- **Flow** The store is read in bounded pages of `Limits().StreamPage`; each page
  is folded and released before the next is requested.
- **Observed** The same state as a single-shot read would give. Memory in use is
  bounded by one page and by the state itself, **not by stream length**; §D.13
  quantifies it.
- **Must not happen** The whole stream must not be materialised at once. A page
  boundary must not drop, duplicate or reorder an event. A read that stops early
  must not return a truncated state as if it were complete.
- **Control** A test that folds the same stream at two different page sizes,
  including a page size of 1, and asserts identical state and version. Without
  paging the small-page case is impossible; with a broken boundary the states
  differ.

#### UC-012 A stream contains an event at a revision older than the current reader  [happy]
- **Actor** A3.
- **Precondition** History written at revision 1; the declaration's current
  revision is 2.
- **Trigger** Load.
- **Flow** The envelope's revision selects revision 1's reader type **and its
  codec**; the bytes decode into it; the single declared upcaster produces the
  current type; the fold applies it.
- **Observed** State identical to what the same facts would produce if they had
  been written at revision 2.
- **Must not happen** The upcaster must not read a clock, a config value, an
  environment variable, a database or a random source. It must not be applied
  twice. There must not be two paths from revision 1 to the current type. The
  current revision's codec must not be used on an older revision's bytes — a
  defect one codec per store could not even express.
- **Control** The same test folds a stream mixing revision 1 and revision 2
  events and asserts the state matches a stream of equivalent revision-2-only
  events. Deleting the upcaster must make the test fail rather than silently
  decode into the current type.

#### UC-013 A stream contains an event type the declaration does not know  [edge]
- **Actor** A3.
- **Precondition** History contains a wire type name absent from this aggregate's
  table — a type retired without an upcast path, a foreign writer, a stream key
  collision across families.
- **Trigger** Load.
- **Flow** The read stops at that envelope.
- **Observed** A typed refusal reaching the caller with the **zero** state and a
  zero at-token. The refusal names the declared identifiers involved — the family
  and the unknown wire type — and nothing else.
- **Must not happen** No partially rehydrated state may be returned. The unknown
  event must not be skipped, counted, or tolerated by a "lenient" option — no such
  option exists. The returned at-token must be the zero one, whose backing is
  invalid, so a caller that ignores the error and appends anyway is refused rather
  than writing at a fabricated version (§INV-015).
- **Why** [ES]: "Unknown, malformed or unsupported revisions fail before
  returning a partially rehydrated aggregate."
- **Refusal** `ErrUnknownType`, history class (§2.3).
- **Note** "A stream key collision across families" is a *cause* this refusal can
  have. Two collisions produce no refusal at all, because every type in the merged
  stream is declared: two identities of one aggregate rendering to one key
  (§INV-033), and two aggregates declaring one family (§INV-039). Both are
  obligations with their own invariants rather than things this refusal catches.

#### UC-051 The identity mapper produces an illegal key  [edge]
- **Actor** A3, through A1's mapper.
- **Precondition** An identity whose mapper output is empty — the zero value of
  an identity type is the ordinary way to reach this — or longer than the store's
  declared key cap, or breaking the kernel text rule (invalid UTF-8, a NUL, a
  control character).
- **Trigger** Any call that names that identity — `Repo.Load`, `Fact.New` or
  `Aggregate.Fold` — or any `Append`, which names none but presents the keys the
  token and the changes carry.
- **Flow** **A key is validated at every door it crosses, produced or presented**,
  before any statement and before the store is reached, and the two rule sets
  differ only in where their number comes from. The **kernel's** rules — non-empty
  and the text rule — are applied at all four calls: at `Load`, `New` and `Fold`
  because the mapper runs there, and at `Append` because it is the last door
  before a write and the key it is handed was rendered by nobody it can see. The
  **store's** `Limits().MaxKey` is applied at the two calls that know a store,
  `Load` and `Append`; reading `Limits()` issues nothing, so both stay before the
  store is reached, and **§D.5 fixes where the check sits in each call's order** —
  first in both. Calling this "three doors" while saying the mapper runs at
  `Append`, which it does not, and taking `Append` out of the kernel's rules
  altogether, are the two ways this has already been got wrong (GAP-76, GAP-82);
  the second left §INV-015's first defence with nothing performing it. A `Change`
  whose key is illegal also carries the refusal
  on itself, exposed by `Change.Err()` and surfaced by `Append`, which is the
  mechanism a deferred encoding failure already uses (§D.3).
- **Why `Append` applies the kernel's rules and does not merely trust them** Every
  key that reaches it *through the framework* was validated where it was produced,
  so the check never fires on a program the framework built — which is why it is
  worth its cost: the only value that can fail it is one no mapper made. A zero
  `At[S]{}` is that value, it is legal Go to write (§INV-015), and this is the
  check that refuses it, at one scan of at most `MaxKey` bytes per append (§D.13).
- **Observed** A typed refusal naming the family and the rule the key broke, and
  **not the key**.
- **Must not happen** An empty key must not be accepted: every zero-valued
  identity shares it, so it is UC-050's same-history merge with no composite
  identity needed. A key must not be truncated to fit the cap, padded, hashed or
  otherwise "fixed" — each maps two identities onto one key. No layer may
  normalise Unicode, fold case or trim: an NFC and an NFD spelling of one identity
  are two streams, and the application owns which it writes (§INV-033).
- **Refusal** `ErrKey`, **request** class (§2.3). The wiring class would report the
  commonest cause — a request that omitted an id — as an operator failure and turn
  ordinary bad input into a page (GAP-52). A blank or over-long identity arrives
  with a request, and [[D-040]]'s line is that the class decides the status.
- **Control** Three. A legal key of exactly the cap length in the same test loads
  normally, so the check cannot pass by refusing every key. A key holding a NUL
  byte is refused while the same key without it is accepted, so the text rule is a
  mechanism rather than a sentence. And **a zero `At[S]{}` appended is `ErrKey`**,
  with a token from a real `Load` appended in the same test and admitted — the
  case §INV-015 hands the suite and the one that fails if `Append` stops applying
  the kernel's rules.

#### UC-014 A stream contains a revision the declaration cannot read  [edge]
- **Actor** A3.
- **Precondition** The wire type is declared; its revision is above the current
  revision (a newer writer, mid-rollout) or below the earliest retained one.
- **Trigger** Load.
- **Flow** No reader exists for that revision.
- **Observed** A typed refusal distinct from the unknown-type one, naming the
  type and the revision, with the zero state and the zero token.
- **Must not happen** The framework must not fall back to the nearest revision,
  must not attempt the current reader on older bytes, and must not treat "above
  current" as "current".
- **Why** A rolling deploy puts a newer writer beside an older reader by
  construction; guessing here corrupts state silently on exactly the day a deploy
  is half done.
- **Refusal** `ErrRevision`, history class (§2.3).

#### UC-015 A payload is malformed  [edge]
- **Actor** A3.
- **Precondition** Bytes that that revision's codec cannot decode into the reader
  type.
- **Trigger** Load.
- **Flow** Decode fails.
- **Observed** A typed refusal, the zero state, and — critically — **no payload
  bytes and no fragment of them in the error text**. The codec's own message does
  not travel either.
- **Must not happen** No partial state. No echo of the payload. No SQL, no driver
  text, no stream key.
- **Why** [[D-044]] and [[D-047]] applied to this subsystem: a refusal names the
  classification and the declared identifiers, never the data.
- **Refusal** `ErrPayload`, history class (§2.3). **The same sentinel answers the
  same question at the design's other decode** — a `Change`'s frozen bytes its own
  codec cannot decode, `Fold`'s fourth cause (§UC-041). They are the same bytes,
  because `Fact.New` recorded the fact at the moment of decision (§UC-063); all
  that differs is how long ago. `eventtest.RoundTrip` catches the broken codec
  before either decode runs anywhere real (§UC-042).

#### UC-016 A payload decodes but the reader refuses it  [edge]
- **Actor** A3.
- **Precondition** An upcaster that cannot map an old value — a retired enum
  member, a field whose old value has no meaning under the new type.
- **Trigger** Load.
- **Flow** The upcaster returns a refusal.
- **Observed** A typed refusal that wraps the application's own error so the
  application can match it with `errors.Is`, with the zero state.
- **Must not happen** The framework must not swallow the upcaster's error, must
  not substitute a zero value, and must not continue folding. The upcaster must
  not be able to signal refusal by panicking as its normal channel — a panic is
  recovered and reported as `ErrUpcast` rather than taking the process down for a
  data problem, which is `jobs.invokeUpcaster`'s existing shape.
- **Why** This is the one place an application-supplied pure function is allowed
  to say "no", and it must be a value, not a panic, so the app-usecase can decide
  what a poisoned stream means to it.
- **Why the upcaster is recovered and the fold is not** An upcaster is the one
  callback whose subject is **data**, so a failure in it is a fact about a stream
  and belongs to the caller as a value. A fold cannot fail on data, so a fold that
  panics is a programmer error and is not recovered (§INV-017, §UC-067).
- **Refusal** `ErrUpcast`, history class (§2.3), wrapping the application's own
  error — one of the four declared class wraps. **A recovered panic is not an
  application error and wraps nothing**: `ErrUpcast` still names the type and the
  revision, `errors.Is` reaches the sentinel, and the panic value does not travel
  (§INV-025), which is `jobs.HandlerFailure`'s line exactly.

#### UC-017 A stored payload exceeds the byte cap  [edge]
- **Actor** A3.
- **Precondition** A stored payload larger than the store's declared cap — data
  written before the cap was lowered, or by another writer.
- **Trigger** Load.
- **Flow** The cap is checked before decode.
- **Observed** A typed refusal carrying the framework's existing too-large class,
  so a transport answers the status that class already maps to.
- **Must not happen** The bytes must not be decoded, buffered whole, or
  truncated and decoded anyway. There must be no spelling for "unbounded".
- **Why** [[D-063]]: "Do not use a limit reader without comparing the length
  afterwards. Silent truncation is worse than the unbounded read it replaces."
- **Refusal** `ErrTooLarge`, request class (§2.3), wrapping the framework's
  too-large class.

#### UC-018 A load runs under a cancelled or expiring context  [edge]
- **Actor** A3.
- **Precondition** A context already cancelled, or cancelled mid-page.
- **Trigger** Load.
- **Flow** The store observes cancellation and stops.
- **Observed** The cancellation error's identity is preserved exactly — a caller
  matching the standard cancellation sentinel still matches. The zero state.
- **Must not happen** Cancellation must not be converted into a conflict, an
  unknown-type refusal, an internal error or a partial state.
- **Why** [ES] capability obligation 7: "Error identity and cancellation are
  preserved. A wrapper does not convert a context cancellation, conflict or
  unknown commit into another class."
- **Note** A load writes nothing, so a cancellation here is only a cancellation.
  The append path is where the two classes compete (UC-057).

### Group D — Deciding and appending (A3, mode E3)

#### UC-019 An operation loads, decides and appends  [happy]
- **Actor** A3.
- **Precondition** A bound repository and an aggregate identity.
- **Trigger** An application command.
- **Flow** `Load` produces `(state, at)`. The application's own decision function
  is called **as a method on the state**, with the aggregate's identity and the
  command; it returns changes or a domain error. Each change is minted by a fact
  handle from that identity, so it carries the stream it was decided for
  (§UC-064). `Append` is a **repository** call taking the at-token and the
  changes, and it admits only changes whose stream is the token's (§INV-044).
- **Observed** Three values: an advanced at-token at version v+n, a receipt, and
  no error. The ordinary call discards the first two —
  `_, _, err := repo.Append(ctx, at, changes...)` — because most operations decide
  once and are done.
- **Must not happen** The append must not carry a version the caller typed. The
  framework must not re-read the store or decode its own written bytes to produce
  the advanced token — the version is `at.Version() + len(changes)` and nothing
  else. The decision function must never be called by the framework, and the
  framework must never call it twice. **The framework must not fold anything on
  the caller's behalf**: an advanced *state* is the caller's to compute, with one
  visible line, and §UC-021 is where that is argued.
- **Why** [ES]: "Every aggregate append names the exact observed stream version.
  A blind append is not a convenience overload." This design goes further: there
  is **no spelling** for an append that does not name the observed version,
  because the version is carried by a token only `Load` and `Append` can mint
  (§INV-015).
- **Why the receipt is not optional, and why there is no two-value spelling**
  `Append` returns three values and there is exactly one way to call it. A shorter
  overload would be two ways to do one thing, which `architecture.md` bans, and it
  would be the one everybody used — which is how §UC-030's atomicity claim would
  quietly become unavailable in the shape consumers copy. `_, _, err :=` costs
  four characters and is honest about what the call produces.

#### UC-020 An operation decides that nothing changed  [edge]
- **Actor** A3.
- **Precondition** A command that is a no-op against the current state — an
  idempotent re-application, a cancel of something already cancelled.
- **Trigger** The decision function returns zero changes and no error.
- **Flow** The append writes nothing and touches no store.
- **Observed** Success. The returned at-token is the one that went in, unchanged.
  The receipt answers **yes** to `Empty()`, reports the stream and the unchanged
  version, and carries the **invalid** authority. Asking an empty receipt for its
  authority is legal and never panics; that authority compares equal to nothing,
  including another invalid one, which is the honest answer — an append that wrote
  nothing is atomic with nothing.
- **Must not happen** An empty append must not fabricate a version bump, must not
  produce a receipt indistinguishable from a real one, and must not be reported
  as evidence that the caller's token was current — because it performed no
  version check. **A caller must not have to know it was empty before it may ask
  the receipt a question**: every accessor answers rather than panics.
- **Why** The alternative — refusing an empty append — forces the same
  `if len(changes) == 0` branch into every call site, which is precisely the
  boilerplate [[D-021]] exists to delete. The cost is that "empty" and "current"
  are different facts, so the receipt must say which. Recorded as an open
  question (Q5) because the opposite choice is defensible.
- **What an empty receipt does not carry** `Durability()`, because the per-commit
  durability class had no honest value here and is deleted outright (§D.12, Q8).

#### UC-021 Two decisions are taken in one operation  [happy]
- **Actor** A3.
- **Precondition** A successful append already happened in this operation.
- **Trigger** A second decision that must see the first one's facts.
- **Flow** The caller advances its own state with the aggregate's pure fold, and
  appends through the token the first append returned:

  ```go
  at, _, err = repo.Append(ctx, at, first...)
  state, err = accounts.Fold(cmd.Account, state, first...) // pure, the caller's memory
  second, err := state.Credit(cmd.Account, cmd.ID, cmd.Minor)
  _, _, err = repo.Append(ctx, at, second...)
  ```

  `at` and `err` already exist here — `at` is on the right-hand side of the first
  line, so it must — which is why the first and last lines assign with `=` and only
  the line introducing `second` uses `:=` (GAP-65). `Fold` takes the identity for
  the same reason `New` does: it is the fold's only way to refuse a change list
  decided for another instance (§INV-044, §UC-064). **`state` is the same variable
  on both sides on purpose** — `Fold` runs the caller's own folds over the
  caller's own value, so the argument is consumed and assigning back is the only
  correct spelling (§UC-041, §UC-068).
- **Observed** Success. The two appends are two separate atomic units unless a
  caller transaction spans them (UC-028). The state the second decision reads is
  the fold of the first append's changes, and it is the same function that folds a
  replay — there is one fold in the exported surface and it has one name.
- **Must not happen** The second append must not reuse the version of the first —
  it cannot, because the first append returned the advanced token and the stale
  one conflicts. The framework must not silently reload.
- **The one thing a caller can get wrong, named rather than hidden** Forgetting
  the `Fold` line — not folding the *wrong instance's* changes, which the identity
  parameter refuses (§INV-044). Both appends then succeed and the second decision
  was taken on a state missing the first fact. That is the cost of the framework
  retaining no state, and it is why `Append` returns the advanced **token**, so
  that at least the version half can never be forgotten, while the state half is
  one visible, pure, greppable line. Both alternatives are worse: a framework that
  folds for you holds your state, and one that hands back a state it folded has
  decided your state type may be folded twice.
- **Control** §UC-062 drives exactly this: two decisions in one operation where
  the second's correctness depends on the first's fact, asserting that the fold
  line is load-bearing by removing it and watching the assertion fail.

#### UC-022 Two writers append to one stream concurrently  [edge]
- **Actor** Two instances of A3, in two goroutines or two processes.
- **Precondition** Both loaded the same stream at version v.
- **Trigger** Both append.
- **Flow** The store admits exactly one at expected version v.
- **Observed** One caller gets a receipt; the other a typed conflict carrying the
  framework's existing conflict class, so a transport answers the status that
  class already maps to.
- **Must not happen** The loser's events must not be written, partially written,
  reordered behind the winner's, or written at a version the store then reuses.
  **No framework layer may retry the loser's proposed list.** The conflict must
  not carry the actual current version (see §INV-026). Both must not win. Under
  `-race`, no data race may be reported.
- **Why** [ES]: "A conflict requires a fresh load and a new domain decision. No
  framework layer retries a stale proposed event list", which is [[D-040]] applied
  to append.
- **Refusal** `ErrConflict`, write class (§2.3), wrapping the framework's conflict
  class.
- **Token** unchanged. The loser still holds a token at version v, which is now
  stale; every further append through it conflicts, because the store's real
  expected-version check says so and not because a kernel bit was flipped
  (§INV-040).
- **Control** The same test with a single writer must succeed, so a store that
  refuses every append fails the control rather than passing the conflict case.

#### UC-023 WITHDRAWN — the loser of a conflict tries to append again on the same view
> The case existed only because a `View` could be poisoned. There is no view.
> A stale at-token conflicts again, with `ErrConflict`, because the store's
> expected-version check refuses it — which is the same instruction to the caller
> with one fewer mechanism and no `ErrStaleView`. What the round-3 shape bought
> (the caller *cannot* loop) is replaced by §UC-034's **bounded procedure**, which
> says what the caller does instead rather than only what it may not do.
> Citations to UC-023 resolve here and should be read as §UC-022 plus §UC-034.

#### UC-024 A change carries a payload the codec cannot encode, or one over a cap  [edge]
- **Actor** A3.
- **Precondition** A payload value that is shape-legal (the declaration accepted
  its type) but value-illegal — a non-finite float, a custom marshaller that
  errors — or one that encodes to more bytes than a cap.
- **Trigger** `Fact.New`, then `Append` or `Fold`.
- **Flow** Encoding happens **at `New`**, at the moment of decision. Two bounds
  are checked in two places, and the split is not an accident: the **kernel's
  absolute payload ceiling** — a constant, the shape `jobs.MaxPayloadBytes`
  already has — is applied at `New`, because a store is not known there and an
  unbounded encode before any store exists would be a hole; the **store's**
  `Limits().MaxPayload` is applied at `Append`, because that is the first moment
  the store is known. The store's cap is never above the kernel ceiling.
- **Observed** `New` returns a `Change` carrying the failure rather than
  panicking or returning a second value — so a slice literal of changes stays
  writable. `Change.Err()` exposes it for a caller who wants to fail at decision
  time. `Append` and `Fold` both surface it, and `Append` surfaces it **before
  the store is reached**, so nothing is written. An illegal key from the identity
  mapper travels the same way (§UC-051).
- **Must not happen** Nothing may be written — not the earlier changes in the
  same append, not a partial batch. The store must not be reached at all. A change
  carrying a deferred failure must not fold as a no-op inside `Fold`: it surfaces,
  which is why `Aggregate.Fold` returns an error at all (§D.8).
- **Note** The residual is honest and stated: type-level encodability is checked
  at declaration against that revision's own codec (UC-056), value-level
  encodability cannot be checked before the value exists, so this is the one
  failure in the write path that surfaces at request time. It surfaces at `New`
  rather than at `Append` — where the decision was taken and where the caller
  still has the payload in hand.
- **Refusal** `ErrEncode` or `ErrTooLarge`, request class (§2.3), and they are
  distinct.
- **Token** unchanged. Nothing reached the store (§INV-040).

#### UC-025 An append batch exceeds the declared batch bound  [edge]
- **Actor** A3.
- **Precondition** A decision that emitted more changes than the store's declared
  maximum per append.
- **Trigger** Append.
- **Flow** The bound is checked before anything is sent.
- **Observed** A typed refusal naming the bound class, not the count of a
  caller's data.
- **Must not happen** No partial write. No silent chunking into several appends —
  it would break the one-transaction invariant and produce two versions where the
  caller expected one, quietly breaking [ES]'s "commit or roll back in one
  transaction".
- **Refusal** `ErrTooLarge`, request class (§2.3) — the same sentinel a payload
  over the byte cap takes, because both have one caller obligation and one
  transport class (§D.12's `ErrBatch` row).
- **Token** unchanged (§INV-040).

#### UC-026 A change built for another aggregate is appended  [edge]
- **Actor** A1/A3 by mistake.
- **Precondition** Two aggregate declarations over **two state types** (UC-002).
- **Trigger** Passing aggregate B's change to aggregate A's append.
- **Flow** It does not compile.
- **Observed** A compile error at the call site.
- **Must not happen** No `any`-typed change list and no registry keyed by string
  may accept it. The same holds for an at-token: an `At[Order]` is not assignable
  where an `At[Account]` is wanted.
- **The boundary of what a type can do** The type parameter separates **state
  types**, not aggregates, so this compile error is exactly that wide and no
  wider; §INV-020 states the scope and what refuses the rest.

#### UC-027 A caller tries to name an expected version  [edge]
- **Actor** A3.
- **Precondition** None.
- **Trigger** Any attempt to append at a version the caller chose — a literal,
  a value read from a request, a value from another aggregate.
- **Flow** There is no parameter to put it in and no constructor for the value
  that carries it.
- **Observed** A compile error, or no expressible program at all.
- **Must not happen** No exported constructor, no exported field and no
  deserialisation path may produce an at-token the store will honour. A token must
  be obtainable only from `Load` or from a successful `Append`.
- **Why** This is the phase's strongest single safety claim and it is the same
  shape as [[D-117]]'s minted-never-manufactured scope: a value that authorises
  a write exists only where the framework produced it.

#### UC-057 The context is cancelled during an append  [edge]
- **Actor** A3.
- **Precondition** An append in progress and a context that is cancelled or
  whose deadline expires.
- **Trigger** Cancellation, in one of two windows that the contract keeps apart.
- **Flow** **Before any write was issued**, the append refuses as a cancellation
  and nothing was written. **After the write was issued and before the commit is
  acknowledged**, the outcome is unknown, and it is reported as commit
  uncertainty (UC-034) even though the trigger was the caller's own cancellation.
- **Observed** In the first window the standard cancellation sentinel still
  matches and the token is unchanged. In the second the caller gets
  `ErrUncertain`, and the cancellation's identity is *not* what the caller matches
  — because what the caller must do differs.
- **The precedence rule, stated once** An uncertain outcome outranks a
  cancellation, because the two have opposite caller obligations and the asymmetry
  decides: reporting an uncertain commit as a cancellation loses the "it may have
  landed" fact and cannot be recovered, while reporting a cancellation as an
  uncertainty costs one reload.
- **Must not happen** A store must not report cancellation for a write it issued
  and could not confirm. It must not report uncertainty for a write it never
  issued — that would make every cancelled request look dangerous and would
  hide the real ones. No layer may guess which window it was in: the store knows,
  and nothing above it does. **A cancellation observed before the store was
  reached at all** — the kernel's own pre-store checks, or a context already
  cancelled on entry — is always the first window, never uncertainty (§UC-060's
  scoping rule).
- **How a store says which window it was in** In the first it returns the bare
  `ctx.Err()`, not wrapped in an `event.Failure`, and the kernel lets it travel as
  itself — the one exception to the append door's fail-safe default and what makes
  §INV-029 true. In the second it returns
  `event.Failure(event.Unconfirmed, ctx.Err())` and the kernel answers
  `ErrUncertain` (§D.14, §INV-045). One value apart, which is what stops two
  stores spelling them differently.
- **Refusal** `context.Canceled`/`context.DeadlineExceeded` in the first window,
  `ErrUncertain` (write class) in the second.
- **Why** [ES] requires cancellation to survive as cancellation and unknown
  commit to survive as unknown commit. Both survive only if the contract says
  which one this event *is*; without the rule, two stores answer differently and
  the conformance suite passes both.
- **Control** The uncancelled append in the same test succeeds, and the
  cancelled-before-write case asserts zero events, so a store that answers
  `ErrUncertain` to everything fails a control.

#### UC-058 One append carries two identical changes  [happy]
- **Actor** A3.
- **Precondition** A decision that legitimately emits the same fact twice — two
  credits of the same amount, two identical line additions.
- **Trigger** Append with both.
- **Flow** Both are encoded at `New` and written; they take consecutive versions.
- **Observed** Two events, two versions, one atomic unit. This is legal and the
  framework must not deduplicate.
- **Must not happen** No layer may collapse equal payloads, warn about them, or
  require the application to distinguish them. Equal facts are a normal history.
- **Why** It has to be said out loud, because it is what makes the *accidental*
  double append (§D.10) undetectable: if two identical changes are legal in one
  batch, two in two batches cannot be a framework error either. That distinction
  is the domain's, and the command's identity belongs in the payload (UC-034).

#### UC-060 The store's own backend fails an append  [edge]
- **Actor** A3, against any real store.
- **Precondition** A failure that is neither a conflict nor a cancellation nor a
  close: a lost connection, a serialisation failure, a deadlock victim, a lock
  timeout, a full disk, a constraint the store's own schema imposes.
- **Trigger** Append — and this is the outcome a real store produces most often
  after a conflict, so leaving it unnamed leaves the common case unspecified.
- **Flow** The store classifies its own failure into exactly one of two answers,
  because only the store can: **certainly not written**, or **unknown**. There is
  no third answer. **It says which in the kernel's own words** —
  `event.Failure(event.NotWritten, cause)` or `event.Failure(event.Unconfirmed,
  cause)` — which is the whole of the classification channel and is written out in
  §D.14. A store adds no sentinel and invents no wrapper of its own; it selects a
  row of §2.3's table and the kernel maps the selection (§INV-045). Unknown is
  commit uncertainty (UC-034) and nothing else; certainly-not-written reaches the
  caller as a backend refusal wrapping the store's own error, carrying the
  framework's retryable class when — and only when — the store classified the
  failure retryable, which is [[D-040]]'s taxonomy: a serialisation failure, a
  deadlock and a lock timeout are retryable and are never a client error.
- **The fail-safe default, and its scope** The kernel's default is: *a failure
  returned from the append attempt itself that does not say "certainly not
  written" is `ErrUncertain`.* It is a reading of a value rather than an
  inference: an error that is not an `event.Failure`, or one whose outcome is
  `Unclassified`, takes the default. It is right because guessing the safe way
  costs one reload and guessing the other way is a silent double write. **Its
  scope is exactly the append attempt**, and it must not swallow:
  - the kernel's own pre-store refusals — key, encode, too-large, batch, backing
    mismatch, **stream mismatch** (§UC-064), transaction refusals — which never
    reached the store;
  - a **closed** store, which refused rather than tried (`ErrClosed`);
  - a cancellation observed **before any statement**, including a context already
    cancelled on entry (§UC-057's first window);
  - a failure from `Transaction(ctx)`, which is the only other method that can
    return one and which issues no statement (§D.14's four doors).

  Without that scoping, every ordinary store error tells the caller to reload and
  re-decide, which is the failure mode that makes an uncertainty signal worthless:
  a signal that fires on everything is read as firing on nothing.
- **Observed** A refusal the caller can tell apart from a conflict (the version
  was fine), from an uncertainty (this one did not write) and from a closed store
  (that one refused rather than tried). Retryable-ness is a property of the
  wrapped class, so a caller and a transport branch on it as they do everywhere.
- **Must not happen** A store must not return a failure it has not classified —
  that is the third answer, and it forces every caller to guess between "retry
  freely" and "reload before doing anything". It must not report a
  certainly-not-written failure as uncertainty, and must not report an unconfirmed
  write as a plain failure, which is the silent double write (§INV-025).
- **Refusal** `ErrBackend`, store class (§2.3), carrying the store's own error as
  its **cause** so `errors.Is` reaches it while its text does not travel, and
  class-wrapping the framework's retryable class exactly when that cause is
  retryable.
- **Token** unchanged — the write certainly did not land, so the token's version
  is still the store's, and the caller may decide again and append through the
  same token. The uncertain case is UC-034's and is a different row (§INV-040).
- **Why** Phase 2's PostgreSQL append produces serialisation failures routinely
  and `errs/sqlerr` already classifies them. With no sentinel and no wrapping rule
  a caller cannot tell a retryable backend failure from a conflict from an
  uncertainty — three failures with three different obligations.
- **Control** The same append with no injected failure succeeds, and the injected
  *uncertain* failure in the same test produces `ErrUncertain`, so a store that
  answers `ErrBackend` to everything fails the control.

### Group E — The caller's transaction (A3, mode E4)

This group follows [[D-118]]: a durable write made while the caller's transaction
is bound to the store's own data source is written **inside** it, and only an
ambient executor that is *not* a transaction is refused — never autocommit. Two
subsystems answering one question two ways is itself the defect, and §INV-041 is
where that is argued. What `Within` is for under that rule is §UC-028's.

#### UC-028 A caller's transaction spans a load and an append  [happy]
- **Actor** A3.
- **Precondition** A store whose `Capabilities().Transactions` is supported, and
  a transaction the caller opened and owns.
- **Trigger** The caller binds its transaction into the context the way that
  store documents — `crud.WithExecutorFor(ctx, source, tx)` for a SQL store,
  `eventmemory.WithTransaction(ctx, tx)` for the memory store — then calls
  `Within` on the repository, then loads, decides and appends through the context
  `Within` returned.
- **Flow** `Within` asks the store one question, `Transaction(ctx)`, which issues
  no statement and touches no backend. The store answers the **authority** of the
  transaction it found for its own backing. `Within` returns a context carrying a
  kernel-minted **marker** for that authority, chained on any marker already
  there. Every later `Load` and `Append` on that context asks the same question
  and compares the answer with the marker.
- **Which marker, and the rule is one clause** A marker carries an authority, an
  authority carries its backing, and an operation compares against **the innermost
  marker whose backing is its own store's**. A context with no marker for this
  store's backing is unmarked *for this store*, and §INV-041's three rows apply
  unchanged. Resolved innermost-first instead, an operation calling `Within` over
  store A and then over store B — two backings, one context, which §UC-007 and
  §UC-055 both make ordinary — leaves B's marker innermost; A's next `Append`
  compares A's authority against B's marker, `Authority.Same` begins with
  `Backing.Equal`, the backings differ, and a correct program is refused with a
  message that by §INV-025 names neither transaction and is undiagnosable
  (GAP-73). Chaining rather than replacing is what lets two stores' transaction
  contexts compose; keying by backing is what makes the chain resolvable.
- **Observed** The events are in the caller's transaction. They become visible
  when it commits and are absent when it rolls back (§UC-029). The receipt
  carries the authority, and two appends in one transaction carry authorities
  that compare `Same` (§UC-030).
- **What `Within` is for, since it is not what admits the write** Three things,
  and they are why it exists at all when an ambient transaction is joined without
  it (§UC-046):
  1. **It fails early.** An operation that must be atomic says so at its top,
     before the first statement, and learns *there* that there is no transaction
     (`ErrNoTransaction`, §UC-032) or that this store has none
     (`ErrNoTransactionBinding`, §UC-033) rather than after three writes.
  2. **It mints the marker**, the only thing that makes §UC-049's mismatch check
     possible: without one, a context that later acquires a *different*
     transaction is indistinguishable from one that always had it.
  3. **It is the authority's first observation**, so the caller can hold it and
     compare it against another subsystem's before any append has happened.
- **Must not happen** `Within` must not return an inner store, a bound
  repository, or anything else the caller then calls methods on. It returns a
  **context**, so a decorator that policed the store before `Within` still
  polices every operation inside the transaction — the tunnel [[D-115]] closed
  must not be reopened here (§UC-048, §INV-030). The framework must not open,
  commit or roll back a transaction of its own at any point (§INV-027). It must
  not commit the caller's. It must not require `Within` in order to be
  transactional — that is §UC-046 and [[D-118]].
- **Why the marker is a context value and not a field on a repository** A bound
  repository is a second object with the same methods, and the one thing it
  cannot check is which context it is called with: `bound.Append(otherTxCtx, …)`
  writes into the first transaction while the caller believes the second, with no
  refusal available. A marker in the context cannot be separated from the context
  it describes, so that program is not expressible.
- **Refusal** none on the happy path; the three wiring refusals are §UC-032,
  §UC-033 and §UC-049.
- **Control** The same operation with no transaction at all succeeds on
  autocommit (§UC-019), so a store that refuses every append fails the control
  rather than passing this case. And **two `Within` contexts chained for two
  different backings**, with both stores' operations asserted to proceed — the case
  an innermost-marker rule refuses (GAP-73).

#### UC-046 The context carries a transaction and the caller never called `Within`  [edge]
- **Actor** A3, by omission.
- **Precondition** The caller opened a transaction and bound it into the context
  for this store's backing. The repository was never asked for a `Within`
  context — one forgotten line, and the shape a helper called from two places
  reaches by accident.
- **Trigger** `Load` or `Append` on the transaction-carrying context.
- **Flow** The operation asks `store.Transaction(ctx)` as it always does. The
  store answers a **valid authority**, and the context carries no marker.
- **Observed** The write is made **inside the caller's transaction**, and the
  receipt carries that authority. This is [[D-118]]'s answer, the same one
  `jobspg.Driver.Place` gives: "a durable invocation placed while the caller's
  transaction is bound to the driver's `crud.Source` is written **inside** that
  transaction". Nothing escapes and nothing is refused.
- **The second half, and it is a refusal** The context carries, for this store's
  backing, an executor that is **not** a transaction — or one this store cannot
  write an append through. The store answers with its own error and the kernel
  refuses **before any statement**, never falling back to autocommit. That is the
  other half of [[D-118]] verbatim: "an ambient executor that is *not* a
  transaction … is `RejectPlacement(ErrUnsupported)`. It is never placed on
  autocommit instead."
- **Why refusing instead was deleted rather than narrowed** §INV-041 carries the
  argument and [[D-118]]'s standing.
- **Must not happen** An append must never run on autocommit while an executor
  for this store's backing is bound in the context. No layer may guess whether an
  ambient executor is a transaction — only the store knows what its own executors
  are. The refusal must arrive before any statement.
- **The mirror case, which this use case does not cover** A transaction open and
  **not bound** for this store's backing — a forgotten binding line, or a store
  with no transactions at all. Nothing of this store's is in the context, so it
  answers "nothing" and §INV-041's first row applies: autocommit. That is §UC-066,
  the one route no mechanism can see.
- **Refusal** `ErrAmbientNotTransaction`, wiring class (§2.3), carrying the
  store's own answer as its **cause** so a caller can reach the reason — a cause
  and not a fifth class wrap (§2.3).
- **Token** unchanged. Nothing reached the backing (§INV-040).
- **Control** The same context carrying a real transaction of this store's
  succeeds and lands inside it, so a store that refuses every ambient context
  fails the control.

#### UC-049 A marked context meets a different transaction  [edge]
- **Actor** A3, by mistake.
- **Precondition** A context returned by `Within` for transaction T1, into which
  a second transaction T2 for the same backing has since been bound — the shape a
  middleware that re-binds a request-scoped executor produces, and the shape a
  helper that takes `ctx` and "makes sure there is a transaction" produces.
- **Trigger** `Load` or `Append` on that context.
- **Flow** `store.Transaction(ctx)` answers T2's authority. The innermost marker
  **for this store's backing** names T1 (§UC-028's lookup rule). They are not
  `Same`.
- **Observed** A refusal before any statement, naming neither transaction and
  nothing about either (§INV-025). The caller's atomicity claim was about T1 and
  the write would have landed in T2; the framework cannot know which one the
  caller meant, and both possible guesses are wrong for somebody.
- **Must not happen** The write must not land in either transaction. The
  framework must not prefer the marker, must not prefer the ambient answer, and
  must not "re-mark" the context silently. A marker must not be forgeable: it is
  minted only by `Within`, from an authority the store answered.
- **The savepoint case, decided rather than left to an implementer** A savepoint
  is not a second transaction, and nothing has to remember that: `crudsql`'s
  savepoint returns the parent `*sql.Tx`, and an authority **is** its
  transaction's identity held by reference (§INV-028), so the two calls resolve to
  one pointer and `Same` holds by construction. It is also right for what an
  authority claims — *which durable commit will carry this write*: a savepoint has
  no commit of its own, so a claim made inside one is a claim about the parent.
  What the design deliberately does **not** offer is "these two writes survive
  together even if this savepoint rolls back"; no phase-1 API spells it.
- **Where it is asserted, since the conformance suite cannot make a savepoint**
  No hook and no method on `eventtest.Tx` can open one — it is `crudsql`
  vocabulary and the memory store has none — and adding one would be a factory
  hook whose requiredness is derivable from no capability, which §D.9's own rule
  forbids (GAP-71, §D.12). So the `transactions` section asserts **two live
  transactions answer authorities that are not `Same`**, and the savepoint claim
  is asserted where it is a claim (§INV-028, §9).
- **Refusal** `ErrTransactionMismatch`, wiring class (§2.3).
- **Control** The same operation on the context `Within` returned, with no second
  transaction bound, succeeds — so a store that refuses every marked context
  fails the control.

#### UC-029 The caller's transaction rolls back  [edge]
- **Actor** A3.
- **Precondition** A successful append inside a caller transaction (§UC-028 or
  §UC-046).
- **Trigger** The caller rolls back — deliberately, or because a later step in
  the same operation failed.
- **Flow** The store's staged work goes with the transaction, because it was
  never anything else.
- **Observed** The events are absent from both reads. The stream's version is
  what it was before. **The positions the rolled-back append would have taken are
  burned**: a later append takes a higher position and the gap stays
  (§INV-009) — a store that reissued them would let a consumer that had already
  seen position p see a different event at p.
- **Must not happen** No event, no version bump and no position may survive a
  rollback. A store must not make a rolled-back event visible to either read,
  transiently or permanently. The framework must not compensate, must not
  re-append, and must not notice the rollback at all — it is the caller's.
- **Why the at-token is not poisoned by a rollback** The token names a version
  the stream no longer has, so the next append through it conflicts — correct and
  self-correcting (§UC-022). The framework cannot observe a rollback it did not
  perform, so a rule depending on noticing one is a rule none could keep.
- **Control** The same append followed by a commit makes the events visible, so a
  store that hides every append passes nothing.

#### UC-030 Two subsystems write in one transaction, and the claim is checkable  [happy]
- **Actor** A3, composing `event` with a second durable subsystem — an auditor, a
  `jobs` placement, the application's own rows.
- **Precondition** One transaction, bound in the context for one data source, and
  both subsystems constructed over that same data source.
- **Trigger** The operation appends events and performs the other subsystem's
  write on the same context.
- **Flow** Neither subsystem is asked to prove anything about the other. Each
  **refuses to run outside the transaction it was given**: `event` by
  §UC-046's second half, `jobs` by [[D-118]]'s `RejectPlacement`. Both writes
  therefore ran in the one transaction the context carried, or one of them
  refused before writing anything.
- **The precondition the argument needs, and what enforces it** "The context
  carried a transaction" is not self-evident: a transaction the caller opened and
  never bound is invisible to both subsystems, both write on autocommit, and this
  guarantee is false with nothing refusing (§UC-066). So the claim is "by
  construction **given the precondition**", and `Within` is what makes a missing
  precondition loud — which is why §D.6's sketch calls it.
- **Observed** Atomicity, established by construction rather than by comparing
  two tokens. `commit.Authority()` and `repo.Authority(ctx)` answer the same
  value, and two appends in one transaction answer authorities that compare
  `Same` — which is what makes the property testable *within* this subsystem.
- **Must not happen** The authority must not be a data-source pointer, a store
  pointer or anything derivable from either — two stores over one database in one
  transaction and in two different transactions must be told apart (§INV-028). It
  must not be serialisable: `MarshalJSON` refuses, which is
  `jobs.TransactionBinding`'s existing shape. No subsystem may compare its
  authority with another subsystem's; the types are not comparable and phase 1
  offers no bridge (Q17).
- **Why the equality is within-subsystem only** A comparable cross-subsystem
  identity would have to be minted somewhere neither subsystem owns: a change to
  `jobs` **and** a new dependency edge, neither of them phase 1's (Q17).
- **Control** Two appends in **two** transactions must answer authorities that
  are **not** `Same`, so a store that answers a constant authority fails the
  control instead of passing this case.

#### UC-031 An at-token minted over one backing is appended through a repository over another  [edge]
- **Actor** A3, in a process holding two stores (§UC-007).
- **Precondition** Two stores over two different backings, and one aggregate
  declaration bound to both.
- **Trigger** `repoB.Append(ctx, atFromA, changes...)`.
- **Flow** The token carries the backing it was minted over. The repository
  compares it with its own store's `Backing()` using `Equal`, never `==`, before
  any statement.
- **Observed** A refusal naming a scope failure and nothing about either store,
  either backing or the stream (§INV-025). Nothing is written.
- **Why the check has two parties at all** Because the append is on the
  **repository**: the token and the repository are two independently obtained
  values, so the mismatch is one line of ordinary code away. On a `view.Append`
  there was one party and the comparison was vacuous (GAP-45).
- **The same refusal at `Bind` and at `Read`** Both doors run the same three
  store-honesty checks and refuse with this sentinel; §INV-022 argues the two
  doors.
- **Must not happen** The comparison must not be pointer equality of the store
  value, must not be pointer equality of a value a decorator may have replaced,
  and must not depend on which store minted the token. Two invalid backings must
  **not** compare equal, so a store that forgot to answer one cannot accidentally
  match another that also forgot. Two store values over **one** backing must be
  accepted — that is §UC-054 and it is this case's control.
- **Refusal** `ErrWrongStore`, wiring class (§2.3).
- **Control** §UC-054's same-backing append must succeed, so an implementation
  that compares store instances fails the control rather than passing this case.

#### UC-032 `Within` is called with no transaction in the context  [edge]
- **Actor** A3.
- **Precondition** A transaction-capable store and a plain context — the caller
  forgot to open or to bind a transaction.
- **Trigger** `Within(ctx)`.
- **Flow** `store.Transaction(ctx)` answers the invalid authority with no error:
  nothing of this store's is bound here.
- **Observed** A refusal at `Within`, before the operation's first statement.
  The caller asked to be inside a transaction and there is none, and the whole
  point of `Within` is to say so here rather than after three writes.
- **Must not happen** `Within` must not open a transaction of its own — the
  framework would own one the caller must commit, with nowhere to return it
  (§INV-027). It must not silently degrade to autocommit: an operation that asked
  to be atomic and was not is the failure this refusal exists for, and it differs
  from §UC-046 exactly because §UC-046's caller never asked.
- **Refusal** `ErrNoTransaction`, wiring class (§2.3).
- **Control** The same call on a transaction-carrying context returns a marked
  context and no error.

#### UC-033 `Within` is called on a store that has no transactions  [edge]
- **Actor** A3, against a store whose `Capabilities().Transactions` is
  unsupported.
- **Precondition** Such a store exists by construction: a future read-through
  store, a store over a backing with no transaction concept.
- **Trigger** `Within(ctx)`.
- **Flow** The capability is read from the **exact outer store value**. There is
  no unwrap chain, no optional interface and nothing to discover: `Transaction`
  is a required method of `Store` and `Capabilities()` is a required method of
  `Log` (§INV-030).
- **Observed** A refusal distinct from §UC-032's: *this store never has
  transactions* is a deployment fact and *this context has none* is a wiring
  mistake, and the two are fixed in different places.
- **Why this is a required method and not an optional interface** An optional
  interface can be *missing*, which makes the escape and mismatch checks absent
  for a decorated store (§D.12, §INV-030, GAP-47).
- **Must not happen** The kernel must not walk a wrapper chain to find a
  transaction seam. It must not treat a missing capability as an unsupported
  request at append time — the refusal belongs at `Within`, before work. A store
  must not declare the capability and then refuse every `Transaction`; that is
  §UC-043's dishonest-capability failure and the suite fails it.
- **Refusal** `ErrNoTransactionBinding`, wiring class (§2.3).
- **Control** The memory store declares the capability and the same call
  succeeds, so the refusal is a property of the capability rather than of the
  call.

#### UC-034 The commit outcome is unknown  [edge]
- **Actor** A3.
- **Precondition** A connection or a process lost around commit; the append was
  issued and its outcome was never confirmed.
- **Trigger** Append.
- **Flow** The store classifies its own failure as **unknown** — the second of
  the two answers §UC-060 allows it, and the only one nothing above it can infer.
- **Observed** A refusal the caller can tell apart from a conflict and from a
  certainly-not-written backend failure, because the obligation differs. No layer
  guesses. The receipt is not produced.
- **The bounded recovery procedure, and it is bounded** Left unsaid, the obvious
  reading is `for { repo.Append(ctx, at, changes...) }`, which compiles into a
  livelock that can never write once the token is stale (§D.10). The procedure is
  three steps and there is no fourth:
  1. **One retry, same token, same changes.** Exactly one. It is safe *because*
     the token names the expected version rather than a blind append: the store
     admits it only if the first attempt did not land.
  2. **Its two outcomes are the answer.** Success means the first attempt had not
     landed, and the caller is done. `ErrConflict` means it had landed — or
     somebody else wrote — and either way this caller's work is finished or
     superseded; the caller loads again and decides again. Any other outcome,
     **including a second `ErrUncertain`**, ends the procedure and reaches the
     caller as `ErrUncertain`.
  3. **A conflict is never retried with the same changes.** The state the
     decision was taken on is stale, so the decision must be taken again: load,
     decide, append (§UC-022, [ES]'s non-negotiable "a conflict requires a fresh
     load and a new domain decision").
- **What the framework does with that procedure** None of it. There is no retry
  inside the library — [ES] forbids it and [[D-040]] is why. It is written here
  because a contract that names an uncertainty and does not say what to do with it
  produces the livelock above.
- **Must not happen** No layer may convert an unknown commit into a conflict, a
  cancellation, a plain backend failure or a success. No layer may retry on the
  caller's behalf. The caller must not treat an uncertainty as a failure and
  re-decide from a stale state without reloading.
- **Refusal** `ErrUncertain`, write class (§2.3), wrapping nothing — there is no
  honest framework class for "maybe", and inventing one would give a transport a
  status it cannot justify.
- **Token** unchanged in its *value* and untrustworthy in its *meaning*: the
  stream may be at the token's version or at version + n, and only the store
  knows. That is why step 1 exists and why step 2 stops (§INV-040).
- **Control** The same append with no injected failure succeeds, and an injected
  *certainly-not-written* failure produces `ErrBackend` (§UC-060), so a store
  that answers `ErrUncertain` to everything fails a control.

#### UC-035 The same command arrives twice, and there is no idempotency key  [edge]
- **Actor** A3, behind an at-least-once transport.
- **Precondition** A command delivered twice — a retried HTTP request, a redriven
  job, a user double-click.
- **Trigger** Two operations that load, decide and append the same fact.
- **Flow** Both load, both decide, both append. If they interleave, one conflicts
  and re-decides (§UC-022). If they do not, both succeed and the history contains
  the fact twice.
- **Observed** Two events. **This is correct**, and the framework must not
  prevent it: §UC-058 establishes that two identical facts in one batch are
  legal, so they cannot be a framework error in two. The distinction between a
  legitimate repeat and an accidental one is the domain's, and the domain has the
  mechanism: **the command's identity belongs in the payload**, where the
  aggregate's own fold can refuse the second application. The sketch's
  `Applied map[string]bool` is that mechanism and it costs no framework feature.
- **Why phase 1 ships no idempotency parameter** An append idempotency key is a
  second identity for a fact, checked by the store and never folded — so the store
  and the aggregate would disagree about whether the command had been applied, and
  only the store's answer would be invisible in the history (Q16).
- **Must not happen** No framework layer may deduplicate appends. No parameter,
  option or flag for it exists in phase 1, so nothing has to be un-shipped if the
  answer is no.

### Group F — The bounded read (A4, mode E5)

#### UC-036 A consumer reads the log in bounded pages  [happy]
- **Actor** A4.
- **Precondition** A store with events in it, and a consumer that holds a
  **read-only** `Log` — not a `Store`.
- **Trigger** The consumer opens a reader and drains it.
- **Flow** `Read` is the second of §INV-022's two doors, so it runs the same three
  store-honesty checks `Bind` does and returns an error rather than a reader if
  any of them fails. Then the reader asks the store for a page after the cursor it
  holds. The store returns at most `Limits().MaxRead` envelopes in ascending
  global position and a fresh cursor. The reader hands the page to the consumer
  and keeps the cursor. There is no page-size parameter anywhere: the kernel is
  the only party that fills a read (§D.7).
- **Why `Read` validates and not only `Bind`** Because **a consumer-only
  deployment never binds anything**, so this is the only door it takes. §INV-022
  carries the argument and the `MaxRead: 0` store that motivates it.
- **What a consumer holds afterwards** A `*Reader`, the one stateful handle in the
  caller-facing surface: it keeps the page it fetched and the cursor it reached,
  for one goroutine at a time (§INV-038). Everything else handed to a caller is
  inert — including the **page**, which is the consumer's from the moment it is
  returned and survives the next `Next` (§INV-021's fourth and seventh hand-offs).
- **Observed** Every committed event, once, in ascending position, across as many
  pages as it takes. Memory in use is one page.
- **Must not happen** The consumer must not be handed a value it can append
  through, bind a transaction on, or close — least privilege on the seam every
  later projector and outbox relay inherits, and a projector that can append is
  how a replay writes. The read must not be unbounded and must have no spelling
  for unbounded. A page must not be returned that the store cannot resume from.
- **Why the caller supplies no limit** A caller-named limit had a zero value
  meaning both "the store's default" and "no limit", on the one path with no
  kernel in it; §D.7 and §D.12 carry it (GAP-51).
- **Refusal** `ErrWrongStore`, wiring class (§2.3), when `Read` refuses the log —
  the same sentinel and class as `Bind`'s three store-honesty refusals, because
  they mean the same thing to a caller: *this store cannot host this work*.
- **Control** Two. A store with fewer events than one page returns them all in one
  page and then an empty one, so a reader that always asks again terminates. **And
  a store answering `MaxRead: 0` is refused by `Read`** — without that case the
  first control passes against it, since "returns nothing and terminates" is
  exactly what a zero bound produces.

#### UC-037 A store does not promise monotone visibility  [edge]
- **Actor** A4, against a store whose positions can become visible out of order —
  which is what an ordinary SQL sequence produces, because a writer that took
  position 5 can still be uncommitted when the writer that took 7 commits.
- **Precondition** `Capabilities().MonotoneVisibility` is unsupported.
- **Trigger** A read, then a resumption from the cursor it returned.
- **Flow** The store's cursor is **not** its newest position. It is a point below
  which no writer can still commit — a watermark it computes for itself. The
  kernel neither computes nor interprets it.
- **Observed** Resumption skips nothing. A freshly committed event at a position
  below the watermark cannot exist, so the consumer sees every event, once, per
  pass, at the cost of seeing it slightly later.
- **What the capability actually claims, since it is not safety** Freshness, and
  nothing else: a store that declares it supported may return a cursor at its
  newest position, so a consumer sees an event sooner. One that declares it
  unsupported is not less correct and passes the same mandatory `resumption`
  section (§INV-035). Promising exact tiling unconditionally while admitting
  stores that cannot keep it is GAP-3; safe resumption is therefore **mandatory
  for every store** and this capability is a freshness claim.
- **Must not happen** A consumer must never derive a cursor from a position, and
  there must be no arithmetic, ordering or comparison on a cursor that this
  document defines. A store must not return a cursor above its own watermark. The
  kernel must not read the cursor's bytes.
- **Control** The `resumption` conformance section drives a writer that commits
  out of position order against *both* kinds of store, and both must lose nothing;
  a store that returns its newest position as a cursor while a lower one can
  still commit is one of the six defects the suite injects into itself (§UC-045).

#### UC-038 Per-stream order is a subsequence of global order  [happy]
- **Actor** A4, cross-checking against A3's loads.
- **Precondition** Several streams written interleaved.
- **Trigger** A full global read and a per-stream load of each.
- **Flow** Both reads are projections of one committed set.
- **Observed** For any one stream, the events in the global read appear in
  ascending stream version, and their positions ascend with them. Events of
  different streams may be interleaved arbitrarily.
- **Must not happen** A store must not reorder one stream's events in the global
  read, and must not assign a lower position to a later version of one stream.
  The recorded instant must not be used as an ordering key by anything, ever
  (§INV-009): two events at positions p and p+1 may carry instants in either
  order.
- **Note** A statement about **order**, not **membership**. Membership is
  §INV-034's conservation property, a separate obligation with its own section,
  because a store can keep either without the other: an event visible to the
  stream read and not the global read is ordered perfectly and invisible to every
  downstream consumer forever.

#### UC-039 Repeated bounded reads tile a quiescent log  [happy]
- **Actor** A4.
- **Precondition** A store nobody is writing to.
- **Trigger** Reading from the beginning to the end, one page at a time.
- **Flow** Each page resumes from the previous page's cursor.
- **Observed** The concatenation of the pages is every committed event, in
  ascending position, with none skipped and none repeated, for one uninterrupted
  pass over a store that is not being written to.
- **Must not happen** No event may be skipped at a page boundary. No event may
  appear in two pages of one pass. **The word "exactly once" must not be used
  here or anywhere else in this document to describe delivery**: this is a
  statement about one pass over a quiescent store, and reading it as a delivery
  guarantee is how an exactly-once claim enters a system that cannot make one
  ([ES]'s ninth non-negotiable, §INV-036). Delivery is at least once and
  consumers are idempotent.
- **Control** The same walk at a store whose `MaxRead` is 1 must produce the same
  sequence, so a boundary defect is visible.

#### UC-053 A cursor is persisted, resumed in a new process, and refused when it is foreign  [edge]
- **Actor** A4, across a restart.
- **Precondition** A cursor obtained from a previous read and written to a
  checkpoint — a column, a file, a JSON document.
- **Trigger** A new process reads from it, through a **different store value over
  the same backing**, which is what a restart is.
- **Flow** The store parses its own cursor, checks that it was minted over
  **this backing**, and resumes.
- **Observed** The read continues where it left off and skips nothing.
- **Must not happen** A store must not put a per-instance nonce in a cursor: the
  store *value* that minted it does not exist in the new process, and two values
  over one backing are one store for every purpose in this document (§UC-054), so
  "a cursor this store minted" means **this backing**, never this value. A cursor
  must not be interpretable, comparable or derivable by a caller.
- **When it is refused** Three causes, one sentinel: a cursor minted over another
  backing, a cursor the store cannot parse, and a cursor in a format the store no
  longer accepts. Each is a wiring or data mistake the consumer can act on and
  none is a store failure.
- **Why the type is a defined string, and what that costs** So that a store in
  another package mints one by conversion, which keeps §INV-019 true for the
  cursor (GAP-44). The cost, stated rather than denied: `<` compiles on two
  cursors. No ordering is defined, the kernel never compares two, and a hand-built
  cursor is refused by the store that did not mint it. Q18 is for anyone who wants
  the struct and the minting API it costs.
- **Refusal** `ErrCursor`, store class (§2.3).
- **Control** The happy half above is the control: a cursor from **this** backing,
  in a fresh store value, must resume — so a store that refuses every cursor
  fails it rather than passing this case.

### Group G — Lifetime and decorators (A2, A7)

#### UC-047 The composition root closes the store, and everything after  [edge]
- **Actor** A2, then A3 by mistake.
- **Precondition** A constructed store, possibly with a caller transaction open
  and staged work in it.
- **Trigger** `Close()`, then a second `Close()`, then a load or an append.
- **Flow** Close releases what the store opened and nothing else.
- **Observed** Close is **idempotent** and **refuses nothing**, including when a
  transaction is open: it returns nil the first time and every later time. It
  **neither commits nor rolls back anything**, and after it the caller's
  transaction alone decides what happened. Every later operation refuses.
- **What "staged work" is, and it is two different things** A store that owns its
  own staging — `eventmemory`, whose transaction is a value it minted — discards
  it on close. A store that stages inside the caller's transaction — any SQL store
  under §INV-041's join row — holds none of it: those rows are in a `*sql.Tx` the
  store did not open and must not close (§UC-055, §D.14). "Close discards staged
  work" is therefore true of one and false of the other, on a section the suite
  runs `always` (GAP-69). The property true of **both** is: *a close makes no
  staged work visible and commits none*.
- **The ordering obligation, stated because it is the caller's** Closing the store
  **before** ending the transaction makes a later commit's outcome
  store-dependent, and this contract does not define it: under the first shape
  there is nothing left to commit, under the second the rows land. End the
  transaction first — which Go's ordinary shape already does, `defer
  store.Close()` written before `defer rollback(tx)` running *after* it, so a
  caller who thinks about it gets the answer it would have got anyway.
- **Must not happen** Close must not commit staged work, and must not roll any
  back either: neither is its transaction. It must not close a
  datasource the store did not open (§UC-055). It must not refuse a second call,
  because `defer store.Close()` beside an explicit shutdown close is the ordinary
  shape and two calls answering differently is precisely the caller-visible
  difference the conformance suite exists to eliminate. An operation after close
  must not panic, must not hang, and must not be reported as a backend failure —
  a closed store **refused rather than tried**, so it is never commit uncertainty
  (§UC-060's scoping rule).
- **What an at-token held across a close is** Inert data: a stream, a version and
  a backing. It survives the close because it holds nothing of the store, and is
  usable again through a repository over another store value on the same backing
  (§UC-054) — which makes §UC-055's per-request store ordinary.
- **Refusal** `ErrClosed`, store class (§2.3), which the store spells
  `event.Failure(event.Closed, …)` — only the store knows it is closed, and the
  kernel maps that classification like any other (§D.14, §INV-045).
- **Control** Three, and they keep the assertion from being vacuous either way.
  The same operations **before** the close succeed. The second close returns nil —
  which alone passes against an implementation that refuses the first call, so it
  never travels alone. And the staged work is **invisible to another reader**
  immediately after the close, paired with the control that the same work
  **committed before** the close *is* visible after it, because "invisible" proves
  nothing against a store that hides everything. The `transactions` section's
  rollback case is a different assertion on a different trigger: the transaction
  is resolved and the store is still open (§UC-029).

#### UC-048 A decorator wraps a store to observe or police it  [happy]
- **Actor** A7.
- **Precondition** A store, and a wrapper that wants to count appends, refuse
  writes for a tenant that is over quota, or record what was read.
- **Trigger** The composition root binds the wrapper instead of the store.
- **Flow** The wrapper implements all eight methods of the contract and forwards
  each of them, once, neither replaying nor dropping a call.
- **Observed** Behaviour identical to the wrapped store except for the wrapper's
  declared policy. The wrapper is in the path for **every** operation, including
  every operation inside a caller transaction, because `Within` returns a context
  rather than an inner store (§UC-028).
- **Must not happen** A wrapper must not answer a `Capabilities()`,
  `Limits()` or `Backing()` of its own: it returns the wrapped store's values, or
  it is a different store pretending to be this one and every identity check in
  the document is defeated. It must not convert a cancellation, a conflict or an
  unknown commit into another class ([ES] capability obligation 7). It must not
  replay, buffer or re-inspect an `AppendRequest`'s records, **and it must not
  write into one's `Payload`, not even temporarily**: that array is the kernel's
  and the kernel reads it again, so a wrapper that redacts, compresses or encrypts
  in place rewrites a fact that was already decided (§INV-021's **third**
  hand-off, §D.14). Redacting is legal on a **copy** it forwards instead. It must
  not retain the `Records` slice past the call either, for the same reason one
  level out (§INV-021's sixth). It must not
  advertise a capability and then discover its absence after beginning work. A
  policy refusal must arrive **before** the forwarded call, never after a partial
  effect ([ES] obligation 4).
- **A wrapper may wrap an error and must not strip its classification** Adding
  context to a store's error with `%w` is the ordinary observing decorator and is
  legal: the kernel finds the `Outcome` with `errors.As`, so a wrapped
  `event.Failure` maps to the same row as an unwrapped one (§INV-045). What is
  forbidden is producing a **different** `Failure` — re-classifying is converting
  one class into another, which is the sentence above.
- **Who these obligations bind** A value that claims to be a wrapper of another
  store. They do **not** bind a fixture that claims to be a broken store: two of
  the suite's six self-falsification defects cannot be expressed by forwarding
  and are purpose-built defective stores for that reason (§UC-045) — one of them
  would otherwise have to buffer an `AppendRequest`'s records, which the bullet
  above forbids. The fourth defect is the mirror case and stays a decorator,
  because writing into a forwarded payload is what this bullet forbids a wrapper.
- **Why there is no unwrap chain and no `Next()`** Every question the kernel asks
  a store is a **required method**, so the exact outer value always answers and
  there is nothing to discover, nothing to walk and nothing that can be missing
  ([ES] obligation 3, [[D-115]], §INV-030); the optional `Transactional`
  interface plus a declared chain is deleted (§UC-033). A `Next()` accessor would
  be public API with no reader in phase 1, which `restrictions.md` §1 forbids; the
  moment any optional interface is added, [[D-061]]'s rule applies in full and
  the accessor arrives with it.
- **Control** The conformance suite runs against a store wrapped in **two**
  unrelated opaque middleware in both orders, plus a nil middleware, and every
  section must pass — ordering changes only the declared policy envelope and never
  makes a hidden effect reappear ([ES]'s conformance clause). Four of the suite's
  six self-falsification defects are decorators (§UC-045), so the mechanism is
  exercised whether or not an application writes one.

### Group H — Testing an aggregate (A6, mode E6)

#### UC-040 An aggregate is unit-tested with no database  [happy]
- **Actor** A6.
- **Precondition** An aggregate declaration and the in-memory store.
- **Trigger** A test constructs the store, binds, loads, decides, appends and
  reloads — including inside a transaction.
- **Flow** The app-usecase under test is the **same code**, byte for byte, that
  runs in production. What differs is two lines in the test's own composition
  root: the store's constructor, and the store's own way of binding a transaction
  into a context.
- **Observed** Real expected-version admission, real conflicts, real dense
  versions, real transactions with staging and re-validation at commit, real
  concurrency under `-race`. Not a mock: an implementation.
- **Must not happen** The memory store must not be a lenient implementation of
  the contract, must not skip a check the suite tests, and must not be described
  as a mock, a fake or a test double — it runs `eventtest.Run` and either passes
  or does not. A test must not need to edit the app-usecase to run against it.
- **What is honestly not shared, said rather than denied** The two composition
  lines above. "No line differs" is false: the transaction-binding vocabulary is
  store-private, so `eventmemory.WithTransaction(ctx, tx)` and
  `crud.WithExecutorFor(ctx, source, tx)` are two different lines. They belong to
  the composition root, the layer a test is allowed to replace.
- **Why the memory store pays for transactions when `jobsmemory` and
  `cachememory` did not** `jobs.Stager` is an optional producer path; `event`'s
  transaction seam is on the **main write path of any application that writes
  anything else in the same transaction**. Refusing it in memory leaves group E —
  ten use cases, five sentinels and a whole conformance section — unexercised by
  anything in the root module (Q12).
- **Control** The same test against a store that declares
  `Transactions: Unsupported` must skip the transactional half and report it
  skipped rather than passing it.

#### UC-041 A fold is exercised with no store at all  [happy]
- **Actor** A6.
- **Precondition** An aggregate and a list of changes, minted with
  `credited.New(id, payload)` for **one** identity. No store is needed to mint
  one: a fact handle knows its aggregate, so it knows the mapper, so an identity
  is all it wants (§UC-064).
- **Trigger** `accounts.Fold(id, zero, changes...)` — the same identity the
  changes were minted for, which the test already holds.
- **Flow** The pure fold runs over the caller's memory. No store, no context, no
  binding. The aggregate renders `id` through its declared mapper and refuses any
  change whose stream is not that one (§INV-044); then each change decodes its
  frozen bytes with its own revision's codec at
  the moment of the fold, so the value the fold receives is the same one a reload
  would produce and is reachable from nothing the framework holds (§UC-065).
- **What `Fold` does to the state it is given, and it is the contract** It
  **folds** it. `Fold` applies the caller's own fold functions to the caller's own
  value, in the loop the caller would otherwise have written by hand, so for every
  field of a reference kind — and for a state type that *is* one — the fold writes
  through the value the caller still holds. **The input is consumed: take the
  returned state and do not use the argument again**, which both blessed spellings
  already do (§UC-021, §D.5). It is the one inbound row in §INV-021's table whose
  answer is not *read only*, and §D.10's sixth residual.
- **Why it is not a copy, and why that is not a defect to fix** The framework
  cannot copy an `S` honestly: a **shallow** copy is what Go already performs at
  the call and is exactly what produces the hybrid, and a **deep** one is the
  transitive copier walk §UC-052 deleted with an argument that stands (§D.12). It
  must not copy either — a fold that mutates a large state in place is legitimate,
  `type Ledger map[string]int64` has no other kind (§UC-068), and a `Fold` that
  behaved differently from the loop the caller would have written is the one thing
  a pure seam may not do. What makes it survivable is what makes every residual
  here survivable, **the reload is the authority and it is one line away**; what
  makes it discoverable is that it is stated, enumerated and controlled.
- **Observed** The state those facts produce. This is the same function that
  advances a state between two appends (§UC-021) and the same one a load's
  private accumulator runs — **one fold, one name**. The second spelling round 3
  shipped, `event.Replay`, is deleted: two names for one concept is what
  `architecture.md` bans and it had no behaviour of its own.
- **Must not happen** There must not be a second exported fold. The fold must not
  reach a store, and reading the declaration's fold table **seals** it
  (§UC-005) — a test that folds and then declares another fact must be refused,
  which is the case a bind-triggered seal would pass. A fold must not apply a
  change **decided for another instance**: a state is one instance's, and the
  identity is what lets the fold say so.
- **Why the fold takes an identity** So that it can make the same comparison
  `Append` makes, at the other call that pairs a change list with an instance.
  §INV-044 is where that argument lives, in full and once.
- **Why it returns an error, and the causes are four** In the order `Fold`
  performs them, which is `Append`'s order (§D.5) and which is what decides the
  sentinel when two apply. **The first three are checks over the whole list,
  before any fold runs; the fourth is per change, during the fold** — which is why
  the returned value is not one answer for all four:
  1. **The identity `Fold` was given renders an illegal key** — the mapper runs
     here, so `accounts.Fold(zeroID, …)` is this case and it is `ErrKey`, request
     class, exactly as at `Load` and at `New` (§UC-051). It is first because (2)
     has nothing to compare against until the rendering exists, and reporting a
     blank identity as a stream crossing would put a request-class fault in the
     wiring class (GAP-81).
  2. **A change decided for a stream that is not this identity's** —
     `ErrWrongStream`, wiring class.
  3. **A `Change` carrying a deferred encoding or key failure from `Fact.New`**
     (§UC-024, §UC-051) — the change's own carried refusal, surfaced as it is:
     `ErrEncode`, `ErrTooLarge` or `ErrKey`, all request class.
  4. **A decode that fails when the fold runs** — a broken codec, whose proxy is
     `eventtest.RoundTrip` (§UC-042). It is `ErrPayload`, **history class**, the
     same sentinel a load gives for the same failure over the same bytes:
     `Fact.New` recorded the fact at the moment of decision, so a `Change`'s
     frozen array *is* the envelope's payload (§UC-063), and the class is stated
     over a fact's recorded bytes rather than over the store precisely so one
     decode failure needs one sentinel (§2.3, §UC-015). Not GAP-72's class misuse
     in the other direction: a panicking fold means the code is wrong and the
     bytes fine, while here the bytes genuinely cannot be read.

  Folding any of the four as a no-op would hide a fact that was decided and never
  applied. The error surfaces; it does not fold nothing.
- **What `Fold` returns with each error** Causes 1, 2 and 3 run before any fold
  does, so they return **the state that went in, untouched**, with the error.
  Cause 4 can only fire on change *k*, after *k−1* folds have already run, and it
  returns **the state as of the last change it applied** — which for a reference
  kind is the argument itself, already advanced, because the fold wrote through
  it. On any error the returned state is **not usable**; a reload is the
  authority.
- **Why that is not §INV-006's answer** `Load` returns the **zero** state on any
  failure because it owns its accumulator and started from a kernel-owned zero
  value. `Fold` was handed the caller's own value and has already advanced it in
  place, so a zero state would be a lie about what the caller's memory holds,
  would destroy a value the caller still has a claim on, and is unobtainable
  anyway — nothing can un-fold. §INV-006 is stated over `Load` and only over it.
- **Refusal** One sentinel per cause, in the list's own numbering: `ErrKey`
  (request) for 1, `ErrWrongStream` (wiring) for 2, the change's own carried
  refusal for 3 — request class, whichever of the three it is — and `ErrPayload`
  (history) for 4 (§2.3).
- **Control** Four. The same list folded with the identity it was minted for
  succeeds, so the check cannot pass by refusing every fold. One change of another
  instance's, added to an otherwise correct list, is refused — so the comparison is
  per change and not "refuse when the list is mixed". The same correct list folded
  with the **zero identity** is `ErrKey` and not `ErrWrongStream`, pinning the
  order of causes 1 and 2. And cause 4 driven at change 2 of 3 asserts both halves
  of the answer above — the sentinel, and that the returned state carries change
  1's effect and neither change 2's nor change 3's.

#### UC-042 A payload's revisions are round-tripped in a test  [happy]
- **Actor** A6.
- **Precondition** A fact with two or more retained revisions.
- **Trigger** `eventtest.RoundTrip(t, credited, v1Value, v2Value)`.
- **Flow** For each value: encode with its revision's codec, decode with the same
  codec, run the upcaster chain to the current type, compare. Reading the reader
  chain seals the declaration (§UC-005).
- **Observed** A failure that names the revision and the field rather than
  printing two structs.
- **Must not happen** The helper must not encode with the current revision's
  codec — that is the defect §UC-012 forbids and the one the per-revision codec
  exists to make impossible. It must not skip a retained revision. It must not
  require a store.

#### UC-061 The caller mutates the state it was handed, and a reload is unaffected  [edge]
- **Actor** A3, or A6 asserting the property.
- **Precondition** A loaded aggregate whose state holds a map and a slice.
- **Trigger** The caller writes into `state.Applied`, into `state.Lines[0]`, and
  into a nested `state.Header.Tags`, then reloads.
- **Flow** Nothing in the framework is reachable from any of those values. `Load`
  folded a private accumulator and returned it by value, once, retaining nothing
  (§INV-042). `Append` never saw a state.
- **Observed** The reload returns the fold of the stream, which disagrees with the
  mutated value — and that is the correct answer, because **the reload is the
  authority and it is one line away**. The stream itself is untouched.
- **Must not happen** The framework must not retain, memoise, re-fold or hand out
  a second time any value it gave the caller. There must be no cached state
  anywhere, so there is no second reader for a mutation to poison.
- **Why this use case exists at all** It is the falsification of the property that
  let the transitive copier walk, `event.Copy` and `eventtest.Copies` be deleted
  (§UC-052). "The framework retains nothing" is either testable or it is a
  sentence, and this is the test.
- **The case that would falsify it** A state field whose value came out of an
  `Envelope.Payload` by aliasing — the shape a compact binary codec produces when
  it decodes a `[]byte` or a string field without copying, and the one
  `event.JSON` happens not to produce. Mutating **that** field and reloading must
  still disagree with the mutation. It does, because a store hands out payload
  bytes **no party will ever write** (§INV-021's fourth hand-off, GAP-68); under
  the weaker *freeze* wording it did not, and the memory store's own history was
  writable through the caller's state. The same field reached through a `Change`
  instead of an envelope is §UC-065, the second hand-off of the same rule.
- **Control** Two. The same test asserts the state **before** the mutation equals
  the reload, so a store that returns a zero state to every load fails rather than
  passing this case. And the aliasing case above is driven with a codec that
  deliberately decodes by aliasing its input, so the property is exercised rather
  than assumed from `event.JSON`'s good behaviour.

#### UC-062 Two decisions in one operation, and the fold line is load-bearing  [happy]
- **Actor** A3, and A6 asserting it.
- **Precondition** An aggregate whose second decision is only correct if it sees
  the first one's fact — the duplicate-guard map is the ordinary shape.
- **Trigger** Load, decide, append, **fold**, decide again, append again
  (§UC-021).
- **Flow** The first append returns the advanced token, so the version half can
  never be forgotten. The state half is one visible, pure, greppable line.
- **Observed** Both appends succeed, and the second decision was taken on a state
  that contains the first fact.
- **Must not happen** The framework must not fold on the caller's behalf — a
  framework that folds for you holds your state — and must not silently reload.
- **Control, and it is the point of the use case** Delete the `Fold` line and the
  test must **fail**. That is what makes the line load-bearing rather than
  decorative, and it is the honest cost of retaining nothing: both appends still
  succeed, no error is returned anywhere, and only the assertion catches it.

#### UC-063 A payload carries a slice, and the caller mutates it after deciding  [edge]
- **Actor** A3.
- **Precondition** A payload type with a reference kind —
  `struct{ Lines []Line }`, `struct{ Tags []string }`, a map of attributes. All
  ordinary.
- **Trigger** `credited.New(id, payload)`, then the caller mutates `payload.Lines`,
  then `Append`, then `Fold`, then a reload.
- **Flow** `Fact.New` **encoded at the moment of decision** and kept the frozen
  bytes. It kept no value of the payload type at all — not the caller's, and not
  one decoded back from the bytes. The caller's payload is unreachable from the
  `Change` the instant `New` returns, and the value a fold applies is decoded from
  those bytes when the fold runs (§UC-065).
- **Observed** The recorded fact, the locally folded state and the reload all
  agree, and none of them contains the mutation. Mutating a payload between `New`
  and `Append` **cannot** change what is recorded.
- **The sequence in the other order is covered too, and by the same construction**
  Fold the change, mutate the state the fold published, *then* append that same
  change list. What reaches `Record.Payload` is still the frozen array, because a
  fold is handed a clone of it and a mutation of the fold's `E` therefore has
  nowhere to reach (§INV-021, §UC-065) — and the store it is handed to may read
  that array but never write into it, which is §INV-021's third hand-off. Without
  the kernel's clone it did reach: with an aliasing codec the state's slice **was**
  the frozen array, and the fact recorded was the fact as mutated (GAP-79).
- **Must not happen** A `Change` must not retain a value the caller can still
  reach, and nothing a fold hands the application may alias the array a later
  append will send. The framework must not require a payload copier, must not walk
  the payload's type graph, and must not refuse a payload type for the kinds it
  contains.
- **Why this is a construction rather than a rule** Deferring the encode to
  `Append` and storing the caller's value in the `Change` let a payload mutation
  change the fact recorded, with no line saying whether that was legal (GAP-43).
  Encoding at `New` makes the question unaskable instead of answering it, which is
  the strongest form the answer can take, at one encode priced in §D.13.
- **Control** The same test with a scalar-only payload must behave identically,
  so the property is not an accident of the reference kind.

#### UC-064 One operation touches two instances of one aggregate  [edge]
- **Actor** A3.
- **Precondition** The canonical two-instance operation, and it is not exotic: a
  transfer. Two identities of one aggregate, two loads, two decisions, two
  appends. Every value in it has the same Go type — two `At[Account]`, two
  `[]Change[Account]`, two `Account` — because Go cannot give a per-instance type
  and this document does not pretend otherwise (§UC-026).
- **Trigger** Appending one instance's changes through the other instance's
  at-token.
- **Flow** A change knows its stream, because `Fact.New` took the aggregate's own
  identity and rendered it through the declared mapper — the same mapper, the same
  single place, the same `Compose` advice (§D.1). `Append` compares each change's
  stream with the token's, before any statement and before the store is reached,
  and refuses on the first that differs. **`Fold` makes the identical comparison
  against the identity it is given** (§UC-041): the two calls that can cross two
  instances are the two calls that compare, and they compare the same thing.
- **Observed** The correct pairing writes each fact into its own stream. The
  swapped pairing — `repo.Append(ctx, toAt, debitChanges...)` — is a typed refusal
  naming neither identity, neither key and neither stream (§INV-025). Nothing is
  written by either.
- **Must not happen** One instance's debit must not land in another instance's
  history. There must be no way to append a change list decided for another
  instance, and no option, flag or convenience that skips the comparison. The
  refusal must not name the streams: a key is data, and §INV-025 has no exception
  for a helpful message.
- **Why this is a call-time refusal and not a compile error** Two *instances* of
  one aggregate are one Go type in every language without dependent types, so the
  first level of the safety ladder is unavailable and the second is taken instead
  — **the mistake is refused at the call, deterministically, with a named error**.
  §INV-020 is where the type parameter's scope is stated (GAP-54).
- **What it costs the DX, and why it is not a second thing to remember** The
  decision takes the aggregate's identity: `state.Credit(cmd.Account, cmd.ID,
  cmd.Minor)`. That is the application's **own type**, and a value the caller just
  loaded with; the framework cross-checks it against the token the load minted.
  The rejected `New(at, payload)` threads a framework type through every decision
  signature and leaves §UC-041's store-free fold needing a token it cannot get.
- **The residual, and it is two things that are not the same thing** They are
  separated because only one of the two is a framework-shaped error (GAP-70).
  1. **The domain-vocabulary mistake.** A caller that names the *wrong identity in
     the decision* and the matching wrong token in the append has written a program
     that debits the other account consistently, in its own vocabulary, three times
     over. Every framework value agrees with every other; no framework can see it,
     and no proxy could assert it without knowing the domain.
  2. **The one framework value that carries no identity: the state.**
     `accounts.Fold(id, otherState, changes...)` passes — the identity and the
     changes agree, and a state carries no stream to disagree with. The caller then
     decides on one instance's state under another instance's name. It is the
     *mechanical* crossing that survives, because a state is the application's own
     value and the framework holds none of it (§INV-042): there is no vantage point
     from which "this `Account` came from that `Load`" is observable, and a proxy
     would need the same one. §D.10 lists it.

  What **is** closed is the crossing between two values the framework did mint: a
  token and a change list at `Append`, an identity and a change list at `Fold`.
- **Refusal** `ErrWrongStream`, wiring class (§2.3) — the same shape and the same
  class as `ErrWrongStore`, because both are two framework values that were minted
  apart and put together by a hand.
- **Token** unchanged. Nothing reached the backing (§INV-040).
- **Control** The correct transfer in the same test must succeed and leave each
  fact in its own stream, so an implementation that refuses every append fails the
  control rather than passing this case; and a single-instance append of a change
  minted for that instance must succeed, so the comparison is not "refuse when
  there are two loads".

#### UC-065 A fold aliases a reference kind out of a change, and folding the change again is unaffected  [edge]
- **Actor** A3, or A6 asserting the property.
- **Precondition** A payload with a reference kind — `struct{ Lines []Line }` is
  the shape §UC-063 already calls ordinary — the fold §D.3 blesses,
  `s.Lines = e.Lines`, and **a codec that decodes that field by aliasing its
  input**, which §INV-021 establishes is an ordinary codec and not a broken one.
- **Trigger** Fold a change into a state; mutate `state.Lines[0]`; fold **the same
  change** again onto a fresh zero state; and reload the stream.
- **Flow** The change holds frozen bytes and an adapter, and nothing else. Each
  fold hands the codec a **clone** of those bytes, so the two folds receive two
  independent values whatever the codec does with what it is given, and the value
  the first fold published into the caller's state is the caller's alone
  (§INV-021).
- **Observed** The second fold produces exactly what the reload produces. The
  mutation is visible only in the value the caller mutated.
- **Must not happen** A `Change` must not retain a decoded value across the call
  that produced it, and its frozen bytes must not be the array a fold's `E`
  aliases. Two folds of one change must not be able to disagree. A fold must not
  be told to treat its `E` as borrowed — it may keep it, mutate it and publish it,
  because it is the fold's own.
- **What the fold's `E` may alias** Nothing the framework holds, at any depth: it
  is decoded for this call, handed over and forgotten, so an application that
  keeps a slice reached through `e` keeps memory nothing will look at again.
- **Why this use case exists** It falsifies §INV-042 and §INV-021's second
  hand-off, which two rounds got wrong in two directions (GAP-55, GAP-79).
  §UC-061 mutates a state whose values came from envelopes, §UC-063 a pre-encode
  payload; this is the third direction and the one neither reaches.
- **Control** Three. The same sequence with a scalar-only payload must behave
  identically, so the property is not an accident of the reference kind. The first
  fold's result must equal the reload **before** the mutation, so a framework that
  returned a zero state to every fold fails. And the aliasing codec decides it:
  with `event.JSON`, which copies on decode, the sequence passes against a kernel
  that never clones, so the shipped codec alone is evidence of nothing (§INV-021).

#### UC-066 A transaction is open and nothing is bound for this store's backing  [edge]
- **Actor** A3, by omission or by composition.
- **Precondition** Either of two routes, and they end in the same place.
  1. The caller opened a transaction and never bound it into the context — one
     forgotten `crud.WithExecutorFor` line, the mirror of §UC-046's omission.
  2. The store's `Capabilities().Transactions` is `Unsupported`, which §UC-033's
     precondition and §UC-040's control both make a live shape, so it has no
     executors of its own and nothing of its can ever be bound.
- **Trigger** `Load` or `Append` on that context, without calling `Within`.
- **Flow** `store.Transaction(ctx)` finds nothing of its own and answers the
  invalid authority with no error. §INV-041's **first** row applies: the operation
  runs on the store's own autocommit. It succeeds, `Empty()` is false, and the
  receipt carries the invalid authority.
- **Observed** The events are durable immediately and are **not** in the caller's
  transaction: rolling it back leaves them. That is correct, not a defect, because
  the transaction the caller opened does not exist for this store — in route 1
  nothing was bound, in route 2 its backing never could be.
- **Why no mechanism can see it, said rather than implied** The only thing any
  layer can look at is what is bound in the context for **this store's backing**.
  A transaction nobody bound is indistinguishable from no transaction; one over
  another backing is indistinguishable from another program's. §INV-041's answers
  are keyed on what the store *finds*, and there is no fourth for what it cannot.
- **What the framework offers instead, and it is a mechanism the caller opts
  into** `Within`, whose three jobs are §UC-028's. Both routes are refused loudly
  — `ErrNoTransaction` or `ErrNoTransactionBinding`, before any statement — the
  moment the caller says out loud that it expected a transaction. **This is an
  application obligation**, level four of the ladder and the losing answer, taken
  because the three above it are unavailable: the mistake cannot be made
  inexpressible, refused at declaration, or refused at the call by anything that
  has the information. Stated as an obligation rather than hidden (GAP-56).
- **Must not happen** The document must not claim this escape is prevented.
  §D.10's inventory row is narrowed to what §INV-041 actually guarantees — *an
  operation that escapes an executor the store can see* — and §UC-030's atomicity
  argument names the precondition it needs. A future reader must not be able to
  take silence for a guarantee.
- **Control, and it is the one that proves the obligation is real** The
  `transactions` conformance section drives route 1 — open a transaction, bind
  nothing, append, roll back — and asserts the events are **still there**. It
  asserts the documented unsafe outcome on purpose, because omitting it leaves a
  later reader free to assume the framework refuses. The paired control is
  §UC-028's: the same operation with the transaction bound puts the events inside
  it, so a store that ignores bindings fails.

#### UC-067 A fold panics  [edge]
- **Actor** A1's fold, called by the framework on A3's load.
- **Precondition** The ordinary Go fold defect, and it is not exotic: a write into
  a nil map. §D.0's own fold guards against it by hand, and an aggregate whose
  state type *is* a reference kind — `type Ledger map[string]int64`, which §UC-052
  blesses — reaches it on the first event of every fresh stream.
- **Trigger** A load, a store-free `Fold`, or the caller's advance between two
  appends. All three run the same function (§UC-041).
- **Flow** Nothing catches it. The panic unwinds through the fold loop and out of
  `Load` or `Fold` to the caller, and the framework does not recover it.
- **Observed** A panic on the goroutine that called, with the application's own
  fold on top of the stack — the shortest distance between the symptom and the
  line that has to change. A transport that recovers panics sees it like any
  other, as §UC-005 already says of the other programmer-error panic.
- **Why it is not recovered, argued rather than assumed** Reporting it as
  `ErrUpcast` puts a sentinel to work outside its class: a panicking fold means
  the code is wrong and the recorded bytes are fine, a recovered panic value is
  not the application error `ErrUpcast` declares it wraps, and a load of a
  single-revision fact would return "an upcaster refused" (GAP-72). The three
  alternatives are each worse. A **new sentinel** puts a programmer error into a
  runtime vocabulary a transport branches on, and §2.3's growth rule makes it
  permanent. **Reusing the declaration class** is a round trip to nowhere:
  `ErrDeclaration` is raised as a panic and never returned. And **recovering into
  anything at all** converts a deterministic bug — the same stream panics on every
  load, in every process — into a per-request refusal a retry loop will hammer.
- **Why the upcaster is different** It is the one application callback whose
  subject is data, so its refusal is a fact about a stream and belongs to the
  caller as a value; §UC-016 carries the argument and `jobs.invokeUpcaster` is the
  tree's precedent for recovering exactly that one.
- **Must not happen** The framework must not recover a fold's panic, must not
  convert it into any sentinel of §2.3, and must not return a partially folded
  state — there is no way to, because the accumulator is a local of the fold loop
  and the stack is unwinding past it (§INV-006 holds by construction). It must not
  log it ([[D-062]], §INV-011). The panic value must not be reformatted, wrapped
  or copied anywhere, which is what stops application data reaching an error
  surface (§INV-025).
- **Refusal** none. This is the one failure in the subsystem with no sentinel, and
  that is the decision.
- **Control** The same load with a fold that does **not** panic succeeds, so the
  case cannot pass by breaking every load; and the **upcaster** panic in the same
  test is recovered into `ErrUpcast`, so the two callbacks are asserted to be
  treated differently rather than assumed to be.

#### UC-068 The state type **is** a reference kind, and `Fold` advances the value it was given  [edge]
- **Actor** A3, or A6 asserting the property.
- **Precondition** `type Ledger map[string]int64`, which §UC-052 blesses and
  §UC-067 reaches on the first event of every fresh stream, and the only fold such
  a type can have: `this[e.Account] += e.Minor; return this`.
- **Trigger** `advanced, err := ledgers.Fold(id, state, changes...)`, and then a
  look at `state`.
- **Flow** Go copies the map header, so `advanced` and `state` are **one value**.
  Nothing in the framework decided that: the fold is the application's, the value
  is the application's, and `Fold` is the loop the caller would have written
  (§UC-041).
- **Observed** `advanced` and `state` agree, being the same map, so
  `state, err = ledgers.Fold(id, state, …)` — the spelling §UC-021 and §D.5 write
  — is a **no-op assignment** and folding the same list twice applies every event
  twice. The history is untouched by either, and a reload folds the stream over a
  fresh zero value, which is the authority and disagrees with the double
  application. The same holds one field in: for `struct{ Applied map[string]bool;
  Balance int64 }` the map advances and the scalar does not, leaving the argument
  a state no event sequence produces.
- **Must not happen** `Fold` must not clone the state — a shallow clone is what Go
  already performs, a deep one is §UC-052's deleted copier walk, and either makes
  `Fold` behave differently from the hand-written loop. It must not refuse a state
  type for the kinds it contains, must not require a `Cloner[S]` (§D.12), and must
  not be documented as leaving its argument alone.
- **Why this is a use case and not a note** The answer has to be falsifiable in the
  direction somebody will later "fix" it, and no other case sees it: §UC-065 folds
  the second time onto a **fresh zero state**, §UC-061 mutates a state the
  framework handed out, and §INV-004's first proxy replays through `Load` from a
  kernel-owned zero value. All three are blind here by construction.
- **Control, and it is the point of the use case** Fold a change list into a state
  holding a map and a slice and assert the **argument** is advanced, so a later
  `Fold` that cloned **fails** this test rather than quietly changing a frozen
  contract; with a scalar-only state in the same test asserted **unaffected**, so
  the case cannot pass by being true of every state type. Read from the other
  side, that pair is §INV-004's proxy (2) and its warning about folding one value
  twice.

### Group I — The store contract and the conformance suite (A5, mode E6)

#### UC-043 A store implementer runs the conformance suite  [happy]
- **Actor** A5.
- **Precondition** A type implementing the eight methods of §D.14, and a test
  file.
- **Trigger** `eventtest.Run(t, eventtest.Factory{...})`.
- **Flow** The suite dispatches named sections. Sections that hold for every
  store always run. Sections that depend on a capability run when the store
  **claims** it. Every section carries a control that a refuse-everything store
  fails. On every run, the suite also falsifies **itself** (§UC-045).
- **Observed** A pass is evidence. The section names are the vocabulary of a
  failure — `expected version`, `resumption`, `conservation` — so a failure names
  the property rather than a line number.
- **The anti-vacuity rules, and there are four** (1) A claimed capability whose
  hook is missing **fails**; it is never skipped, because a store must not be
  able to claim a capability and then avoid being tested on it. (2) A run in
  which every claimed section was skipped fails, because a store that claims
  nothing has been tested on nothing and a green run would say otherwise. (3) A
  capability field left **unstated** is refused at `Bind` and at `Read`
  (§INV-043), so a store cannot skip a section by forgetting a field. (4) Every
  section has a control that fails against a store that refuses everything.
- **Must not happen** The suite must not require a store to expose anything the
  contract does not name, must not construct a store itself — the factory does,
  because construction can fail (§UC-006) and only the factory knows how to report
  it — and must not skip a section silently.
- **Why the factory carries hooks rather than the contract carrying methods** A
  store cannot be asked, through `Store`, to begin a transaction, to produce a
  second value over its backing, or to fail an append on purpose: those are test
  affordances, and on the contract they would be in every production binary. They
  live on the factory, so the suite can tell "not supplied" from "not claimed".
- **Control** The suite run against a deliberately trivial store — an append-only
  slice with no transactions, built inside `eventtest`'s own test package — must
  **fail** the sections it cannot satisfy and **pass** the ones it can. It is also
  §INV-019's compile-time proof, because it lives in a third package: a type the
  contract requires a store to return that needed `event`-internal access would
  not compile. It is the first of §UC-045's three fixtures and the only one with
  nothing broken; the transaction-capable one there is a different fixture, not
  this one with a capability flipped.

#### UC-044 A store cannot exercise a capability, and says so  [edge]
- **Actor** A5, with the in-memory store as the worked case.
- **Precondition** A store that honestly cannot demonstrate part of the contract.
  `eventmemory` is the example and it is not a small one: it has **no commit
  window**, so it cannot lose a connection between issuing a write and confirming
  it, so half the failure-classification section (§UC-060's *unknown* answer) has
  no way to be produced.
- **Trigger** A conformance run.
- **Flow** The store declares what it supports. The factory supplies the hooks it
  can. The suite reports what it did not certify.
- **Observed** Three outcomes and they are reported in **three different words**,
  never collapsed: *passed*, *not certified* (the store cannot exercise this, or
  does not claim it), *failed*. A capability a store does not claim and a
  capability a store cannot demonstrate are reported with the same honesty and
  neither is ever reported as a pass.
- **Whose report this is** The **store under test's**, and only that store's.
  §UC-045's self-falsification run is not a store under test — the suite builds
  the defective store, writes its whole `Factory` and fixes its capability report
  — so nothing in that run is ever *not certified*, and the two mechanisms must
  not be read as one.
- **What `eventmemory` certifies, exhaustively** Expected-version admission,
  dense versions, strictly ascending positions with burned gaps on rollback,
  one-append atomicity, real transactions with staging and re-validation at
  commit, cursor resumption including a writer committing out of position order,
  conservation between the two reads, every bound, the refusal classes, close
  semantics, `-race` safety across many goroutines, and — because it is the store
  that decides the question — **payload ownership**: it retains its history, so it
  clones every payload it hands out (§INV-021).
- **What it cannot certify, and says so** Durability, because it declares
  `Persistence: Unsupported`. The *unknown* half of failure classification,
  because it has no commit window. Crash recovery, database isolation anomalies,
  and whether a real driver's `Transaction(ctx)` resolution is right — its own
  transactions are context values it minted, so it proves the shape, not the
  driver.
- **Must not happen** A skipped section must not be reported as a pass, nor
  counted as one. A store must not reduce its obligations by declaring less than
  it does — the tri-state capability and the unstated-zero refusal (§INV-043).
- **Why this is a use case and not a note** A suite that quietly turns absence
  into success is worse than no suite: it produces a green run that is evidence of
  nothing, and the next reader trusts it. [[D-020]], applied to a harness.

#### UC-045 The conformance suite falsifies itself on every run  [happy]
- **Actor** A5, and everyone who ever reads a green run.
- **Precondition** The store under test, and the suite.
- **Trigger** Any conformance run, including `eventpg`'s in phase 2.
- **Flow** The suite runs each section against **six deliberately defective
  stores**, one at a time, and asserts each fails the section named for it:

  | The defect | How it is built | The section it must fail |
  |---|---|---|
  | Reuses a position it has already issued | a forwarding decorator, rewriting the output | `global order` |
  | Returns a short stream page while more events exist at that bound | a forwarding decorator, truncating the output | `stream paging` |
  | Returns its newest position as a cursor while a lower one can still commit | a forwarding decorator, rewriting the cursor | `resumption` |
  | **Writes into an `AppendRequest`'s `Record.Payload` after the forwarded call returns** — the redacting or compressing wrapper §INV-021's third hand-off forbids | a forwarding decorator, rewriting the **input** | `payload ownership` |
  | Ignores `AppendRequest.Expected` and admits every append | **the suite's own defective store**, not a decorator | `expected version` |
  | Leaves a rolled-back transaction's events readable | **the suite's own defective store**, not a decorator | `transactions` |

- **Why two of the six are stores and not decorators** (GAP-66) Ignoring the
  expected version by forwarding would mean reading the stream and rewriting
  `Expected` to whatever the wrapped store is at — a read-modify the contract does
  not contemplate. Keeping a rolled-back transaction's events readable by
  forwarding would mean buffering an `AppendRequest`'s records and serving them
  from the decorator's own memory, which is **something §UC-048 names as
  forbidden**: those prohibitions bind a decorator that claims to forward, and a
  fixture that claims to be a broken store is not one. The fourth defect is the
  other way round and is why it is a decorator: writing into a forwarded payload
  is a thing §UC-048 forbids a **wrapper** by name.
- **Why the fourth defect writes *after* the forwarded call and not before** So
  that it fails one section rather than every section that decodes. Writing before
  forwarding hands the store the mutated bytes, the store persists them, and every
  later load, fold and paging comparison in the suite reads corrupted history — a
  run that is red in five places and cannot say which failure is the evidence.
  Writing after the call returns leaves the store's history correct and corrupts
  only the caller's `Change`, which is exactly what §D.9's inbound case measures:
  fold the appended list, fold the untouched list, compare both to a reload.
  "Not even temporarily" (§INV-021 hand-off 3) forbids write-then-restore too, so
  the fixture must not restore.
- **What the two defective stores are built from, since one of them needs
  transactions** `eventtest`'s own test package holds **three** fixtures, not one
  with two variants (GAP-83):
  1. **The trivial store** — an append-only slice, `Transactions: Unsupported`,
     nothing broken. §UC-043's control and §INV-019's compile-time proof.
  2. **The admit-everything store** — (1) with `AppendRequest.Expected` ignored.
     It needs no transactions, so it is (1) with one rule broken.
  3. **The leaky-rollback store** — claims `Transactions: Supported`, owns its own
     staging, and makes a rolled-back transaction's events readable. **Not** (1):
     a store with no transactions cannot commit this defect, and the
     `transactions` section would not run against it at all.
- **Where its transaction comes from** From the suite, because the suite built the
  store: the self-falsification run's whole `Factory` is `eventtest`'s, where an
  ordinary run's `Begin` belongs to the implementer (§D.9). The suite therefore
  also fixes each defect's capability report, so **the section a defect is aimed
  at always runs for it** — a skipped section is never a pass for a defect, which
  the claim below needs and the store under test's capabilities could not
  guarantee.
- **Observed** If any defect **passes** the section it is aimed at, the run fails
  with *the suite no longer detects this*, naming the defect and the section.
- **Must not happen** The self-check must not be an opt-in, a separate target or
  a test somebody remembers to write once, and must not be skipped when a store is
  slow. **No defect's section may be skipped, for any reason**: the suite writes
  this run's whole `Factory` and fixes each defect's capability report. *Not
  certified* is §UC-044's word about the **store under test** and is never an
  outcome of the self-check — a skip path here is what the three-fixture shape
  makes unreachable, and building one back costs a store that declares
  `Transactions: Unsupported` a sixth of the self-check's coverage.
- **The claim, and it is the one this suite can hold** Each defect **fails the
  section named for it**. Neither *exactly one section* nor *no unrelated section*
  is true here, and both are recorded rather than assumed away: the
  admit-everything store also fails `concurrency`; the short-page decorator also
  fails `dense versions` and `conservation`, because a truncated stream read is a
  smaller set than the global read and a load through it returns a truncated
  state; and the position-reusing decorator fails whatever else compares
  positions. That is what a store lying about one property does to the data every
  other section reads, so the **named** section is the evidence and the rest are
  consequences. Partitioning the sections so a stronger claim holds is deferred
  (§8, GAP-25), which is why a run reports section names rather than a count. What
  would make a run unreadable is a defect that fails *everything*, which is the
  bullet above and why the fourth defect writes after the forwarded call.
- **Why it is worth its cost** A conformance suite is the one artifact whose
  failure mode is silent by construction: one that tests nothing passes
  everything, and nobody notices until a store is wrong in production. Making the
  sanity check a property of every run catches the suite's own rot with the same
  command that runs it.
- **Control** With the six defects removed, the plain store must pass, so the
  self-check cannot pass by failing everything.

---

## 5. Invariants

Format: **Statement / Falsified by**, and where a document is the reason,
**Why**. An invariant with no falsification is a claim, so every row names how a
test or a check breaks it. Where only half of an invariant is enforceable by the
framework, the two halves are separated and the unenforceable one names its
runnable proxy.

INV-001…INV-014 restate [ES]'s non-negotiable list, with one exception: [ES]'s
ninth — delivery is at least once and no wording claims otherwise — is §INV-036 in
§5.2 rather than §5.1, because it is stated over this document's own read
contract. INV-015…INV-036 are this document's own; INV-037 is **withdrawn** in
place; INV-038…INV-045 were appended one round at a time, never inserted.

### 5.1 From [ES]'s non-negotiable list

#### INV-001 History is append-only
- **Statement** No API updates, deletes or rewrites a stored envelope or payload.
  A correction is a new fact appended after the one it corrects. The kernel
  exposes no update path, and a store's contract has no method that could express
  one.
- **Falsified by** A method inventory of `Store` and every exported type in
  `event`: a signature naming an existing version or position as a *target* rather
  than as a bound is a violation. Plus a conformance section that appends, reads,
  appends again and asserts the first envelope is byte-identical.

#### INV-002 An append of at least one change is admitted only at the observed version
- **Statement** For an append of one or more changes, the store admits it if and
  only if the stream's committed version equals the at-token's version. There is
  no parameter, overload, option or flag that skips the check, and no way to
  produce a token carrying a version the caller chose (§INV-015).
- **Scope, and the carve-out is deliberate** An append of **zero** changes writes
  nothing and touches no store, so it performs no version check (§INV-031). Read
  without the carve-out, this invariant is falsified by the phase's own chosen
  behaviour (GAP-17). It is the cost of the no-op choice (Q5), and nobody may
  "fix" it by sending an empty append to the store.
- **Falsified by** Two writers at one version: exactly one succeeds
  (§UC-022). A grep of the exported surface for any append-shaped call taking a
  `Version`. A conformance section that appends at a stale version and asserts
  the refusal.

#### INV-003 One append is one atomic unit
- **Statement** The event rows and the stream's version advancement of one
  append commit or roll back together. A batch is never split, never chunked and
  never partially applied. Atomicity **across** streams exists only inside a
  transaction the caller owns (§UC-028) and is never implied by an append.
- **Falsified by** A conformance section that injects a failure between the rows
  and the advancement and asserts neither is visible; a batch at the bound that
  fails and leaves zero events; the absence of any multi-stream append spelling.

#### INV-004 Folds and upcasters are pure, and *pure* here means one thing rather than two
- **What the signature holds** A fold is `func(S, E) S` and an upcaster is
  `func(A) (B, error)`. Neither receives a context, a store, a clock, a logger or
  a source of randomness, and a fold has no error channel at all (§INV-017). That
  half is enforced by the framework and is checkable from the source.
- **What is claimed, and what is not** Claimed: a fold's **result** is a function
  of its two arguments and nothing else — no clock, no file, no environment, no
  package counter, no network. **Not** claimed: that a fold leaves its `S`
  argument alone. A fold may write through every reference kind its state reaches,
  and `type Ledger map[string]int64` has no other way to work, so `Aggregate.Fold`
  advances the caller's own value in place **by contract** (§UC-041, §UC-068).
  Reading this as forbidding that is how a later reader "fixes" a frozen contract.
- **What is an application obligation** A fold that writes to a file, increments
  a package counter or calls a global compiles and runs, and the framework cannot
  see it. This half is an obligation, not a property.
- **The two checkable proxies, and exactly what each one pins** (1) Two replays of
  one stream through `Load`, and a replay under an already-expired deadline,
  producing one state — which pins the fold at the **kernel-owned zero value**.
  (2) One (state, changes) pair folded twice **from two independently constructed
  states**, producing two equal results — the same property at `Aggregate.Fold`,
  where the origin is the caller's and proxy (1) cannot reach. Proxy (2) must use
  two constructions and **never** fold one value twice: that asks a different
  question, whose answer is §UC-068's, and a test confusing the two would report
  the contract as a violation.
- **What neither proxy sees** A side effect outside the state, and a
  nondeterminism that happens to agree across two adjacent runs. That is the
  honest scope, stated rather than left to imply the invariant is enforced.
- **Falsified by** A source check on the two signatures; two replays compared; a
  replay under an expired deadline; and proxy (2), with a fold that reads a clock
  asserted to fail both proxies and §UC-068's control asserted to still pass, so
  the two questions stay apart.

#### INV-005 Wire identity is declared, and the key's rendering is frozen
- **Statement** A stream family, an event type name and a payload revision are
  **declared** wire values, never Go type names, translated labels,
  tenant-derived strings or reflection output. Renaming a Go type changes no
  stored value. The stream **key** is the fourth wire value and it is *computed*
  rather than typed, so its rule is a freeze: **once a stream has been written,
  that identity's rendering may never change.**
- **Why the key needs its own clause** An ordinary refactor rewrites it: a field
  added to an ID struct, `Key(id)` swapped for `Compose(id)`, a `strconv` format
  changed. Each renders the same identity to a different key, and the failure is
  quiet — every aggregate reads as version 0 with a zero state, which §UC-009
  makes indistinguishable from a fresh install, and is then written to, producing
  two divergent histories for one identity.
- **Falsified by** A fixture that writes with one spelling and reloads with the
  other and asserts the state is lost; a rename of every Go type in the sketch
  with the stored values asserted unchanged; §UC-050's control, which asserts the
  two legal spellings of one identity produce two **different** keys.

#### INV-006 No partially rehydrated state ever reaches a caller of `Load`
- **Statement** If any envelope in a load fails — unknown type, unreadable
  revision, malformed payload, refusing upcaster, oversized payload — the caller
  receives the **zero** state and a zero at-token, never the accumulator. There
  is no lenient mode and no option that produces one.
- **Its scope is `Load`, and the other fold answers differently for one reason**
  `Aggregate.Fold` was handed the caller's own state rather than starting from a
  kernel-owned zero value, and has already advanced it in place by the time its
  fourth cause can fire, so a zero state is neither honest nor obtainable there:
  it returns what it has folded, with the error, and names the state unusable.
  §UC-041 carries that contract; this invariant is not about it.
- **Falsified by** Each of the five failures injected mid-stream, each asserting
  the returned state equals the zero value and the returned token's backing is
  invalid; and the control that the clean stream loads.

#### INV-007 A conflict is never retried by the framework
- **Statement** No layer of this library re-appends a proposed change list after
  a conflict. A conflict requires a fresh load and a new domain decision, because
  the decision was taken on a state that is now stale.
- **Falsified by** A store that counts append attempts: exactly one arrives per
  caller call. A source check for a loop around an append inside `event`.
- **Why** [ES], and [[D-040]] applied to append: a conflict is not retryable *by
  the framework*, because the framework cannot re-decide.

#### INV-008 Full replay is the only authority
- **Statement** The state of an aggregate is the fold of its stream and nothing
  else. Phase 1 has no snapshot, no cache and no memo of a folded state anywhere
  (§INV-042), so there is no second answer that could disagree.
- **Falsified by** A grep of `event` for any retained `S`; two loads of one
  stream compared; §UC-061.

#### INV-009 Versions are dense, positions are not, and neither is a clock
- **Statement** A stream's versions are dense from 1 with no gaps, no reuse and
  no reassignment. Global positions are strictly increasing over committed events
  and are never reassigned; **gaps are permitted and are normal** — a rolled-back
  append burns its positions (§UC-029). The recorded instant is store-assigned,
  carries no monotonicity promise, and is **not** an ordering key: two events at
  positions p and p+1 may carry instants in either order.
- **Falsified by** A rollback between two appends asserting a gap and asserting
  the second append's position is higher; a stream read asserting versions
  1..n with no gap; a store with a deliberately backwards clock passing every
  ordering section.

#### INV-010 A revision is derived, never typed
- **Statement** A retained revision's number is its position in the declared
  reader chain. There is no constructor, field or argument anywhere in phase 1
  that takes a revision number, so a revision cannot be typo'd, duplicated or
  left with a gap. A chain always starts at revision 1.
- **Why the "does not compile" claim depends on that last clause** A spelling that
  lets a chain start above 1 by naming the number has an off-by-one that decodes
  newer bytes into an older struct **successfully** — revision n+1's payload is
  usually n's plus a field — dropping it and folding a wrong state with no refusal
  anywhere (non-goal 13). A later retention feature introduces the first such
  number and owes this invariant a check of its own.
- **Falsified by** A grep of the exported surface for any parameter of a
  revision-shaped type; a declaration with three revisions asserting the reported
  current revision is 3.

#### INV-011 No identity, payload, version, position, key or cursor becomes a default field
- **Statement** Phase 1 emits no log line, no span and no metric, so the
  invariant holds by construction; what it forbids for later is that any of those
  values becomes a *default* field of one. A refusal's message names declared
  identifiers and a classification, never data (§INV-025).
- **Falsified by** A source check that `event`, `eventmemory` and `eventtest`
  import no telemetry package and call no logger; a table test over every
  sentinel's message asserting no key, payload, version, position or cursor.

#### INV-012 Event history is not a CRUD collection
- **Statement** No exported surface lists, filters, searches, sorts or deletes
  events by anything but the two reads this document defines: one stream in
  version order, and the log in position order. There is no query object, no
  predicate, no filter and no `port.Repository` over envelopes.
- **Falsified by** A method inventory of `Store`, `Log` and `Reader`.

#### INV-013 No package-level mutable state, and no constructor starts anything
- **Statement** Nothing in `event`, `eventmemory` or `eventtest` holds
  package-level mutable state, registers anything by import side effect, or
  starts a goroutine, ticker, reaper or publisher in a constructor. A declaration
  is a value the application owns; a binding's set of bound families and a
  store's own state are **per-value** state with the caller's lifetime, which is
  not what this forbids.
- **What it does not forbid, said explicitly** The two checks the framework can
  actually perform — the bound-family set on a `Binding` (§UC-059) and any memo on
  a store value. State a caller constructed, whose lifetime is the caller's, is
  not a global: it is not import-order dependent, it is testable in isolation, and
  two of them do not interfere.
- **The predicate, and it targets mutation rather than kind** The obvious one — a
  package-level `var` of a mutable *kind* — flags all twenty-two exported
  sentinels and every `*Aggregate` and `*Fact` §D.0's declaration idiom requires,
  and a check that must be suppressed on its first run is how a structural check
  becomes decorative (GAP-63):

  | Package-level `var` in `event/...` | Verdict |
  |---|---|
  | an error sentinel whose initialiser is `errors.New` or `fmt.Errorf`, never assigned again | permitted — the value is immutable and the tree's own convention |
  | an `*Aggregate` or `*Fact` — the declaration idiom | permitted — its table is sealed on first observation (§UC-005), which is what makes it read-only for its whole observable life |
  | any `var` that is the target of an assignment, an index assignment, a `delete` or an `append` outside its own initialiser | **violation** |
  | any `var` of map or slice kind | **violation** — there is no legitimate one, and both are mutable through a copy |
  | any `var` holding a `sync` type, or a struct holding one | **violation** |

- **Falsified by** The AST check above over the non-test files of `event/...`, run
  as an arm of `make check` with the sentinel and declaration idioms present so a
  wrong predicate fails immediately; the grep for `^func init(`, `go func(` and
  `go <ident>` that `scripts/tenancy_test.go` already performs; a goroutine-count
  probe around construction; a declare-after-a-store-free-fold case, which a
  bind-triggered seal would pass (§UC-005).
- **Why** [HOUSE] records the exact failure: lazily resolved shared state written
  outside its `Once`, found by reading rather than by running.

#### INV-014 No start-up migrates, creates or discovers anything
- **Statement** Phase 1 writes no schema and has no migration, so this holds by
  construction. What it fixes for phase 2 is that what a deployment runs is a
  **profile the composition root selects** ([[D-101]]): a store's constructor
  verifies what it was told to verify and never silently creates.
- **Falsified by** In phase 1: the absence of any DDL, any `CREATE`, any embedded
  migration and any filesystem read. In phase 2: `eventpg`'s start-up section.

### 5.2 The seam's own invariants

#### INV-015 An at-token is minted, never manufactured
- **Statement** A value that authorises an append at a version exists only where
  the framework produced it: `Load` and a successful `Append`. `At[S]` has no
  exported field, no constructor, no parser and no unmarshaller, and its type
  parameter makes an `At[Order]` unusable where an `At[Account]` is wanted.
- **The three defences, in the order they actually hold** A composite literal of
  an all-unexported-field struct **does** compile — `event.At[Account]{}` is
  legal Go — so the argument must not rest on unconstructibility alone. (1) Its
  stream key is empty, and `Append` applies the kernel's key rules to the key it
  is handed as well as to one it produced, precisely so that a key no mapper made
  is refused with `ErrKey` before any statement (§UC-051). (2) Its backing is
  invalid, and an invalid backing matches nothing including another invalid one,
  so §UC-031 refuses it with `ErrWrongStore`. (3) The forms that genuinely do not
  compile — naming a field, converting from a struct with the same shape,
  unmarshalling into one — are pinned by a build-failure fixture. The first is the
  one that fires; defence (2) is why removing it does not lose the claim (GAP-82).
- **Why this is the strongest claim in the phase** It is [[D-117]]'s shape: a
  value that authorises a write exists only where the framework produced it, which
  makes "the version is not a parameter, so it cannot be wrong" true rather than
  aspirational.
- **What a `Stream` being constructible does not buy** `Stream`, `Key`, `Version`
  and `Position` are plain types with exported fields and conversions, because a
  store in another package must construct all four (§D.14, §INV-019). A caller can
  therefore write `event.Stream{Family: "accounts.account", Key: "x"}` — and it
  has **nowhere to go through the kernel**: `Repo` takes an identity and a token,
  `Log` takes a cursor, and neither has a parameter of type `Stream`. The only
  surface that accepts one is `Store` itself, and a party holding a `Store` holds
  the equivalent of the database handle — A2, which never decides, and which is
  why a consumer is handed a `Log` (§UC-036). The contract's data structs are the
  **store seam**, and this document's safety claims are all about the caller seam.
- **Falsified by** A build-failure fixture per unconstructible form; a test that
  appends a zero token and asserts `ErrKey`; a test that appends a token whose
  backing is invalid against a real store and asserts `ErrWrongStore`; a signature
  inventory of `Repo`, `Binding` and `Reader` asserting no exported parameter of
  type `Stream`, `Version` or `Position`.

#### INV-016 Store identity is the backing, and backings compare with `Equal`
- **Statement** What two stores share, or do not, is **what they write to** — the
  log a memory store owns, the database a SQL store writes to — never the store
  value, never a pointer, never a scope kind. Two store values over one backing
  are one store for every purpose in this document.
- **How two backings are compared, and it is one call** `Backing.Equal` **is**
  `crud.SameDataSource(this.identity, other.identity)`, and `Backing.Valid` is
  `crud.SameDataSource(this.identity, this.identity)` — the tree's own idiom for
  "is this usable as an identity at all" (`crud/catalog/set.go`). That function
  does most of what this invariant needs: a **nil interface** matches nothing, a
  non-comparable identity matches nothing, dynamic types must match, and only then
  are the identities compared. The zero `event.Backing{}` holds a nil interface,
  so it is invalid and two zero backings compare **unequal** — the requirement
  `==` cannot express. A store answering an invalid backing is refused at `Bind`
  and at `Read` (§UC-031, §UC-036).
- **The one part it does not do, stated as its own rather than attributed** A
  **typed nil** — a `*sql.DB` a store failed to set — is not a nil interface, so
  `crud.SameDataSource(id, id)` answers **true** and two stores that both forgot
  would compare `Equal` (GAP-78). That is `SameDataSource` behaving correctly for
  its own job and narrower than this invariant needs, so the difference is closed
  where the value is minted: **`NewBacking` and `NewAuthority` refuse an identity
  that is nil by any route** — a nil interface, or a nil pointer, map, slice,
  channel or func inside one. Refusing at construction is the second rung of the
  ladder and costs one check off every request path.
- **`event` writes that predicate itself, and the duplication is deliberate**
  `crud`'s own `isNilValue` is the same six-kind test and is **unexported**, so
  reuse would cost another package's surface for one caller; §D.15's import table
  stays at three `crud` symbols and names `reflect` as the means (GAP-85). It is a
  second answer where `SameDataSource` is imported rather than restated, and the
  difference is why: `SameDataSource` is a **comparison on every append** whose two
  answers diverging changes which program is admitted — §UC-031, §UC-054 and this
  invariant would answer differently from the rest of the framework (GAP-58) —
  while this is a **constructor-time** total function over six kinds whose two
  answers, if they ever diverged, both refuse. It has a six-case control, below.
- **Falsified by** Five controls: two backings over two stores must be unequal;
  two store values over one backing must be equal and must accept each other's
  tokens (§UC-054); two invalid backings must be unequal, which an `==`
  implementation fails; a pair of `crud.Source` values driven through **both**
  `crud.SameDataSource` and `Backing.Equal`, with the two answers asserted
  identical for a matching pair, a mismatched pair, a nil and a non-comparable
  identity — so a reimplementation that drifted from the framework's answer fails
  rather than passing quietly; and `NewBacking` asserted to refuse **each of the
  six nil routes** — a nil interface, and a typed nil pointer, map, slice, channel
  or func inside one — with a non-nil identity of each of those types accepted in
  the same test, so the refusal is not "refuse everything" and the restated
  predicate is pinned case by case rather than by resemblance to `crud`'s.
- **Why** Store identity as *instance* identity is correct only for the in-memory
  store and is wrong the moment a deployment constructs a store per request over
  a borrowed tenant lease, which is [ES]'s own tenancy composition (§UC-055).

#### INV-017 A fold cannot fail; only an upcaster may refuse
- **Statement** A fold has no error return. The single place an
  application-supplied pure function may say "no" on the read path is an
  upcaster, whose refusal reaches the caller as `ErrUpcast` wrapping the
  application's own error. **An upcaster's panic** is recovered and reported as
  that same refusal, wrapping nothing, rather than taking the process down for a
  data problem — which is `jobs.invokeUpcaster`'s existing shape.
- **The asymmetry is the whole content of this invariant** An upcaster's subject
  is data and may therefore fail on data; a fold is handed a value the chain
  already accepted and cannot, which is why it has no error channel. A fold's
  panic is **not recovered**: recovering it would invent a refusal for the case
  this invariant says cannot happen (§UC-067, GAP-72).
- **Falsified by** The fold's signature; an upcaster that returns an error and an
  upcaster that panics, both asserting `ErrUpcast` and the first asserting the
  application's own error is reachable with `errors.Is`; and a **panicking fold
  asserted to panic out of `Load`** rather than to return any sentinel of §2.3,
  with the non-panicking fold in the same test as its control (§UC-067).

#### INV-018 The store never sees a Go type
- **Statement** No signature in the store contract carries a type parameter, an
  aggregate, a fold, an upcaster, a codec or an application value. A store
  receives a stream, a version, a type name, a revision and bytes, and returns
  the same. It **carries** its limits and capabilities and never applies them:
  the kernel enforces every bound on the store's behalf, so a store cannot
  enforce one differently.
- **Why the store carries what it does not apply** The alternative is the kernel
  naming `eventmemory.Spec` to read a cap, which is the import inversion
  §INV-019 exists to forbid.
- **Falsified by** A signature inventory of `Store` and `Log`; a source check that
  no file under `event/` outside a store's own package names a store's package.

#### INV-019 A second store implementation costs zero diffs to `event/`
- **Statement** Adding a store implementation requires new files in its own
  package and **no change to any file under `event/`**. There is no registry to
  add a row to, no type switch to extend, no kernel method whose signature names
  a backend, and no value a store must return that it cannot produce from its own
  package.
- **How it is held rather than promised** An **executable check**:

  ```
  git diff --stat <phase-1-tag> -- event/ ':!event/eventpg'
  ```

  must be empty, run as an arm of `make check`. `scripts/checks.sh` is exactly
  where structural checks `go test` cannot see already live, and this is one of
  them: no compiler and no test suite can see a diff.
- **The compile-time half, available today** `eventtest` builds a deliberately
  trivial second store — an append-only slice with no transactions — inside its
  **own test package**, a third package again, and runs `Run` against it. If any
  type the contract requires a store to return needed `event`-internal access,
  that fixture would not compile. **That fixture also returns a classified failure
  of every `Outcome`**, which makes the error — the one value that travels on
  every method (GAP-67) — part of the compile-time proof rather than outside it.
  It compiles today or the seam is wrong today (§UC-043's control).
- **Falsified by** The two above, plus §D.15's per-value walk: every value
  `eventpg` must return is a plain struct with exported fields, a defined string
  type it converts into, an exported constant, an error built by `event.Failure`
  over an exported `Outcome`, or a value with an exported minting constructor
  whose forgery buys nothing (§INV-028, §INV-045).

#### INV-020 A value of one aggregate is not usable with another, and the compile-time half has a scope
- **Statement** A change, an at-token, a fact handle and a repository are all
  parameterised by the **state type** (and the identity type), never by the
  aggregate. So the guarantee has two halves and they are two different
  mechanisms:

  | The two aggregates | What refuses the crossing |
  |---|---|
  | have **different Go state types** — the ordinary case, and the one the type parameter is for | a **compile error**, with no runtime it can reach: no `any`-typed change list, no registry keyed by string |
  | share **one Go state type** | a **call-time refusal**, `ErrWrongStream`, for the two crossings that pair two values — a change with a token — because a change carries its `Stream` and a stream's first field is the **family** (§INV-044). The third crossing, the **repository** itself, has no second party and is described below rather than refused |

- **Why the repository crossing is not a third refusal, and what it does instead**
  A repository is a *handle* to (store, declaration), and the two calls read it
  for different things. **`Append` does not read the repository's declaration at
  all**: the stream comes from the token, the bytes and the wire type and revision
  from the changes — each of which carries its own fact's — and the bounds, the
  backing and the transaction answer from the store. So over one state type
  `repoB.Append(ctx, atFromA, changesFromA...)` is not a crossing but the *same
  call* `repoA.Append` would have made — it lands A's facts in A's stream, at A's
  version — when both repositories are over one store, and is `ErrWrongStore`
  when they are not (§UC-031). There is nothing to refuse and nothing wrong to
  refuse it for. **`Load` is the opposite case**: the repository is the *only*
  party that names a family, so `repoB.Load(ctx, anAIdentity)` is correct
  behaviour for `repoB` — it renders B's key, folds B's folds and returns B's
  state, with no error — and no value in the call carries A's family to compare it
  against. That is a residual, not a refusal, and it is §D.10's fifth. Giving
  `Repo` its own family comparison would close neither: it would refuse the append
  that is harmless and would still have nothing to compare at the load.

- **Why the second row exists, and it is not exotic** `*Aggregate[Ledger, LedgerID]`,
  `Change[Ledger]`, `At[Ledger]` and `*Repo[Ledger, LedgerID]` are the same Go
  types for every aggregate declared over `Ledger`, so `repoA.Append(ctx, atFromB,
  changesFromB...)` compiles. Two bounded contexts reusing one value type is
  something no framework can prevent; §UC-052 blesses a state type that **is** a
  map; and a generated declaration set emits one `Aggregate[Doc, DocID]` for
  several document kinds. Stating the compile-time half with no scope tells
  whoever writes `Append` that the family half of the stream comparison is
  redundant, when it is the only thing that catches these (GAP-80).
- **Why the shared-state case is not closed at the type level** A per-aggregate
  phantom parameter would close it and is rejected in §D.12: it costs every
  consumer a marker type whose only job is to be distinct, it adds a parameter to
  four caller-seam types, and it cannot be inferred at the call site, which is
  §D.11's whole subject. It also buys nothing for the larger risk — two
  *instances* of one aggregate — which no type parameter can reach and which
  §UC-064 refuses the same way.
- **Falsified by** A build-failure fixture per crossing (change, token,
  repository) over **two different state types**, each asserting the compile
  error, with the control that the matching combination builds. And, over **one**
  state type, a runtime assertion for each crossing that *has* a runtime answer:
  the **change** and the **token** crossings return `ErrWrongStream` and write
  nothing, with the matching combination succeeding as their control; the
  **repository** crossing is asserted to be what the clause above says it is — an
  append through a second repository over one store is **admitted** and lands in
  the token's stream, and a load through it returns that repository's own
  aggregate — so the row is pinned rather than overclaimed and a later reader
  cannot mistake the silence for a refusal. A suite that wrote only the first half
  would prove the invariant for the shape it was written from.

#### INV-021 Nothing the framework holds and nothing it hands out share mutable memory
- **Statement, in both directions** *Outbound*: a value the framework returns —
  a state, an envelope's payload, the slice that carries it, a receipt, the `E` a
  fold is given — is reachable from nothing the framework **or the store** retains
  and stays valid for as long as the caller keeps it. *Inbound*: a value the
  caller hands over is read and not retained, with two retentions that are handles
  the caller constructed for exactly that, and **one** value the framework's own
  loop writes through — the state at `Aggregate.Fold`. Both directions are
  **enumerated** below, and the enumerations are what make the rule total rather
  than true of the values the two stores and one codec that exist happen to move.
- **One rule, and it is stated over the hand-offs rather than over the holders.**
  *Whoever hands mutable memory to another party says whether anybody will write
  it again* — and the answer is quantified over what the **recipient** may do: a
  recipient may keep what it is handed **indefinitely**, read it **at any later
  time, from any goroutine**, and, for what a read returns, write into it. A
  sender that is still a reader, **or that does not own the memory it is about to
  hand over**, must make the hand-off safe.
- **The outbound domain, enumerated before the rule so the rule can be total over
  it.** Mutable memory crosses a party boundary at exactly **seven** points: five
  carry payload bytes, two are the slices that carry records and envelopes.
  Everything else crossing any boundary is a string, a scalar, a func value, an
  opaque handle or an error, and no party can write through one. **The store
  boundary — §D.14's eight methods — contributes exactly four of the seven**:
  `AppendRequest.Records` and `Record.Payload` inbound to a store, `[]Envelope`
  and `Envelope.Payload` out of it; `Stream`, `Key`, `Cursor`, `Version`,
  `Position`, `Limits`, `Capabilities`, `Backing`, `Authority` and a classified
  `error` carry no writable memory at all. **The numbering is stable and cited by
  ordinal elsewhere**; later rows are appended rather than inserted.

  | # | The hand-off | Is the sender still a reader, or a non-owner? | What makes it safe, and on whom |
  |---|---|---|---|
  | 1 | a **codec** → `Fact.New`, `Encode`'s output | **yes** — it may reuse an internal buffer and cannot be asked to promise otherwise | the **recipient**: `Fact.New` clones and freezes with a full-slice expression, which is `jobs.EncodedPayload`'s exact shape |
  | 2 | the **kernel** → a **codec**, a `Change`'s frozen array at every fold | **yes** — every later fold, and every later append of the same slice | the **sender**: `Fold` clones before each decode, once per change per fold, so an aliasing codec's value points into a per-fold copy and the fold may publish it |
  | 3 | the **kernel** → a **store**, that same array as `Record.Payload` | **yes** — the same reads; an append does not end a `Change`'s life | the **recipient**: a store, and any decorator forwarding the request, **reads it, clones it if it retains it, and never writes into it, not even temporarily** (§D.14, §UC-048). It is the one hand-off answered by a promise instead of a copy, and the bullet below is why |
  | 4 | a **store** → the kernel or a caller, `Envelope.Payload` | **it must be neither** — it clones what it retains **and what it does not own**, and never reuses a buffer across two calls | the **sender**, which is why a consumer may write into a page it was handed and why the load path need not clone |
  | 5 | the **kernel** → a **codec**, a store's `Envelope.Payload` on the load path | no — the kernel retains none of it, and row 4 already discharged it | nothing, and naming it is what makes this table total: it is why a replay pays **zero** kernel clones (§D.13) |
  | 6 | the **kernel** → a **store**, the `Records` slice itself | **yes** — every `Payload` inside it is row 3's array, which the kernel reads again at every later fold and every later append of the same change list (§UC-058, §D.3) | the **recipient**: a store reads the slice for the duration of the call and neither writes into it nor retains it past the return. A store that keeps rows for later takes its own slice, exactly as it clones a payload it retains |
  | 7 | a **store** → the kernel or a caller, the `[]Envelope` a read returns | **it must be neither**, for row 4's reason one level out | the **sender**: a fresh slice per call, whose backing array no later `ReadStream` or `ReadAll` reuses — because a `*Reader` hands that slice straight to the consumer and §INV-038 blesses fanning it out to workers |

  A caller's own payload value is not an eighth: it is consumed at `Fact.New` by
  being encoded, which is the inbound table's first row and not a hand-off of an
  array at all.
- **Why rows 4 and 7 are quantified over the recipient and not over the store's
  own future reads** *Memory the store will never read again* is satisfied by an
  array a **third party** will overwrite: the ordinary high-performance driver
  accessor whose byte slices are valid only until the next fetch, an `mmap`ed
  page, a scratch buffer a compression library owns. A store author reasoning *I
  retain nothing, so I clone nothing* is right about itself and wrong about the
  memory, and a consumer's retained page changes under it between two `Next`
  calls with no error and no way to notice. So the obligation is the recipient's
  freedom — **what a read returns belongs to whoever receives it, for as long as
  it keeps it and from whatever goroutine it reads it on** — and every party in
  the chain owes it, so a store that **borrows** its bytes copies before returning
  them. `crud/adapter/crudpgx` is already in this repository and phase 2 is
  PostgreSQL, so the borrowing store is the next one rather than a hypothesis.
- **Why row 3 is answered by a promise and the others by a copy** Because the
  alternative pays on every append on every store, most of it in the store that
  retains nothing, so the cost would fall exactly where the risk is not; §D.13
  prices both sides. The promise lands on the party that has the contract, §D.14
  being by its own opening sentence the whole of what a store may rely on, and it
  is what the standard library states where it has the same problem (`io.Writer`:
  an implementation "must not retain p" **and** "must not modify the slice data,
  even temporarily"). The `payload ownership` section's **inbound** half is what
  makes it exercised rather than written down.
- **The inbound domain, enumerated the same way.** A caller hands the framework a
  value at the calls below, and this table is all of them.

  | What the caller hands over | Where | What the framework does with it |
  |---|---|---|
  | a payload `E` | `Fact.New` | **encoded at once**; no value of it is retained, so a later mutation of the caller's copy changes nothing recorded (§UC-063) |
  | an identity `ID` | `Fact.New`, `Repo.Load`, `Aggregate.Fold` | handed to the **caller's own mapper**; the `Key` it returns is a string the framework keeps, and the `ID` is not retained (§INV-042) |
  | a state `S` | `Aggregate.Fold` | handed to the **caller's own folds**, in a loop the framework owns: **written through** for every reference kind it reaches, and the argument is consumed (§UC-041, §UC-068). This is the one row whose answer is not *read only* |
  | a `[]Change[S]`, an `At[S]`, a `Cursor` | `Append`, `Fold`, `Read` | read. A change is frozen bytes plus a stream, a token is scalars and strings, a cursor is a string; none is written and none is retained past the call |
  | a `context.Context` | every operation | read. `Within` returns a **derived** context and never mutates the one it was given (§UC-028) |
  | a `Store` or a `Log` | `Open`, `Read`, `ReadOnly` | **retained, by design** — it is the handle the composition root constructed and closes (§UC-006, §UC-047) |
  | a codec, a fold, an upcaster, a mapper | `Define`, `Declare`, `From`, `Then` | **retained, by design**, on the sealed declaration, and **called by the framework** — which is the mechanism the state row above is a consequence of (§UC-005) |

  **Four** of the seven rows are *read and not retained*; **two** are retentions
  the caller constructed for exactly that; and the **one** row where the
  framework's own loop writes the caller's memory is the state at `Fold`, which is
  a stated contract and §D.10's sixth residual rather than a leak.
- **Why leaving one hand-off out is the defect that keeps recurring** A codec that
  decodes a `[]byte` or string field by **aliasing** its input is ordinary — it is
  the shape a compact binary format takes, and `event.JSON` merely happens not to
  be one. Four consecutive rounds each left one hand-off out and each produced the
  same failure: the store's *freeze* wording let a consumer write into the memory
  store's own history (GAP-68); the kernel's missing clone let a blessed fold
  publish a `Change`'s frozen array into a state §UC-052 invites the caller to
  mutate (GAP-79); the undescribed kernel→store hand-off let a decorator rewrite
  an already-decided fact (GAP-87); and a rule stated over the payload array left
  out the slice that carries it, and asked about the store's future reads instead
  of the memory's lifetime (GAP-94). Every time, **the fact recorded was not the
  fact decided**, or the page read was not the page that was returned. The two
  alternatives to a uniform rule are rejected in §D.12: obliging a codec not to
  alias its input, which no third-party codec promises and no proxy falsifies, and
  declaring a fold's `E` borrowed, which §UC-065 forbids in terms. `bytes.Clone`
  at a decode boundary is what `jobs.invokeUpcaster` already does, so this is the
  tree's answer and not a second one.
- **What a caller may do with what it is handed** Anything. An `Envelope.Payload`,
  the `[]Envelope` a read returns, the `E` a fold receives, and any reference kind
  a codec aliased out of either into the application's state are the caller's: it
  may keep them, mutate them and publish them, and the framework will never look
  at them again. That is what the rule buys, and it is why the answer is one
  sentence rather than a list of which fields are borrowed.
- **What round 4 deleted here** The transitive copier walk over the application's
  state type, `event.Copy` and `eventtest.Copies`. It existed to protect a live
  accumulator the kernel kept and handed out repeatedly, and there is no such
  object; §UC-052 argues the deletion (GAP-42).
- **Falsified by** §UC-061 (mutate a handed-out state, reload disagrees, history
  intact — **including a state field a deliberately aliasing codec decoded out of
  an envelope's payload**); §UC-063 (mutate a payload after `New`, the fact is
  unchanged); §UC-065 **driven through the same aliasing codec**, which is the
  case that tells hand-off 2 apart from `event.JSON`'s good behaviour and which no
  store-side assertion reaches; §UC-068 for the inbound table's third row.
  Hand-offs **4 and 7** against a store that retains its payloads — which every
  in-memory store is: read a page, write into every `Envelope.Payload` byte, read
  the same page again and assert it is unchanged, then load the stream and assert
  the state is unchanged; **and hold page one, fetch page two, and assert page
  one's envelopes and its payload bytes are both unchanged**, which is the half a
  store that pools either slice fails. A store that only *froze* what it returned
  fails the first, and `eventmemory` is the store that decides both. Hand-offs
  **3 and 6**, which no hand-out assertion reaches: with a fixture that writes
  into every byte of every `Record.Payload` **after the forwarded call returns**,
  the appended change list and an identical list that never left the test must
  still fold to one state, and a reload must equal it (§D.9's `payload ownership`
  inbound half, and §UC-045's sixth defect). And a conformance decorator that
  returns one reused payload buffer **and one reused envelope slice** for every
  page, which the `payload ownership` section must fail.

#### INV-022 Every bound is declared, reachable and never escapable
- **Statement** Six bounds, each with exactly one meaning, each readable from any
  store value, and none with a spelling for "unbounded".

  | Bound | What it limits | Where it is read from | Zero means |
  |---|---|---|---|
  | `MaxPayload` | encoded payload bytes, on encode and on decode | `Limits()` | refused at `Bind` **and at `Read`** |
  | `MaxBatch` | changes in one append | `Limits()` | refused at `Bind` and at `Read` |
  | `MaxKey` | key bytes | `Limits()` | refused at `Bind` and at `Read` |
  | `StreamPage` | envelopes one stream page returns | `Limits()` | refused at `Bind` and at `Read` |
  | `MaxRead` | envelopes one global page returns | `Limits()` | refused at `Bind` and at `Read` |
  | declared-identifier length | family and wire type name bytes | a kernel constant | not applicable |

- **Why a zero in `Limits()` is refused rather than defaulted** A store's own
  spec may have zero-means-default fields — that is the store's affair and its
  constructor fills them. `Limits()` is what the **kernel** enforces on the
  store's behalf, so a zero there is a store that forgot to state a bound, and
  substituting a kernel default would silently enforce a number the store never
  chose. The kernel also refuses a limit **above** its own absolute ceiling, so a
  store cannot raise a bound the kernel holds for every deployment.
- **Where "refused" happens, and it is two doors rather than one** A store enters
  the kernel by exactly two calls, `Bind` and `Read`, and **both** perform the
  same **three** store-honesty checks — a valid `Backing()`, a `Limits()` with no
  zero field and none above the kernel ceiling, a `Capabilities()` with no
  unstated field. Three is the count everywhere it is stated (§UC-031, §UC-036,
  §UC-055, §D.4, §D.7); `Bind`'s fourth check is the family collision, which is
  not a store-honesty check and which a read cannot have. At `Bind` alone they are
  absent from the deployment that never binds: a projector holds a `Log` and calls
  `Read`, so a store answering `MaxRead: 0` reaches it unvalidated, honestly
  returns at most zero envelopes, and the drain terminates having read nothing
  (GAP-60). Two doors is the whole list; a third would owe the same checks.
- **Why the identifier cap is a kernel constant** A family and a wire type name
  must be legal in *every* store's identifier space, and they are the same in
  every deployment, so the kernel takes the narrow intersection and no store may
  widen or narrow it.
- **Falsified by** A test per bound, each with an at-the-bound control built from
  `store.Limits()` rather than from a literal, so the case is right for whichever
  store is under test; a store answering a zero limit refused at `Bind` **and** at
  `Read`; a store answering a limit above the ceiling refused at both.

#### INV-023 Encodability is the codec's question, answered before `main`
- **Statement** Whether a type can be encoded deterministically is answered by
  the **codec declared beside it**, never by a rule the kernel holds. The
  question is asked once per retained revision, at declaration, against a zero
  value of that revision's reader type.
- **Why the codec's placement is what makes this possible** With the codec on the
  declaration both halves of the question exist before `main`, so [[D-021]] is
  satisfied outright and `Bind` costs O(1); §D.2 prices what a store-owned codec
  cost instead.
- **Falsified by** A declaration whose codec rejects its reader type panicking at
  declaration; the same declaration with an accepting codec building; two codecs
  on two revisions of one fact, one accepted and one refused, asserting the
  refusal names the revision.

#### INV-024 The refusal vocabulary is a partition, and `errors.Is` never crosses a class
- **Statement** Every sentinel belongs to exactly one of the six classes in §2.3.
  `errors.Is` from a refusal of one class never reaches a sentinel of another.
  Within a class one sentinel may wrap another, and no claim is made about
  row-to-row distinguishability, because no caller needs one. Two closed lists
  complete it: **two intra-class wraps**, both into `ErrDeclaration`, and **four
  declared class wraps** out of the vocabulary into classes a transport already
  maps. A refusal produced from somebody else's error also carries that error as a
  **cause**, which is not a wrap of this vocabulary and cannot cross a class,
  because a store's or an application's error is not a sentinel of it — and a
  store's error must not be one, which is §2.3's one cause obligation.
- **Why a partition and not a matrix** §2.3's opening carries the argument.
- **Why the intra-class list has to be written down** The falsification needs a
  **pairwise ground truth** for all 22 × 21 ordered pairs. Cross-class pairs are
  free — always false — and intra-class pairs are not derivable from the partition
  alone, so §2.3 carries the closed list of two edges rather than leaving a test to
  assert only the half it can compute (GAP-62).
- **Falsified by** A table test that walks every sentinel and asserts, for each
  of the other twenty-one, that `errors.Is` answers **true** exactly when the pair
  appears in §2.3's intra-class list and **false** otherwise, so the negative half
  is computable rather than guessed; a test per declared class wrap asserting the
  wrapped class is reachable and was not replaced; the same table test run over
  refusals **built from a store's error**, asserting the answers are unchanged;
  and a store returning one of this vocabulary's sentinels as its own cause
  asserted to fail the `refusal classes` section (§2.3's cause obligation).
- **How the set grows** §2.3's three sentences, and they are not restated here.

#### INV-025 A refusal names declared identifiers and a classification, never data
- **Statement** No error this subsystem produces carries a stream key, a payload
  or a fragment of one, a version, a position, a cursor, an identity value, a
  driver's message text or a codec's message text. What it may name: the family,
  the wire type name, the revision, the rule that was broken and the sentinel's
  own classification.
- **The mechanism, not the rule** The wrapped cause stays reachable with
  `errors.Is` while its **text** does not travel — `cache/errors.go`'s
  `opaqueError`: `Error()` returns the category's text and `Is` still reaches the
  cause, under a bounded hop budget. It is the one shape `ErrBackend` and
  `ErrUpcast` need, and nothing else in the tree has it.
- **Falsified by** A table test over every sentinel's message asserting none of
  the forbidden values appears; an injected driver failure whose distinctive text
  must not appear in the rendered message while `errors.Is` still reaches it.

#### INV-026 A conflict carries no version
- **Statement** The conflict refusal does not report the stream's actual current
  version, and no accessor exposes it.
- **Why** Reporting it invites the one recovery [ES] forbids: re-appending the
  same proposed changes at the version the store named, which is a stale decision
  written at a fresh version. The correct recovery is a fresh load and a new
  decision (§UC-022, §UC-034), and the load reports the version anyway.
- **Falsified by** The refusal's message and its accessors; a test asserting no
  exported path from a conflict to a number.

#### INV-027 The framework never opens, commits or rolls back a transaction
- **Statement** No call in `event` begins a transaction, commits one or rolls one
  back. `Within` asks a question and returns a context; it does not create. A
  store's append runs on whatever executor the context resolved to — the caller's
  transaction or the store's own autocommit — and the caller's transaction is
  ended by the caller.
- **Why it is its own invariant** A framework that opens a transaction owns a
  resource the caller must release and has nowhere to hand it back; one that
  commits it decides that nothing else belongs in it. Both are the "the library
  owns your transaction" defect, and both are one convenience method away.
- **Falsified by** A source check for `Begin`, `Commit` and `Rollback` calls
  under `event/` outside a store's own internals; a recording executor asserting
  the exact statement sequence of an append under a caller transaction contains
  no begin and no commit.

#### INV-028 An authority **is** its transaction's identity, and forging one buys nothing
- **Statement** An `Authority` proves *which transaction*, not which datasource:
  two calls that resolve to one **live** transaction answer authorities that
  compare `Same`, and two **live** transactions on one backing answer authorities
  that do not. It is unserialisable — `MarshalJSON` refuses, which is
  `jobs.TransactionBinding`'s shape — and no accessor exposes anything inside it.
- **How it is derived** It holds the store's backing and the **transaction's own
  identity, by reference** — for a SQL store the `*sql.Tx` that
  `crudsql.Transaction(executor)` returned, for the memory store its own `*Tx`.
  `Same` is `Backing.Equal` and then `crud.SameDataSource` over the two
  identities, the same one call §INV-016 uses. Nothing is minted, nothing is
  memoised and nothing has to be evicted; §D.12 carries why the minted-token shape
  was wrong (GAP-64).
- **Why holding the identity is safe where deriving from it was not** A live
  `Authority` keeps its transaction **reachable**, so its address cannot be
  reused while the authority exists. Two authorities that are both alive therefore
  either name one transaction or hold two distinct live pointers; there is no
  third case, and the pointer-reuse hazard is prevented by the retention rather
  than by a rule. What is retained is a `*sql.Tx` header, not a connection: a
  committed `*sql.Tx` has already released its driver connection, so an authority
  outliving its transaction costs a few words and holds nothing open.
- **Two things this makes structural rather than remembered** A savepoint and its
  parent answer one authority, because `crudsql`'s savepoint returns the parent
  `*sql.Tx` and the identity is that pointer (§UC-049). And a store that "mints
  fresh entropy per call" is not a shape this contract admits at all, rather than
  one it fails a test for.
- **What is honestly forgeable, and why it does not matter** `NewAuthority` is
  exported, because a store in another package must construct one and §INV-019
  admits no kernel diff for it (GAP-44). A forged one has nowhere to go: the
  kernel never accepts an authority as an argument — it obtains one from
  `store.Transaction(ctx)` — so it is a value only its forger compares. **The
  unforgeability that carries weight is the at-token's** (§INV-015), and an
  at-token never crosses the store boundary in either direction, so no minting API
  exists or is needed for it.
- **Falsified by** Two appends in one transaction compared `Same`; two live
  transactions compared **not**-`Same`, which is what the `transactions` section's
  `Begin`-twice case asserts (§UC-049, GAP-71); **`NewAuthority(b, tx)` called twice over one identity
  compared `Same`**, which is the whole content of the savepoint claim and is
  assertable in `event`'s own tests without a savepoint existing, with `crudsql`'s
  savepoint-returns-the-parent's-`*sql.Tx` half asserted in `eventpg`'s package in
  phase 2 (§9); `MarshalJSON` asserted to return an error; `NewAuthority` asserted
  to refuse a non-comparable identity and one that is nil **by any route,
  including a typed nil pointer** (§INV-016, GAP-78), with a live `*sql.Tx`
  accepted in the same test; a store that mints fresh entropy per call failing the
  first case; and a source check that no store in this repository keeps a map keyed
  by a live transaction.

#### INV-029 Cancellation identity is preserved, and uncertainty outranks it
- **Statement** A context cancellation reaches the caller as itself:
  `context.Canceled` and `context.DeadlineExceeded` are not sentinels of this
  subsystem and are never wrapped by one. An unknown commit reaches the caller as
  `ErrUncertain`. When a cancellation happens **inside the commit window** the
  outcome is uncertainty, for §UC-057's asymmetry.
- **Falsified by** The ambiguous case constructed deliberately — a store that
  issues the write and is then cancelled — asserting `ErrUncertain`; the
  before-write control asserting the cancellation sentinel still matches; both
  driven through two opaque decorators in both orders.

#### INV-030 Every question the kernel asks a store is answered by the exact outer value
- **Statement** There is no capability discovery, no optional interface and no
  unwrap chain in `event`. All eight contract methods are required, so the value
  the composition root handed over is the value that answers — including a
  decorator. A decorator that does not forward is a defect in the decorator, and
  it cannot make the kernel skip it.
- **Why it is this shape** Discovering the transaction seam through a declared
  chain and returning an inner store from `Within` removes a policing decorator
  from the whole transactional path — the tunnel [[D-115]] was corrected to close
  and [ES]'s obligation 3 forbids (GAP-47). A required method plus a
  context-returning `Within` deletes the question instead of ruling on it.
- **Falsified by** A method inventory showing no optional interface in `event`; a
  refusing decorator asserted still in the path for every operation inside a
  transaction; the suite run through two opaque middleware in both orders.

#### INV-031 An empty append touches no store
- **Statement** An append of zero changes issues no statement, performs no
  version check, writes nothing, and returns the token that went in together with
  a receipt that answers **yes** to `Empty()`. Every accessor of that receipt
  answers rather than panics, and its authority is the invalid one — an append
  that wrote nothing was written through nobody's transaction, so it is atomic
  with nothing. The one check it still performs is the token's own key (§D.5 step
  1): every token a `Load` minted passes it, and refusing a forged one even when
  nothing would be written keeps §INV-015's first defence unconditional.
- **Falsified by** A recording store asserting zero calls; a receipt asserted
  empty with every accessor called; the northstar sketch's transaction path
  asserted to succeed on a no-op decision, which is the shape a caller copies.

#### INV-032 Close is idempotent, refuses nothing, and decides nothing about a transaction
- **Statement** `Close` returns nil the first time and every later time, refuses
  nothing including when a transaction is open, and **neither commits nor rolls
  back** staged work. What becomes of that work is decided by the caller's own
  transaction and by nothing else.
- **The two store shapes, and the one property common to them** §UC-047 names
  them; the property that holds for both, and the only one worth asserting on a
  section the suite runs `always`, is **invisibility**: after a close with an
  unresolved transaction, no other reader sees that work (GAP-69).
- **Falsified by** Two closes both returning nil; a close with staged
  transactional work asserting the work is **invisible to another reader**
  afterwards, paired with the control that work **committed before** the close
  *is* visible after it — without which "invisible" passes against a store that
  hides everything.

#### INV-033 A stream is (family, key) compared byte-exact, and injectivity is the application's
- **Statement** Two streams are the same stream when their family and key are
  byte-identical. No layer folds case, trims, normalises Unicode or truncates. A
  key and a declared identifier are legal text when non-empty, valid UTF-8, free
  of NUL and of control characters, and within their cap.
- **The obligation the framework cannot verify** The identity mapper must be
  **injective** over the aggregate's identity domain: two identities must never
  render to one key. The framework cannot check it in general, so it owes three
  things instead — the correct rendering must be the shortest one to write
  (`Compose`, which length-prefixes each part so no two distinct part lists
  render to one key), validation at the seam, and a **runnable proxy**
  (`eventtest.Keys`) so the obligation is testable rather than written down.
- **What the per-store key byte domain was replaced by** One kernel-wide text
  rule, covering keys and declared identifiers alike: a store whose column cannot
  hold that text cannot host this framework's data, which is a fact about that
  store and not a setting nobody could calibrate.
- **Falsified by** A conformance section writing two keys that differ only by a
  separator ambiguity and asserting two streams; the same for an NFC and an NFD
  spelling; a key with a NUL refused while the same key without it is accepted;
  `eventtest.Keys` failing on a concatenating mapper.

#### INV-034 The two reads describe one set
- **Statement** For every committed append, the events it wrote are returned by
  that stream's read, by the global read, and by **no other stream's** read.
- **The qualification, and it is not a loophole** With concurrent writers, and on
  a store with permitted position gaps, set equality is asserted over a
  **quiescent** store or up to the cursor the global read returned — because an
  event committed after the walk began is not yet anybody's obligation. What is
  never permitted is an event that is permanently visible to one read and not the
  other.
- **Why it is separate from ordering** An event that folds into an aggregate's
  state but never appears in the global read is a decision no projector, outbox
  or export ever sees: silent, permanent divergence between the write model and
  everything downstream, with perfect ordering.
- **Falsified by** A `conservation` conformance section that appends across
  several streams, then compares the union of the per-stream reads with the
  global read for **set equality over `(Stream, Version)`** — the event's derived
  name, so a store that reissued a position fails `global order` rather than this
  — and asserts no stream's read contains another stream's event.

#### INV-035 A resume point the read returned is safe to persist, on every store
- **Statement** Resuming a read from a cursor a store returned skips nothing, on
  every store the contract admits — including one that cannot promise monotone
  visibility. A cursor is store-minted and opaque; no arithmetic, ordering or
  derivation is defined on it, and no caller derives one from a position.
- **Falsified by** A `resumption` section, mandatory for **every** store, that
  reads a page, persists the cursor, has a writer commit **out of position
  order**, then resumes in a fresh reader and asserts nothing was skipped; and
  the same across a different store value over the same backing when
  `SharedBacking` is claimed, which is what a restart is. A store returning its
  newest position as a cursor while a lower one can still commit is one of the
  six defects the suite injects into itself (§UC-045).

#### INV-036 A walk tiles; delivery is at least once
- **Statement** One uninterrupted pass over a **quiescent** store returns every
  committed event once, in position order. That is a statement about a walk and
  never about delivery. **Delivery is at least once, consumers are idempotent,
  and no wording in this subsystem may use "exactly once" to describe delivery.**
- **Why it is an invariant and not a note** [ES] makes it a non-negotiable, and
  the neighbouring use case had promised the forbidden word (GAP-13). A reader
  who takes "reads tile the log exactly once" as a delivery guarantee stops
  writing the idempotency their consumer needs, which is how an exactly-once
  claim enters a system that cannot make one.
- **Falsified by** A grep of the finished document for "exactly once", asserting
  that **every** occurrence is a prohibition of the phrase and never a claim made
  with it — there are five, and they are §UC-039, §D.7, this row and its two
  explanatory sentences; the tiling section scoped to a quiescent store and named
  so.

#### INV-037 WITHDRAWN — the view's state machine
> The invariant enumerated five states of a live `View` and six transitions
> between them. There is no view: a state, an at-token and a receipt are three
> inert values, none of which remembers anything about an append. What an append
> does to the caller's next token is §INV-040, which is a table of **outcomes**
> rather than a state machine over a receiver, and §UC-023 is withdrawn with it.
> Citations to INV-037 resolve here and should be read as §INV-040.

#### INV-038 What is safe to copy, and what is safe to share
- **Statement**, per type, because one answer for all of them would be wrong for
  some:

  | Type | Copying it | Concurrent use |
  |---|---|---|
  | `At[S]` | free; it changes no answer the copy gives | safe; it is immutable data |
  | `Change[S]` | free; a copy shares the frozen bytes and the stream, and nothing writes either | **safe**: it retains no decoded value, and each fold decodes a **clone** of the frozen bytes, so two goroutines folding one change get two independent values even from a codec that decodes by aliasing — which is what makes the row true rather than true of `event.JSON` only (§INV-021, §UC-065) |
  | `Commit` | free | safe |
  | `Authority`, `Backing`, `Cursor` | free | safe |
  | `Envelope` | free; a struct of scalars, strings and one `[]byte` that no other party will ever write (§INV-021 hand-off 4) | safe to read; the payload is the caller's to mutate, so two goroutines sharing one and one of them writing is the caller's own race, like any slice |
  | `[]Envelope`, the page a read returns | free; the slice header is the caller's, and its backing array is never reused by a later read (§INV-021 hand-off 7) | safe: it is the caller's own memory from the moment it is returned, so it survives the next `Next` and may be fanned out |
  | the application's state `S` | the caller's business; the framework holds none of it | the caller's business |
  | `*Repo[S, ID]` | it is a handle; copy the pointer | **safe for many goroutines** |
  | `*Binding` | it is a handle | safe; its family set is written only by `Bind` |
  | `*Aggregate[S, ID]`, `*Fact[S, ID, E]` | they are handles; copy the pointer | **safe for many goroutines, and this is why the seal exists**: the tables are read-only from the first observation onward (§UC-005), so every request goroutine in the process reads them concurrently and nothing writes |
  | `*Reader` | it is a handle; a copy is the same reader | **one goroutine at a time** — it is the only stateful value in the caller-facing surface. `Next` advances it and `Events` serves what the last `Next` fetched, so the two must not run concurrently. The **page** is a different question and is the `[]Envelope` row: once returned it is the caller's, so fanning it out to workers is safe and stays safe across the next `Next` |
  | `Store` | the implementation's business | must be safe for many goroutines |

- **Why no value's row makes copying unsafe** A `*View` needed `stale := view` not
  to escape a poisoning bit. There is no bit and no view, so copying any
  caller-facing value changes no answer it gives; the handle rows say "copy the
  pointer" only because a copied handle *is* the same handle.
- **Why the handle rows, the envelope and its page are here** They are the values
  most likely to be shared and the table was silent about them (GAP-74, GAP-94).
  `*Reader` is also why §INV-021 does not rest on "the framework retains nothing":
  the reader keeps a page of envelopes from one `Next` to the next, and what makes
  that page and its payloads safe is §INV-021's hand-offs 7 and 4.
- **Falsified by** A `-race` test with many goroutines loading and appending
  across several aggregates through one repository, **and reading one `*Aggregate`
  and its `*Fact`s from all of them**; a test that copies a token, appends through
  the original, and asserts the copy now conflicts — because it names a version the
  stream has moved past, which is the store's own check and not a bit anybody
  flipped; and a `-race` test folding one `Change` from two
  goroutines at once **through an aliasing codec**, which is the row that fails if
  a `Change` ever retains a decoded value again or if the kernel stops cloning the
  frozen bytes (§UC-065, §INV-021).

#### INV-039 One family names one aggregate, forever
- **Statement** A stream family names exactly one aggregate — across processes,
  deployments and binaries, not only within one program. Two aggregates sharing a
  family share a history: both load each other's facts, both fold them, both
  append at versions derived from the other's, and there is no error at any point.
  It is byte for byte §INV-033's corruption reached by the route §INV-033's scope
  excludes, and the two are read together.
- **What the framework can check** The reachable route — a declaration copied to
  start a second one with the family string unchanged, both bound **through one
  `Binding`** at the composition root — is refused by `Bind` (§UC-059). The set of
  bound families lives on that `Binding` value, which is per-value state, not the
  package-level registry §INV-013 forbids.
- **What it cannot, both halves** (1) Two equal families in two processes or two
  deployments that never link together. (2) **Two equal families in one process,
  bound through two `Binding` values** — including two store values over one
  backing, which every other rule in this document treats as one store (§UC-054's
  stated exception). `Open` may be called any number of times and no per-value set
  can see a second value of itself; the only wider scope is the registry §INV-013
  forbids, which would make declaration order matter, make two tests interfere,
  and make an import side effect load-bearing (GAP-59).
- **What is offered instead of pretending** The obligation stated with the same
  seriousness as injectivity; the scope stated on the value that has it (§UC-006's
  one-`Binding`-per-store obligation); a conformance section that **drives the
  escape and asserts it is not prevented**, so silence cannot be read as a
  guarantee; and the runnable proxy that is not scoped, `eventtest.Families`, over
  every declaration an application composes whichever store each is bound to.
- **Falsified by** Two different declarations with one family refused at the
  second `Bind`; two families accepted; **the same** declaration bound twice
  accepted, because that is one family, one table and one history and refusing it
  would fail the ordinary composition root that binds per feature module; the two
  `Binding` escape asserted to be **admitted**, which is the control that keeps
  this invariant's two halves honest; `eventtest.Families` failing on a duplicated
  family.

### 5.3 Rounds 4, 5 and 6's invariants

#### INV-040 An append has outcomes, and each says what the caller's next token is
- **Statement** There is no receiver that remembers an append. There is a table
  of outcomes, and it is total: every outcome of `Append` puts the caller in
  exactly one row.

  | Outcome | The caller's next token | Why |
  |---|---|---|
  | success, n ≥ 1 changes | the **returned** token, at version + n | The store admitted it at the token's version, so the new version is arithmetic and needs no re-read |
  | success, zero changes | the token that went in, unchanged | Nothing was written and no version check was performed (§INV-031) |
  | `ErrConflict` | unchanged, and now stale | The store's expected-version check refused it. Every further append through it conflicts, which is the correct answer and needs no kernel bit |
  | a pre-store refusal — key, encode, too-large, backing, **stream** (§UC-064), any transaction refusal | unchanged | Nothing reached the backing |
  | `ErrClosed` | unchanged | The store refused rather than tried |
  | cancellation before any statement | unchanged | Nothing was issued (§UC-057) |
  | `ErrBackend` (certainly not written) | unchanged | The store classified it, and only the store can |
  | `ErrUncertain` | unchanged in value, untrustworthy in meaning | The stream may be at the token's version or beyond; §UC-034's single retry is what resolves it |

- **What this deleted** `ErrStaleView` and view poisoning. A stale token conflicts
  because the store's real check says so, not because the kernel flipped a bit:
  one fewer sentinel, an identical caller obligation, and a refusal from the party
  that knows.
- **Falsified by** A conformance section driving each row and asserting the
  token's version afterwards; the success row as its control.

#### INV-041 The transaction rule, and it is [[D-118]]'s
- **Statement** Three answers, decided by what the store finds in the context,
  and there is no fourth:

  | What the store finds for its own backing | What happens |
  |---|---|
  | nothing | the operation runs on the store's own autocommit |
  | a transaction | the operation runs **inside it**, whether or not `Within` was called (§UC-046) |
  | an executor that is not a transaction | the operation **refuses before any statement**, never falling back to autocommit |

  On top of those, one refusal that exists only because `Within` was called:
  a context marked for authority T1 whose store now answers T2 refuses
  (§UC-049).
- **What the table is about, and what it cannot be about** Every row is keyed on
  what the store **finds bound for its own backing**, because that is the only
  thing any layer can look at. It says nothing about a transaction that is open
  and not bound, or open over another backing: for this store those are the first
  row, indistinguishable from no transaction at all, and there is no fourth answer
  to add for a fact nothing can observe (§UC-066, GAP-56). The mechanism that
  makes that case loud is `Within`, which the caller opts into and which refuses
  before any statement; the obligation to call it when atomicity matters is the
  application's, and §UC-030's guarantee is stated over the precondition rather
  than unconditionally.
- **Why this is stated as an invariant** Because rounds 1–3 specified the second
  row as a **refusal**, while [[D-118]] had already decided it the other way for
  the sibling subsystem and named the failure it prevents in the same words this
  document had used to justify the opposite: the escape is
  autocommit-while-a-transaction-is-open, and joining is what prevents it. **Two
  subsystems answering one question two ways is itself the defect**, and a future
  owner who wants the refusal back needs a decision doc superseding [[D-118]] for
  both, not a paragraph here.
- **Falsified by** A `transactions` section driving all three rows plus the
  mismatch, each asserting where the events landed; the autocommit row as the
  control; the unbound-transaction case of §UC-066 asserting the events **survive
  a rollback**, which pins what the framework does not see; and a source check
  that no path in `event` writes on autocommit after a store answered a valid
  authority.

#### INV-042 The kernel retains no application value across a call boundary
- **Statement** No type in `event`, `eventmemory` or `eventtest` keeps a value of
  an application's state type, payload type **or identity type** after the call
  that produced it returns. `Load` folds a private accumulator and
  returns it by value once. `Append` never receives a state at all. A `Change`
  holds **encoded bytes, the stream it was decided for, and an adapter** — and no
  application value of any kind: not the payload the caller passed to `Fact.New`,
  not a value decoded back from the bytes, and not the `ID` either. A stream holds
  the *rendering* the mapper produced, which is a `string` the framework made.
- **The checkable property, stated correctly** It is **not** "no type has a field
  of type `S`" — a repository is `Repo[S, ID]` and a token is `At[S]`, and
  parameterisation is what makes §INV-020's compile errors possible. It is: **no
  type in `event` retains an `S`, an `E` or an `ID` across a call boundary.**
  `At[S]` carries a stream, a version and a backing and no `S` at all; the
  parameter is there for the type system, not for storage.
- **Retention is not the same question as mutation** `Aggregate.Fold` writes
  through the state it is handed, for the duration of the call, and retains
  nothing of it afterwards — so this invariant holds unchanged and is not the one
  that answers §UC-041's contract. §INV-021's inbound table is where the two
  questions are separated.
- **Why a `Change` retains no decoded value** A retained one made this invariant
  false of its own design and its source check one a correct implementation must
  fail (GAP-55); it was also handed to the fold, so `s.Lines = e.Lines` published a
  path from the caller's state into a value the framework held. Deleting it costs
  one decode where a fold runs and saves one everywhere a change is decided and
  never folded, which is the ordinary path.
- **What the adapter may close over, since it is the one field that could cheat**
  The declaration, and nothing else: the fact's codec for this revision and the
  fact's fold. A closure that captured the `payload E` passed to `Fact.New` would
  retain an `E` under a different name, and that is precisely what the source
  check below has to catch — a check that looked only at named fields of type `E`
  would miss it.
- **Why it is the load-bearing invariant of round 4** Everything the copier walk
  was protecting follows from it: if the kernel holds none of the caller's
  memory, a decision that mutates its own state in place mutates only the
  caller's value, the framework has nothing to be corrupted, and a reload is the
  authority. It is also what makes a 100k-event aggregate cost one fold and no
  retention (§D.13).
- **Falsified by** A source check over `event` asserting that no struct field is
  assigned from a parameter of type `S`, `E` or `ID`, **and that no function
  literal stored in a field captures one** — which is satisfiable by the design as
  it now stands and was not by round 4's; §UC-061; §UC-063; §UC-065; and a heap
  check that folding a 100k-event stream retains one state and no envelopes.

#### INV-043 An unstated capability is refused
- **Statement** Every capability whose false value would skip a conformance
  section is a **tri-state** whose zero value is `Unstated`, and **`Bind` and
  `Read`** refuse a store that answers `Unstated` for any of them. A store must
  not be able to skip a section by forgetting a field, and a deployment that only
  reads must not be the one deployment where the check does not run (§INV-022's
  two doors, GAP-60).
- **Why a boolean was not enough** Its zero is `false`, indistinguishable from a
  deliberate "this store does not do that", and the suite's anti-vacuity design
  rests on telling *unclaimed* from *not stated*. It generalises: a field added to
  `Capabilities` after phase 1 cannot silently default to "skip that section".
- **Falsified by** A store answering the zero `Capabilities` refused at `Bind`
  **and at `Read`**; a store answering `Unsupported` for one field bound
  successfully with that section reported not certified; a store answering
  `Supported` and supplying no hook **failing** (§UC-043's first anti-vacuity
  rule).

#### INV-044 A change names its stream, and an append is one stream's
- **Statement** Every `Change[S]` carries the `Stream` it was decided for,
  rendered by the aggregate's declared mapper from the identity `Fact.New` was
  given. **The two calls that can cross two instances both compare, and they
  compare the same thing.** An `Append` admits a change list if and only if
  **every** change's stream equals the at-token's; a `Fold` applies a change list
  if and only if every change's stream equals the one the aggregate's mapper
  renders from the identity `Fold` was given. Both compare before doing anything
  else, and `Append`'s comparison happens before any statement and before the store
  is reached.
- **Why it is an invariant and not a note** A token and a change list are both
  typed by the **state type**, never by the aggregate and never by the instance,
  so two instances of one aggregate — and two aggregates over one state type —
  are one Go type and §INV-020's compile error is unavailable. Without
  this comparison, `repo.Append(ctx, toAt, debitChanges...)` compiles, is admitted
  at the token's version, and records one account's facts in another's stream with
  every other check passing — the backing matches, the version matches, the wire
  types are declared, the fold accepts them (GAP-54). That is silent permanent
  corruption of the one thing the phase exists to make trustworthy, on the
  canonical two-instance operation.
- **Why the `Fold` half needs the identity** The only check available without one
  — *all the changes name the same stream* — passes on a list that is entirely the
  other instance's (GAP-70). `accounts.Fold(fromState, toChanges...)` then
  advances one instance's state with another's facts; the caller decides again on
  that state, names this instance everywhere else, and every comparison in the
  first half of this invariant passes while the history gains a fact decided from
  another account's state. The identity subsumes the weaker check — a two-stream
  list has at least one change that is not `id`'s — and costs the caller nothing
  it does not already hold.
- **What it does not claim** Anything about a value that carries no stream. The
  two residuals — a decision taken in the wrong instance's own vocabulary, and a
  **state** that came from another instance's load — are separated in §UC-064 and
  listed in §D.10. What it closes is the mechanical pairing error between two
  values the framework minted: a token and a change list, an identity and a change
  list.
- **Falsified by** An `expected version` conformance case appending a change
  minted for one stream through a token for another and asserting `ErrWrongStream`
  with zero events written; §UC-064's correct transfer as the control; a `Fold`
  given one instance's identity and another's change list asserting the same
  sentinel, **and a mixed list with one foreign change asserting it too**, so the
  comparison is per change; the same list folded with its own identity as that
  case's control; and a source check
  that `Append` performs the comparison before every other check that touches a
  store.

#### INV-045 A store classifies its own failure, and the kernel maps that classification and nothing else
- **Statement** Every error that crosses the store boundary in the store's
  direction carries exactly one of §D.14's `Outcome` values or none, and the
  kernel's map from outcome to refusal row is **total, fixed and the only thing it
  reads**. The kernel never inspects a store's error text, never re-reads a stream
  to work out whether an append conflicted, and never infers a classification from
  a driver code it does not import. A store adds no sentinel to §2.3 and needs no
  `event`-internal access to say any of it (§INV-019).
- **What the store owns and what the kernel derives** The store owns the five
  answers only it can give: *conflict*, *certainly not written*, *issued and never
  confirmed*, *this cursor is not mine*, *I am closed*. The kernel owns everything
  downstream: which sentinel, which class, which framework class is wrapped,
  whether the store's own error stays reachable while its text does not
  (§INV-025), and what an unclassified error means at each door.
- **What the door decides, and it is exactly one thing** What an **unclassified**
  error means, because the safe guess differs between a write and a read; §D.14
  carries the map, the four doors and each one's default, and §UC-060 carries why
  the append's default is uncertainty and the read's is not. A **classified**
  outcome is mapped the same way at every door, because the store is the only
  party that knows what happened and a kernel that filtered a classification by
  door would be inferring the thing this invariant forbids it to infer. The kernel
  finds the outcome with **`errors.As`** and never by a type assertion, so a
  decorator that forwards and wraps keeps its classification; §D.14 carries that
  argument once, beside the map it is about. A bare `context.Canceled` or
  `context.DeadlineExceeded`, not wrapped in a `Failure`, overrides every default
  and travels as itself (§UC-057's first window, §INV-029).
- **What it does not claim** That a store classifies *honestly*. Nothing catches a
  store that says *not written* about a write it issued; those are lies, not
  contract violations the kernel can see. What this invariant makes impossible is
  two stores spelling one classification two ways, or a store having no way to
  spell it at all (GAP-67).
- **Falsified by** A `store failure classification` section driving each outcome
  through the `Fail` hook and asserting the mapped sentinel — `NotWritten` →
  `ErrBackend` with zero events, `Unconfirmed` → `ErrUncertain`, `Conflict` →
  `ErrConflict`, `Closed` → `ErrClosed`, `BadCursor` → `ErrCursor`; an
  **unclassified** failure injected at `Append` asserting `ErrUncertain` and the
  same one injected at `ReadAll` asserting `ErrBackend`, which is the pair that
  pins the door-dependent default; a **closed** store asserted to answer `Load`
  with `ErrClosed` rather than `ErrAmbientNotTransaction`, which is the fourth
  door's clause; every mapped outcome driven again through a decorator that
  **wraps** the store's error, asserting the sentinel is unchanged, which is the
  case a type assertion fails; a bare cancellation from a store asserting the
  cancellation sentinel still matches (§INV-029); the store's own error asserted
  reachable **through `event.CauseOf` and by neither `errors.Is` nor
  `errors.As`** through every mapped sentinel — superseded from "reachable with
  `errors.Is`" by [PLAN] S1 and S6 ([[D-122]], GAP-135), because a cause carrying
  an ordinary driver class would set the caller's transport status and an
  uncertain commit would render as retryable — while its distinctive text is
  asserted absent from the rendered message (§INV-025); the uninjected
  append succeeding as the control; and — §INV-019's half — the trivial store in
  `eventtest`'s own test package returning a classified failure of each kind with
  no `event`-internal access, which it can because `Outcome` and `Failure` are
  exported.

---

## 6. The DX sketch

> **The reading rule from the top of this document applies hardest here.** Every
> name below is a placeholder chosen to communicate shape and obligation. What is
> normative is the *shape*: what a caller holds, what can fail where, what cannot
> be expressed at all, and what a store implementer must write. Receivers are
> spelled `this`, which is the tree's dominant receiver by a factor of two and a
> half.

### D.0 The whole thing, end to end

An application's own domain package, then a composition root, then one operation.
Nothing else. This is the shape a consumer copies, so every line in it is a line
this document is prepared to defend.

```go
// ─── the application's domain package ──────────────────────────────────────

type Account struct {
    Open     bool
    Currency string
    Balance  int64
    Applied  map[string]bool
    OpenedAt time.Time            // reaches a *time.Location; costs nothing
}

type Opened     struct{ Owner, Currency string }
type CreditedV1 struct{ Amount int64 }
type Credited   struct{ Minor int64; Currency, Command string }

var accounts = event.Define[Account]("accounts.account",
    func(id AccountID) event.Key { return event.Compose(string(id)) })

var opened = event.Declare(accounts, "accounts.opened",
    event.From(event.JSON[Opened]()),
    func(this Account, e Opened) Account {
        this.Open, this.Currency = true, e.Currency
        return this
    })

var credited = event.Declare(accounts, "accounts.credited",
    event.Then(event.From(event.JSON[CreditedV1]()), event.JSON[Credited](), toCreditedV2),
    func(this Account, e Credited) Account {
        if this.Applied == nil {
            this.Applied = map[string]bool{}
        }
        if this.Applied[e.Command] {
            return this
        }
        this.Applied[e.Command] = true
        this.Balance += e.Minor
        return this
    })

func toCreditedV2(old CreditedV1) (Credited, error) {
    return Credited{Minor: old.Amount, Currency: "EUR"}, nil
}

// ─── the decision: a method on the state, taking the aggregate's own id ────

func (this Account) Credit(id AccountID, command string, minor int64) ([]event.Change[Account], error) {
    switch {
    case !this.Open:
        return nil, ErrAccountClosed
    case minor <= 0:
        return nil, ErrAmountNotPositive
    case this.Applied[command]:
        return nil, nil // already applied: no facts, a legal no-op
    }
    return []event.Change[Account]{
        credited.New(id, Credited{Minor: minor, Currency: this.Currency, Command: command}),
    }, nil
}

// ─── the composition root ──────────────────────────────────────────────────

log, err := eventmemory.NewLog(eventmemory.LogSpec{})
if err != nil {
    return err
}
store, err := eventmemory.New(eventmemory.Spec{Log: log, Clock: clock.Now})
if err != nil {
    return err
}
defer store.Close()

accountRepo, err := event.Bind(event.Open(store), accounts)
if err != nil {
    return err
}

// ─── the app-usecase: four lines, no version, no store, no copier ──────────

state, at, err := accountRepo.Load(ctx, cmd.Account)
if err != nil {
    return err
}
changes, err := state.Credit(cmd.Account, cmd.ID, cmd.Minor)
if err != nil {
    return err
}
_, _, err = accountRepo.Append(ctx, at, changes...)
return err
```

**What is not in that listing, and each absence is a decision.** No version. No
store in the usecase. No copier and no option on the state type, although
`Account` holds a map and a `time.Time`. No registry. No `init`. No mock. No
transaction, because this operation does not need one — and §D.6 shows that the
one that does adds two lines to the *composition root*, not to the usecase.

**What *is* in it that is easy to miss: the identity, in two places.** The
decision takes `cmd.Account`, and so does the fact handle that mints the change —
the application's own type, and a value the caller already holds (§UC-064).
**Why `Append` returns three values and there is one way to call it** is
§UC-019's; the advanced token is discarded here because this operation decides
once, and §D.5 is where it is not.

### D.1 The aggregate, and the mapper that is the one dangerous line

```go
func Define[S any, ID any](family string, key func(ID) Key) *Aggregate[S, ID]
func Compose(parts ...string) Key

func (this *Aggregate[S, ID]) Family() string

// Runs the caller's own folds over the caller's own value: `state` is CONSUMED,
// written through for every reference kind it reaches. Take the result (UC-041).
func (this *Aggregate[S, ID]) Fold(id ID, state S, changes ...Change[S]) (S, error)

// The one non-generic view of a declaration, with an unexported marker method so
// that only *Aggregate implements it. It has exactly two readers in phase 1 —
// eventtest.Families and the Binding's bound-family set — and no third is
// invented for it.
type Declaration interface {
    Family() string
    // contains an unexported method
}
```

`Define[Account](…)` names `S` explicitly and infers `ID` from the mapper — a
partial type-argument list with the explicit parameter first, which is exactly
what Go allows.

**The mapper is the one place an application can corrupt its own history in
silence**, and the framework's job is to make the correct rendering the shortest
one to write.

```go
// wrong, and nothing anywhere will tell you
func(id OrderID) event.Key { return event.Key(id.Tenant + id.Number) }
//  {tenant:"ab", number:"c1"} and {tenant:"a", number:"bc1"} are one stream

// right, and shorter
func(id OrderID) event.Key { return event.Compose(id.Tenant, id.Number) }
```

`Compose` length-prefixes each part, so no two distinct part lists render to one
key. A part that is not text is hex-encoded in one line before composing
(§INV-033).

**The rendering is a wire format.** Once a stream has been written, that
identity's rendering may never change (§INV-005). `event.Key(id)` and
`event.Compose(id)` are **not** interchangeable after the first append — the
composing helper length-prefixes and the conversion does not — so the advice is
to take `Compose` on day one even for a single-part identity. The failure is
quiet: every aggregate reads as version 0 with a zero state, which §UC-009
deliberately makes indistinguishable from a fresh install.

**The runnable proxy**, because an obligation with no proxy is one nobody
discovers breaking:

```go
eventtest.Keys(t, accounts, id1, id2, id3, …)      // asserts distinct keys
eventtest.Families(t, accounts, orders, invoices)  // asserts distinct families
```

### D.2 The reader chain, and where the codec lives

```go
type Codec[V any] interface {
    Encode(V) ([]byte, error)
    Decode([]byte) (V, error)
    CanEncode() error
}

func JSON[V any]() Codec[V]

func From[V any](codec Codec[V]) Chain[V]
func Then[A, B any](prev Chain[A], codec Codec[B], up func(A) (B, error)) Chain[B]

func Declare[S, ID, E any](
    a *Aggregate[S, ID], name string, chain Chain[E], fold func(S, E) S,
) *Fact[S, ID, E]
```

A `*Fact` carries the aggregate's `ID` because minting a change needs the mapper:
that is what lets a `Change[S]` name its stream (§D.3, §INV-044). `Change[S]`
stays parameterised by the state type alone, so a fold and an append see one type
regardless of which fact produced the change — and, for the same reason, two
aggregates that happen to share a state type share every caller-seam type, which
is the scope §INV-020 states and the stream comparison covers.

A chain's **position is the revision**: `From` is revision 1, each `Then` is the
next. There is no revision number to type, so there is none to typo, duplicate or
leave a gap in (§INV-010). `Declare` infers `S` and `ID` from the aggregate and
`E` from the chain, so no type argument is written at the call site.

**The codec belongs to the data, and the tree already says so.**
`jobs.Codec[P]` is per definition and `jobs.Upcast(from Codec[A], to Codec[B],
fn)` carries one codec per hop. A store-owned codec cost three things that all go
away here: a `Store.Codec()` method existing only so `Bind` could interrogate a
codec through the store contract; an encodability check at `Bind` — start-up but
not *before* `main`, and an O(retained readers) walk on the request path of
§UC-055's per-request store (GAP-35), where it is now once per process and `Bind`
is O(1); and one codec serving every revision, which made "decode revision 1's
bytes with revision 2's codec" inexpressible as a defect.

**What moving the codec costs a deployment, and it is not free.**

1. **A store may assume nothing about the payload bytes.** They are opaque to it
   in a way they were not when it chose the codec, so `MaxPayload` is a **byte**
   bound applied after encode and never a "field count" or a "depth" the store
   could have known.
2. **A team that wanted JSON in development and a compact binary format in
   production now performs a data migration instead of changing one spec field.**
   That is a real loss and it is the price of the check running before `main` in
   every deployment. The exchange is deliberate: the deployment-varying codec was
   a knob whose wrong setting is discovered by a decode failure in production,
   and the check it made impossible catches the same class of defect before the
   process starts.
3. **Two store values over one backing can no longer disagree about the codec**,
   because they have none — which deletes two of the four things §UC-054 required
   them to agree on.

**A chain always starts at revision 1.** Retention — declaring that the earliest
revisions are no longer readable — brings its own constructor, its own refusal
and its own tests, and adding it is additive (non-goal 13). What phase 1 must not
do is export a floor nobody tests: the floor would be the only number in the
whole design a hand types, an off-by-one in it shifts every revision in the
chain, and the misdecode that follows is **silent** — revision n+1's payload is
usually n's plus a field, so decoding it into the older struct succeeds and drops
the field.

### D.3 The decision, and `Fact.New`

```go
func (this *Fact[S, ID, E]) New(id ID, payload E) Change[S]

func (this Change[S]) Stream() Stream
func (this Change[S]) Err() error
```

`New` takes the **aggregate's own identity type**, renders it through the
declared mapper, and **encodes at the moment of decision** with that fact's
current revision's codec. What the `Change` keeps is exactly three things: the
frozen bytes, the stream, and an adapter that decodes those bytes and applies
that fact's fold. It keeps **no value of the payload type at all** — not the
caller's, and not one decoded back from the bytes. Four consequences:

- **Mutating the payload after `New` cannot change the recorded fact** (§UC-063).
  The inbound aliasing direction is unrepresentable rather than forbidden, which
  is the strongest form the answer can take: deferring the encode to `Append`
  instead left the rule a sentence nothing enforced (GAP-43).
- **The value a fold receives is produced by the fold**, decoded when the fold
  runs from a **clone** of the frozen bytes, so a fold written `s.Lines = e.Lines`
  aliases a value the framework drops the instant it returns — true even of a
  codec that decodes by aliasing its input, which is why the clone is there — and
  folding one change twice gives two independent values. **The frozen array itself
  is written by nobody**. Its readers are the cloner in `Fold`, `Append`, and —
  through `Record.Payload` — the store and any decorator forwarding the request,
  under the one hand-off §INV-021 answers with the recipient's promise not to
  write rather than with a copy. Every reader and no writer is what makes copying
  a `Change` and folding it from two goroutines safe (§INV-021, §INV-038); a
  retained *decoded* value would falsify §INV-042 and open a path from the
  caller's state back into a framework-held one (§UC-065, GAP-55, GAP-79).
- **A change knows which stream it belongs to**, which is what makes
  `repo.Append(ctx, toAt, debitChanges...)` a deterministic refusal rather than
  silent corruption (§UC-064, §INV-044). The adapter closes over the
  **declaration** and nothing else; a closure capturing the payload would retain
  an `E` under a different name and is what §INV-042's source check must catch.
- **It costs one encode at decision time, and one clone plus one decode wherever a
  fold runs.** The ordinary operation — load, decide, append, never fold — pays
  neither. Priced in §D.13.

`New` returns **one** value so a slice literal of changes stays writable. A
failure — a non-finite float, a custom marshaller that errors, a payload over the
**kernel's absolute ceiling**, a mapper output that is empty or breaks the kernel
text rule — is carried on the `Change`. `Change.Err()` exposes
it for a caller that wants to fail at decision time; `Append` surfaces it before
the store is reached; `Fold` surfaces it rather than folding nothing, which is
why `Fold` returns an error at all (§UC-024, §UC-051).

**One consequence of decoding at fold time, named rather than left to be found.**
A codec that can encode a value and cannot decode its own output fails at the
first fold rather than at `New`. That is a broken codec, `eventtest.RoundTrip` is
the required helper that catches it (§UC-042), so the exchange is a defect
surfacing in a test instead of a decode running twice on every decision.

Two byte bounds in two places, and the split is not an accident. The **kernel's
ceiling** is a constant applied at `New`, because no store is known there and an
unbounded encode before any store exists would be a hole. The **store's**
`Limits().MaxPayload` is applied at `Append`, the first moment the store is
known, and it is never above the ceiling.

A `Change` is an ordinary value. A successful append does not consume it, and
appending the same slice twice records the facts twice — which is legal
(§UC-058) and which the framework cannot tell from the accidental case
(§D.10).

### D.4 The composition root

```go
func Open(store Store) *Binding
func Bind[S, ID any](b *Binding, a *Aggregate[S, ID]) (*Repo[S, ID], error)
```

`Open` starts nothing and cannot fail. `Bind` returns an error, and so does a
store constructor, for §UC-006's reason. **Every sketch in this document obeys
that**, including the
conformance factory's (§D.9) — round 3 wrote `store := eventmemory.New(…)` in
four places while requiring `New` to refuse a disagreeing shared backing, which
is a signature that cannot exist (GAP-50).

**What `Bind` costs, so an implementer can be held to it.** Four constant-time
checks and one allocation:

| Check | Refusal |
|---|---|
| this family is not already bound through this `Binding` to a *different* declaration | `ErrFamily` (§UC-059) |
| the store's `Backing()` is valid | `ErrWrongStore` |
| the store's `Limits()` has no zero field and none above the kernel ceiling | `ErrWrongStore` (§INV-022) |
| the store's `Capabilities()` has no `Unstated` field | `ErrWrongStore` (§INV-043) |

No codec walk, no reflection, no lock, no state shared between requests — which
is what makes §UC-055's per-request store ordinary.

**The last three of those are store-honesty checks, and they run at both doors** —
`Bind` and `Read`, for §INV-022's reason. The family check is `Bind`'s alone,
because a read binds no declaration.

**One `Binding` per store value, and it is an obligation**, because the
bound-family set lives on the value `Open` returns. What that scopes, what escapes
it and what is offered instead are §INV-039's.

`eventmemory`'s spec, with the log separate so a shared backing is expressible at
all:

```go
type LogSpec struct {
    MaxPayload int   // zero means the store's default
    MaxKey     int
}
type Spec struct {
    Log        *Log            // required; two stores over one Log are one store
    Clock      func() time.Time
    MaxBatch   int
    StreamPage int
    MaxRead    int
}

func NewLog(spec LogSpec) (*Log, error)
func New(spec Spec) (*Store, error)
func WithTransaction(ctx context.Context, tx *Tx) context.Context
```

The two **data-bearing** numbers live on the `Log`, not the `Store`, which makes
§UC-054's agreement requirement structural rather than a check: two store values
over one log cannot disagree about `MaxPayload` or `MaxKey` because neither owns
them. The operational numbers stay on the store, where two values may differ.

### D.5 Load, decide, append

> Round 3's §D.5 was the `View`'s five-state table. The view is deleted and so is
> the table; what an append does to the caller's next token is §INV-040, which is
> a table of outcomes rather than a state machine over a receiver.

```go
func (this *Repo[S, ID]) Load(ctx context.Context, id ID) (S, At[S], error)
func (this *Repo[S, ID]) Append(ctx context.Context, at At[S], changes ...Change[S]) (At[S], Commit, error)

func (this At[S]) Stream() Stream    // what every change in an append is compared against
func (this At[S]) Version() Version

func (this Commit) Empty() bool
func (this Commit) Stream() Stream
func (this Commit) First() Version   // the first version this append wrote; 0 when empty
func (this Commit) Last() Version    // the version the stream is at afterwards
func (this Commit) Count() int
func (this Commit) Authority() Authority
```

Three values, three jobs, and none of them can lie. The **state** is the
application's, folded once on the way out and never seen again by the framework.
The **token** is (stream, version, backing) and is inert: copying it changes no
answer it gives. The **receipt** is assembled by the kernel from what it already
knows — the store's `Append` returns only an error — so nothing sealed crosses
the store boundary in the store's direction.

**`Append`'s order, and it is fixed because two of these refusals are in two
classes.** (1) The token's own key against the kernel's text rules — a token that
is not a token makes every later comparison meaningless, and this is the check
that refuses a forged `At[S]{}` before the empty-append short circuit and before
anything else (`ErrKey`, §UC-051, §INV-015). (2) Every change's stream against
the token's (`ErrWrongStream`). (3) Each change's own carried refusal. (4) The
store's bounds. (5) The backing. (6) The transaction question. Nothing reaches
the store until all six have passed.

**`Load`'s order, stated here too because two sections ordered it differently.**
(1) The identity through the declared mapper, and the key it renders against the
kernel's text rules and the store's `MaxKey` — reading `Limits()` issues nothing,
so this is still *before the store is reached* (§UC-051). (2)
`store.Transaction(ctx)`, §INV-041's three-answer question. (3) The paged
`ReadStream`. So a `Load` with a **zero identity against a closed store** is
`ErrKey` and not `ErrClosed` — the input neither §D.9's `lifecycle` row nor
§UC-051 constructed while disagreeing about which came first.

Step (2) of the append is the one check a type cannot do for us: `At[Account]` and
`[]Change[Account]` are the same types for every account — and for every
aggregate declared over `Account` — so a transfer's two tokens are two adjacent
variables of one type and swapping them compiles (§UC-064, §INV-020, §INV-044).

`First`, `Last` and `Count` replace a single `Version()` whose contract drifted
between two meanings (GAP-53); three accessors with one meaning each cannot.
`Positions()` and `Durability()` are deleted (§D.12, non-goal 20).

**Two decisions in one operation** — the one place the caller does work the
framework used to hide:

```go
state, at, err := repo.Load(ctx, id)
first, err := state.Open(id, cmd)
at, _, err = repo.Append(ctx, at, first...)

state, err = accounts.Fold(id, state, first...)   // no store; `state` is consumed
second, err := state.Credit(id, cmd.ID, cmd.Minor)
_, _, err = repo.Append(ctx, at, second...)
```

`Fold` takes the identity for the same reason `New` does, and makes the same
comparison `Append` makes; §INV-044 is where that is argued. The identity is the
value the load already took, and `state` is deliberately the same variable on both
sides: `Fold` advances the caller's own value, so the argument is consumed
(§UC-041, §UC-068).

The **version** half can never be forgotten, because `Append` returns the advanced
token and the stale one conflicts. The **state** half is one visible, pure,
greppable line, and forgetting it is the one thing a caller can get wrong here —
§UC-021 is where that residual and the two worse alternatives are argued, and
§UC-062 is the control that makes the line load-bearing.

### D.6 The transaction

Two lines in the composition root, none in the usecase.

```go
tx, err := this.beginner.Begin(ctx)
if err != nil {
    return err
}
defer rollback(tx)

// the store's own binder, and it is the caller's line, not the framework's:
//   SQL:    crud.WithExecutorFor(ctx, source, tx)   — crud/executor.go
//   memory: eventmemory.WithTransaction(ctx, tx)
txCtx := this.bindTransaction(ctx, tx)

txCtx, err = accountRepo.Within(txCtx)   // refuses HERE, before any statement
if err != nil {
    return err
}

state, at, err := accountRepo.Load(txCtx, cmd.Account)
if err != nil {
    return err
}
changes, err := state.Credit(cmd.Account, cmd.ID, cmd.Minor)
if err != nil {
    return err
}
if _, _, err := accountRepo.Append(txCtx, at, changes...); err != nil {
    return err
}
if err := this.auditor.Record(txCtx, cmd.ID); err != nil {
    return err
}
return tx.Commit(ctx)
```

**There is no atomicity branch, because there is nothing to check.** Both
subsystems refuse to run outside the transaction the context carries, so either
both writes are in it or one of them refused before writing (§UC-030). The
`BoundTo`/`WroteWithin` branch round 3's sketch ended in failed on a correct no-op
program and compared two tokens that could not be made comparable (§D.12, Q17).

**The `Within` line is what makes that argument true, and it is not optional
here.** An ambient transaction is joined without it (§UC-046), so `Within` does
not admit the write — but the claim "both writes are in the transaction" rests on
a transaction being *bound*, and `Within` is the call that turns an unbound one
into `ErrNoTransaction` before the first statement. An operation whose atomicity
claim matters calls it; one with no such claim does not need it. §UC-066 is where
that obligation is argued.

```go
func (this *Repo[S, ID]) Within(ctx context.Context) (context.Context, error)
func (this *Repo[S, ID]) Authority(ctx context.Context) (Authority, error)
```

**`Within` returns a context**, not a bound repository and not an inner store.
That single choice does four things: a policing decorator stays in the path for
the whole transaction (§UC-048); a bound value cannot be called with a foreign
transaction's context, because there is no bound value; the declared-chain versus
exact-outer question is deleted rather than ruled on (§UC-033); and the marker it
carries chains on any previous one, exactly as `crud`'s executor binding does.

**The composition-root line, spelled correctly.** `crudsql.WithTransaction` is an
`Option`, not a context binder — a mistake a consumer would hit on day one. The
real binder is `crud.WithExecutorFor(ctx, source, tx)`, which phase 2's `eventpg`
recovers the way `jobspg` already does. It uses the **two-step** form —
`crud.ExecutorFor(ctx, source)` then `crudsql.Transaction(executor)` — rather than
the one-call `crudsql.TransactionFor`, because only the two-step form
distinguishes *nothing is bound* from *something is bound that is not a
transaction*, which are §INV-041's first and third rows (§D.15).

**What happens on a context that was never passed through `Within`** is
[[D-118]]'s answer and not a refusal: a transaction of this store's backing is
**joined**, an executor of this store's that is not a transaction is **refused**,
and nothing is written on autocommit while either is bound (§UC-046, §INV-041).
**And there is no position range on the receipt** — non-goal 20.

### D.7 The bounded read

```go
type Log interface {
    Capabilities() Capabilities
    Limits() Limits
    Backing() Backing
    ReadAll(ctx context.Context, after Cursor) ([]Envelope, Cursor, error)
}

func ReadOnly(store Store) Log
func Read(log Log, after Cursor) (*Reader, error)

func (this *Reader) Next(ctx context.Context) (bool, error)
func (this *Reader) Events() []Envelope
func (this *Reader) Cursor() Cursor
```

```go
func (this *Projector) Drain(ctx context.Context) error {
    reader, err := event.Read(this.log, this.checkpoint)
    if err != nil {
        return err
    }
    for {
        more, err := reader.Next(ctx)
        if err != nil {
            return err
        }
        if !more {
            return nil
        }
        for _, e := range reader.Events() {
            if err := this.handle(ctx, e); err != nil {
                return err
            }
        }
        this.checkpoint = reader.Cursor()      // persist this, never a position
        if err := this.save(ctx, this.checkpoint); err != nil {
            return err
        }
    }
}
```

**A consumer holds a `Log`, never a `Store`.** `Store` embeds `Log`, so a store
passes where a log is wanted; `ReadOnly` exists for the case where the consumer
must not be able to assert its way back to the append surface. A projector that
can append is how a replay writes.

**`Read` returns an error because it is a door, and doors check.** It is the
second and last place a store enters the kernel, so it runs the same three
store-honesty checks `Bind` does and refuses with `ErrWrongStore` (§INV-022,
§INV-043). `Bind`'s fourth check is the family collision, which a read cannot
have.

**The page `Events()` returns is the consumer's.** Its `[]Envelope` and every
`Payload` in it belong to whoever received them, indefinitely, from any goroutine,
and may be written into; no store reuses either across two calls (§INV-021's
fourth and seventh). So a page may outlive the `Next` after it, which is what
makes fanning it out to workers safe (§INV-038).

**There is no page-size parameter**, and that is GAP-51's fix: the kernel is the
only party that ever fills a read, so `Limits().MaxRead` has exactly one meaning
and no store can implement `min(0, MaxRead)` literally. A consumer that needs
smaller pages gets an additive option in the phase that has one (non-goal 19).

**The resume point is the cursor, never a position.** `cursor = e.Position`
silently skips events on any store where position 7 can commit before position 5,
which is what an ordinary SQL sequence produces (GAP-3); the pattern is now
unspellable, because `Read` takes a `Cursor` and nothing derives one from a
`Position`. Safe resumption is **mandatory for every store** (§INV-035), and
`MonotoneVisibility` is a freshness claim and nothing more (§UC-037).

**The word this section does not use.** A walk over a quiescent store tiles it.
Delivery is at least once, consumers are idempotent, and "exactly once" describes
neither (§INV-036).

### D.8 Testing an aggregate, and the three proxies

```go
// `state` is CONSUMED: see D.1 and UC-041.
func (this *Aggregate[S, ID]) Fold(id ID, state S, changes ...Change[S]) (S, error)

// byRevision holds one value per retained revision, in chain order. It is
// `...any` because the historical reader types are a heterogeneous list and Go
// has no variadic type parameters; the helper checks each element's dynamic type
// against the declared reader type for that position (§D.11).
func eventtest.RoundTrip[S, ID, E any](t *testing.T, fact *event.Fact[S, ID, E], byRevision ...any)
func eventtest.Keys[S, ID any](t *testing.T, a *event.Aggregate[S, ID], ids ...ID)
func eventtest.Families(t *testing.T, declarations ...event.Declaration)
```

**One fold, one name.** `Aggregate.Fold` replays a change list with no store and
advances a state between two appends. The second spelling round 3 shipped,
`event.Replay`, is deleted (§UC-041).

**Why the one fold takes an identity**: it is the only thing that lets it refuse a
change decided for another instance of the same aggregate; §INV-044 argues it.

**Why the one fold returns an error**, since a fold itself cannot fail
(§INV-017): four causes, in `Append`'s own order, each with its own sentinel, and
`RoundTrip` above is the proxy for the fourth. §UC-041 enumerates them, says what
`Fold` returns with each — the first three are pre-checks and return the input,
the fourth fires mid-list and returns the state as of the last change applied —
and notes that a **panicking fold** is none of the four (§UC-067, §INV-017).

**What `Fold` does to the state**: it folds it, in place for every reference kind
it reaches, because it runs the caller's own folds over the caller's own value.
The argument is **consumed**; §UC-041 is the contract, §UC-068 the case, and
§INV-021's inbound table the enumeration that makes it the only such row.

**All three helpers above are sealing readers** (§UC-005). So is `Fact.New`,
because it encodes. A declaration that arrives after any of them has run is
refused, which is the case a bind-triggered seal would pass.

The unit test, with the in-memory store — a real implementation, not a mock:

```go
func TestCreditingAnOpenAccountSurvivesAReload(t *testing.T) {
    store := newMemoryStore(t)          // NewLog + New, both error-returning
    defer store.Close()

    repo, err := event.Bind(event.Open(store), accounts)
    if err != nil {
        t.Fatalf("binding the accounts aggregate to the memory store failed: %v", err)
    }
    ctx, id := context.Background(), AccountID("acct-1")

    _, at, err := repo.Load(ctx, id)
    if err != nil {
        t.Fatalf("loading a stream that was never written failed: %v", err)
    }
    if at.Version() != 0 {
        t.Fatalf("a stream that was never written is at version %d, not 0", at.Version())
    }
    if at, _, err = repo.Append(ctx, at, opened.New(id, Opened{Currency: "EUR"})); err != nil {
        t.Fatalf("opening the account failed: %v", err)
    }

    state, at, err := repo.Load(ctx, id)
    if err != nil {
        t.Fatalf("loading the opened account failed: %v", err)
    }
    changes, err := state.Credit(id, "cmd-1", 250)
    if err != nil {
        t.Fatalf("crediting an open account was refused: %v", err)
    }
    if _, _, err := repo.Append(ctx, at, changes...); err != nil {
        t.Fatalf("appending the credit failed: %v", err)
    }

    // §UC-061 and §UC-063 in two lines, and they need no helper: mutate the
    // state the framework handed over. It is reachable from nothing it holds.
    state.Applied["cmd-1"], state.Balance = false, 999_999

    reloaded, _, err := repo.Load(ctx, id)
    if err != nil {
        t.Fatalf("reloading the account failed: %v", err)
    }
    if reloaded.Balance != 250 {
        t.Fatalf("the balance is %d after a reload, but 250 was the only credit appended", reloaded.Balance)
    }

    // §UC-068: Fold runs the caller's own folds over the caller's own value, so
    // `reloaded` is left a hybrid — its map advanced, its scalars not. That is
    // why only `advanced` is used afterwards.
    advanced, err := accounts.Fold(id, reloaded, credited.New(id, Credited{Minor: 100, Currency: "EUR", Command: "cmd-2"}))
    if err != nil {
        t.Fatalf("folding a second credit onto the reloaded state was refused: %v", err)
    }
    if advanced.Balance != 350 || !reloaded.Applied["cmd-2"] || reloaded.Balance != 250 {
        t.Fatalf("Fold did not advance the argument in place, so its contract has changed")
    }

    // The stale token conflicts, with no poisoning bit anywhere: it names
    // version 1 and the stream is at 2.
    if _, _, err := repo.Append(ctx, at, opened.New(id, Opened{Currency: "EUR"})); !errors.Is(err, event.ErrConflict) {
        t.Fatalf("appending through a token the stream has moved past was admitted: %v", err)
    }

    // §UC-064: a change decided for another instance is refused, and this is the
    // one crossing a type cannot catch — both sides are Account.
    _, otherAt, err := repo.Load(ctx, AccountID("acct-2"))
    if err != nil {
        t.Fatalf("loading the second account failed: %v", err)
    }
    if _, _, err := repo.Append(ctx, otherAt, changes...); !errors.Is(err, event.ErrWrongStream) {
        t.Fatalf("appending one account's changes through another account's token was admitted: %v", err)
    }
}
```

**What is honestly not shared with production**, said rather than denied: the two
composition lines — the store's constructor and the store's own transaction
binder. The app-usecase itself is byte-identical (§UC-040).

### D.9 The conformance suite

```go
package eventtest

type Tx interface {
    Commit(ctx context.Context) error
    Rollback(ctx context.Context) error
}

type Factory struct {
    New     func(t *testing.T) event.Store
    Begin   func(t *testing.T, ctx context.Context, s event.Store) (context.Context, Tx)
    Sibling func(t *testing.T, s event.Store) event.Store
    Fail    func(t *testing.T, s event.Store, outcome event.Outcome) bool
}

func Run(t *testing.T, factory Factory)
```

The factory constructs; the suite never does. Construction can fail (§D.4) and
only the factory knows how to report it, which is why `New` takes a `*testing.T`
rather than returning an error. `Fail` returns **false** for an outcome it cannot
produce, so "cannot" and "did not" are told apart rather than guessed. The one run
whose `Factory` the suite writes itself is `self-falsification` (§UC-045).

**`Fail` names an `event.Outcome`, and there is no second vocabulary.** A
test-side `FailKind` would be a second spelling of a classification the contract
now has (GAP-67): the hook asks for the outcome the store is to produce and the
section asserts the sentinel the kernel maps it to, which is
`architecture.md`'s one-format rule applied to a classification. It also widens
what the hook drives for free — `Conflict`, `Closed`, `BadCursor` and
`Unclassified` are all askable, and `Unclassified` is what pins the door-dependent
fail-safe default (§INV-045).

**Hook requiredness is derivable from the capability report, for every hook.**

| Hook | Required when | Missing while required |
|---|---|---|
| `New` | always | the suite cannot run |
| `Begin` | `Capabilities().Transactions == Supported` | **fails** |
| `Sibling` | `Capabilities().SharedBacking == Supported` | **fails** |
| `Fail` | never | the failure-classification section is reported **not certified**, per outcome: a store that can produce `NotWritten` and not `Unconfirmed` certifies the first half and not the second, which is `eventmemory` exactly (§UC-044) |

`SharedBacking` exists so the third row's requiredness is derivable at all
(GAP-49). Without a sibling, `resumption` runs the same walk in a fresh **reader**
over the same store value — still mandatory, still catching a cursor that is not a
safe resume point; only the *restart* half needs a second value.

**The sections.**

| Section | Runs | What it asserts | Its control |
|---|---|---|---|
| `binding` | always | two families bind; the same declaration binds twice; a store with an invalid backing, a zero limit or an unstated capability is refused **at `Bind` and at `Read`**; and the escape is driven: two different declarations with one family, bound through **two** `Binding` values over one store, are **admitted**, which is the documented scope and not a guarantee (§UC-059) | the good store binds, and the same collision through **one** `Binding` refuses |
| `stream identity` | always | (family, key) byte-exact; separator-ambiguous and NFC/NFD keys are two streams; a NUL is refused | a key at the cap length loads |
| `expected version` | always | admission only at the observed version; a stale token conflicts; **a change decided for another instance of the same aggregate is refused with `ErrWrongStream` and writes nothing** (§UC-064), and so is one decided for **another aggregate declared over the same state type**, which is the key half and the family half of the one comparison (§UC-002, §INV-020); a forged zero token is `ErrKey` (§INV-015) | the correct-version append succeeds, and the two-instance transfer with the correct pairing lands each fact in its own stream |
| `dense versions` | always | 1..n with no gap, no reuse, no reassignment | — |
| `global order` | always | positions strictly increase, are never reassigned, and gaps are tolerated | a single-writer walk is ascending |
| `conservation` | always | the union of the stream reads equals the global read as a **set**, and no stream's read holds another's event (§INV-034) | — |
| `stream paging` | always | one stream folded at `StreamPage` and at 1 gives identical state and version | the whole stream in one page |
| `global paging` | always | a quiescent walk tiles; the same walk at `MaxRead` 1 gives the same sequence | — |
| `resumption` | always | a persisted cursor resumes and skips nothing, **including with a writer committing out of position order**; with `SharedBacking`, also through a second store value over one backing | a walk with no interruption sees the same events |
| `bounds` | always | every bound, each case built from `store.Limits()` rather than a literal | an at-the-bound payload, batch and key are accepted |
| `payload ownership` | always | the **four** hand-offs a store is party to (§INV-021's 3, 4, 6 and 7), which are the ones that vary with the store. **Outbound (4 and 7):** **every byte of every returned `Envelope.Payload` is overwritten and the same page re-read and the stream re-loaded, both unchanged**, which a store that only *froze* what it returned fails (GAP-68); **page one is held, page two is fetched, and page one's envelopes and its payload bytes are both asserted unchanged**, which a store that pools either slice — or that hands out a driver buffer valid only until the next fetch — fails (GAP-94); and, through a codec that decodes by aliasing, mutating a state field decoded out of a payload changes no later read (§UC-061). **Inbound (3 and 6):** the same fact is minted **twice**, into two change lists; one is appended and the other never leaves the test; afterwards, folding either must give the same state and both must equal a reload. A store that writes into a `Record.Payload` it was handed fails it, because only the appended list's arrays are ever in its hands — the third hand-off's promise made into a case rather than left as a clause (§INV-021, GAP-87) — and it is what the sixth self-falsification defect is aimed at. The kernel's own hand-offs (1, 2 and 5) are asserted in `event`'s own tests, below | two reads of one page are equal; the pre-mutation read equals the post-mutation one; a store with fewer events than one page returns page one and then an empty page, so the two-page case is not the only shape; and the two change lists fold identically **before** the append, so a divergence after it is the append's |
| `refusal classes` | always | `errors.Is` never crosses a class, including for refusals built from a store's own error and for a `Failure` a decorator **wrapped**, which must map to the same row as an unwrapped one (§INV-045); each declared class wrap is reachable and not replaced; a store returning one of this vocabulary's sentinels as its own cause **fails** (§INV-024, §2.3's cause obligation) | the same table over refusals with no cause answers identically |
| `cancellation` | always | before any statement the cancellation sentinel survives; in the commit window the answer is `ErrUncertain` | the uncancelled append succeeds |
| `lifecycle` | always | close is idempotent and returns nil twice; an operation after close is `ErrClosed` — including a `Load` **with a legal identity**, whose first act after the key check is `Transaction(ctx)`, which a closed store must answer with the invalid authority and no error rather than a failure of its own (§D.14, GAP-84); a `Load` with a **zero** identity against the same closed store is `ErrKey`, because the key check is first (§D.5's `Load` order, §UC-051, GAP-97); and, with `Transactions` claimed, staged work is **invisible to another reader** after a close that left the transaction unresolved — which is the half true of a store that discards its own staging *and* of one that staged inside the caller's transaction (§UC-047, §INV-032, GAP-69). It asserts nothing about a commit issued **after** a close, which §UC-047 names as store-dependent and as the caller's ordering obligation | the same operations before close succeed, **and work committed before the close is visible after it** — without which "invisible" passes against a store that hides everything |
| `concurrency` | always | many goroutines, one repository, `-race` clean, exactly one winner per version; the same goroutines read one `*Aggregate` and its `*Fact`s throughout, which is the shape §INV-038 says the seal makes safe and the only one of its new rows that is not a caller's own affair (GAP-74) | a single writer succeeds |
| `self-falsification` | always | the six defects — four forwarding decorators and two purpose-built defective stores — each fail the section named for them (§UC-045) | the plain store passes |
| `transactions` | `Transactions` | join, refuse-non-transaction, mismatch, rollback discards, **two live transactions answer authorities that are not `Same`** (`Begin` twice — the savepoint case is asserted elsewhere, §UC-049, GAP-71), **two `Within` contexts chained for two backings both proceed** (§UC-028, GAP-73); **and the obligation's control: a transaction opened and never bound leaves its events behind after a rollback**, asserted so nobody reads silence as a guarantee (§UC-066) | an append with no transaction succeeds, and the same append with the transaction bound lands inside it |
| `durability` | `Persistence` | what survives a store restart | — |
| `shared backing` | `SharedBacking` | a token minted through one value appends through another; two backings still refuse | the two-backing case refuses |
| `monotone visibility` | `MonotoneVisibility` | a committed event is immediately at or below the returned cursor | — |
| `store failure classification` | `Fail` supplied | one case per `event.Outcome`: `NotWritten` → `ErrBackend` and zero events; `Unconfirmed` → `ErrUncertain`; `Conflict` → `ErrConflict`; `Closed` → `ErrClosed`; `BadCursor` → `ErrCursor`; `Unclassified` → `ErrUncertain` **from `Append`** and `ErrBackend` **from `ReadAll`**, which is the pair that pins the door-dependent fail-safe default; every one of them again through a decorator that wraps the store's error, asserting the sentinel is unchanged; the store's own error reachable with `errors.Is` through each, and its text absent from every rendered message (§INV-025, §INV-045) | the uninjected append and the uninjected read succeed |

**The four anti-vacuity rules** are §UC-043's, and they are what make a green run
evidence.

**What is deliberately *not* a section here, and where it is asserted instead.**
This suite runs once per store, so a property that does not vary with the store
does not belong in it — running it N times tests the kernel N times and tells
nobody which store failed. **Seven** such properties are named so that nobody
looks for them in the table. (1) A **panicking fold** and (2) a **panicking
upcaster** (§UC-067, §INV-017) are the kernel's treatment of an application
callback. (3) A **`Fold` given one instance's identity and another's change
list** (§UC-041, §INV-044) needs no store at all, which is its whole point, and
(4) neither does **the value `Fold` returns for each of its four causes — in
particular the state after *k* folds when cause 4 fires mid-list** (§UC-041,
§INV-006). (5) **`NewAuthority(b, tx)` twice over one identity answering `Same`**
(§UC-049, §INV-028) is a property of the value, not of a store. (6) **§UC-065's
fold-mutate-fold-again, driven through a codec that decodes by aliasing its
input** is §INV-021's second hand-off — a `Change` and the kernel's own decode,
with no store in it, and the case a suite over `event.JSON` alone would pass
against a kernel that never cloned. (7) **§UC-068's fold-advances-the-argument
pair, with §INV-004's proxy (2) beside it**, is §INV-021's inbound table's one
non-read-only row and the case that fails if a later `Fold` clones. All seven are
asserted in `event`'s own tests. The store-side halves — a change crossed at
`Append` writing zero events, an authority's `Same` across two appends in one
transaction, a store's own payload and page hand-outs — are in the table, because
those *are* about the store.

**Three words, never two.** *Passed*, *not certified*, *failed*. A capability not
claimed and one that cannot be demonstrated are reported in the same words, and
neither is ever a pass (§UC-044). `eventmemory` reports half of `store failure
classification` not certified: it has no commit window to lose.

### D.10 What a caller cannot do, and the six things it still can

Not a summary — an inventory, because a design's safety is what it makes
**inexpressible**, and an honest one names what it does not.

| Cannot be written | What stops it |
|---|---|
| An append at a version the caller chose | No parameter and no constructor for the token that carries one (§INV-015) |
| An append that names no version | No spelling: `Append` takes a token, and every token carries one |
| Aggregate B's change in aggregate A's append, **when the two have different state types** | A compile error (§INV-020) |
| **The same crossing when the two aggregates share one state type** — two bounded contexts over `type Ledger map[string]int64`, or a generated declaration set | Not a compile error — the caller-seam types are parameterised by the state type, so they coincide — but the same deterministic refusal: a change carries its `Stream`, whose family is B's, and `Append` compares (§UC-002, §INV-020, §INV-044) |
| **Instance B's change in instance A's append** | Not a compile error — one aggregate is one Go type — but a deterministic refusal: a change carries the stream it was decided for and `Append` compares (§UC-064, §INV-044) |
| **Instance B's change in instance A's fold** | The same refusal at the other call that pairs a change list with an instance: `Fold` takes the identity and compares (§UC-041, §INV-044). Round 5 closed only the append half (GAP-70) |
| A caller mutating an envelope's payload, or a state field a codec aliased out of one, and corrupting the store or a decided fact | Whoever hands mutable memory over says whether anybody will write it again: a store clones what it retains **and what it borrows**, and the kernel clones a `Change`'s frozen bytes before every decode (§INV-021, §UC-061, §UC-065) |
| A **store or a decorator** rewriting an already-decided fact by transforming `Record.Payload` in place | The one hand-off answered by the recipient's **promise** instead of a copy — and the `payload ownership` section's inbound half is what exercises it (§INV-021's third, §D.14, §UC-048) |
| A **store** handing out memory a driver, an `mmap` or a library buffer will overwrite, or pooling the `[]Envelope` it returns | The obligation is the recipient's freedom, not the store's own future reads: what a read returns is the caller's for as long as it keeps it, so a store that borrows its bytes copies (§INV-021's fourth and seventh, §D.14) |
| A blind "create" path distinct from a normal append | Version 0 is an ordinary version and a never-written stream loads as the zero state (§UC-009) |
| A resume from a raw position | `Read` takes a `Cursor`, and nothing derives one from a `Position` |
| A lenient load that skips an unreadable event | No option exists (§INV-006) |
| A retry of a conflicted change list inside the framework | No loop exists in `event`, and [ES] forbids one (§INV-007) |
| An operation that escapes **an executor the store can see** onto autocommit | The store answers what is bound for its own backing and the kernel joins or refuses (§INV-041). A transaction that is *not* bound is a different case and is **not** prevented — §UC-066 and the third residual below |
| A decorator bypassed for the duration of a transaction | `Within` returns a context, not an inner store (§UC-048) |
| A fact declared after the table has been read | The seal (§UC-005) |
| A payload mutated after `New` changing the recorded fact | The encode happened at `New` (§UC-063) |
| A forged `At[S]{}` reaching a store | Its key is empty and `Append` validates the key it is handed, not only one it produced — step 1 of §D.5's order, before the empty-append short circuit (§UC-051, §INV-015) |
| A `Change` whose folded value can be changed after it was decided, or whose recorded bytes can be changed by a fold that published a reference kind | It retains no decoded value, and every fold decodes a **clone** of the frozen bytes, so what the fold hands the application aliases nothing a later append sends (§UC-063, §UC-065, §INV-021, §INV-042) |
| A state the framework holds being corrupted by the caller | The framework holds none (§INV-042) |
| A read against a store that never stated its bounds | `Read` checks what `Bind` checks (§UC-036, §INV-022) |

**The six a caller can still get wrong, named rather than hidden.**

1. **Forgetting `accounts.Fold(id, state, changes...)` between two appends in one
   operation.** Both appends succeed, no error is returned anywhere, and the
   second decision was taken on a state missing the first fact. §UC-021 argues the
   residual and §UC-062 is the control that makes the remaining line
   load-bearing.
2. **Re-appending a change slice the caller still holds.** A successful append
   does not consume a `Change`, so the outer shape
   `for attempts { …decide once outside the loop…; repo.Append(ctx, at, changes...) }`
   records the facts again if it ever succeeds twice. The framework cannot detect
   it, and §UC-058 is why: two identical changes in one batch are legal, so two
   identical changes in two batches cannot be a framework error either. Where it
   matters, the command's identity belongs in the payload (§UC-035).
3. **Opening a transaction and never binding it into the context.** Every
   operation then runs on autocommit, the rollback takes none of them, and nothing
   refuses, because a transaction nobody bound is indistinguishable from no
   transaction. §UC-066 argues it; `Within` is the mechanism the caller opts into;
   and the `transactions` section asserts the unsafe outcome on purpose, so this
   row cannot quietly become a belief that the framework prevents it. It is the
   one residual here that is an **application obligation** rather than a limit of
   the type system.
4. **Handing one instance's *state* to another instance's fold or decision.**
   `accounts.Fold(id, otherState, changes...)` passes every check the framework
   can make: the identity and the changes agree, and the state — the one value in
   the whole design that carries no stream — disagrees with both in a way nothing
   can observe. The caller then decides on a state that is not this instance's,
   names this instance everywhere else, and appends successfully. It is round 6's
   addition to this list and it is what remains of GAP-70 after `Fold` gained the
   identity: the crossing between two values the framework minted is refused, and
   the crossing involving the value the framework deliberately does **not** hold
   is not. No proxy on `eventtest.Keys`'s model can close it either, because a
   proxy would need to know which `Load` produced which `Account`, which is
   precisely the retention §INV-042 forbids. What makes it survivable is that a
   reload is the authority and is one line away (§UC-052, §UC-061).
5. **Loading through the wrong repository, when two aggregates share one state
   type.** `repoB.Load(ctx, anAIdentity)` renders B's key, folds B's folds and
   returns B's state with no error — which is correct behaviour for `repoB`, and
   the repository is the only party in that call that names a family, so there is
   nothing for it to disagree with. It is §INV-020's third crossing and the one
   with no runtime answer; the *append* half of the same crossing is not a mistake
   at all, because `Append` reads the token and the changes and never the
   declaration. What makes it survivable is the same thing as item 4: the reload
   is the authority, and the state it returns is one aggregate's throughout.
6. **Using the state that was passed to `accounts.Fold(id, state, changes...)`
   after the call.** `Fold` runs the application's own folds over the caller's own
   value, so it writes through every reference kind that value reaches: the
   argument is **consumed**, and for `type Ledger map[string]int64` it *is* the
   result, which makes `state, err = Fold(…)` a no-op assignment (§UC-068). Take
   the returned state and drop the argument. It is not closable by a mechanism —
   a shallow copy is what Go already does and is exactly what produces the hybrid,
   a deep one is §UC-052's deleted copier walk, and either would make `Fold`
   behave differently from the loop the caller would have written (§UC-041). It is
   the one row here that is a **property of an argument** rather than of the type
   system, and §INV-021's inbound table is where it is enumerated so that no later
   reader has to rediscover it.

**And one shape that reads as a retry and is a livelock.**
`for { repo.Append(ctx, at, changes...) }` can never write once the token is
stale: every iteration conflicts, because the token names a version the stream
has moved past, and nothing in the loop reloads. §UC-034's procedure is bounded
for exactly this reason — one retry after an uncertainty, and a **reload and a
new decision** after a conflict.

**One thing that is not on either list, and why.** Naming the wrong instance's
identity in a decision — `fromState.Debit(to, amount)` — is a domain error written
in the application's own vocabulary, in which the program says it wants to debit
`to`. It is **not** item 4: item 4 is a mechanical crossing where the program's
own vocabulary says one thing and one argument says another, while this program
says the same wrong thing everywhere. Its mechanical half — the two adjacent
tokens swapped at the append — is refused (§UC-064), and this entry exists so
nobody reads that refusal as covering more than it does.

### D.11 What type inference does, and the two places it stops

| Written at the call site | Inferred | How |
|---|---|---|
| `event.Define[Account]("accounts.account", func(id AccountID) event.Key { … })` | `ID = AccountID` | a partial type-argument list with the explicit parameter first; `ID` comes from the mapper's parameter |
| `event.From(event.JSON[Opened]())` | `V = Opened` | from the codec's own type argument |
| `event.Then(prev, event.JSON[Credited](), toCreditedV2)` | `A` from `prev`, `B` from the codec **and** the upcaster's result | two independent sources that must agree, so a mismatched upcaster is a compile error |
| `event.Declare(accounts, "accounts.credited", chain, fold)` | `S`, `ID` from the aggregate; `E` from the chain | the fold's `func(S, E) S` must match all three, so a fold for the wrong revision does not compile; the result is `*Fact[S, ID, E]` and all three are inferred |
| `credited.New(cmd.Account, Credited{…})` | nothing to infer | a method on a fully instantiated `*Fact[S, ID, E]`; another aggregate's identity type is a compile error **when it is a different type**, and another *instance's* identity — or another aggregate's over the same `ID` — is §UC-064's refusal |
| `event.Bind(binding, accounts)` | `S`, `ID` | from `*Aggregate[S, ID]` |
| `repo.Load(ctx, id)` / `repo.Append(ctx, at, changes...)` | nothing | methods on `*Repo[S, ID]`; another aggregate's token or change is a compile error **when its state type differs**, and `ErrWrongStream` when it does not (§INV-020) |
| `accounts.Fold(id, state, changes...)` | nothing | a method on `*Aggregate[S, ID]`; the same split as `Append`'s row above, and another *instance's* identity is §UC-041's refusal |
| `eventtest.Keys(t, accounts, id1, id2)` | `S`, `ID` | from the aggregate; the ids must be `ID` |

**Where it stops, admitted rather than hidden.**

1. **There is no fluent chain.** Go has no method-level type parameters, so a
   revision is added with `Then(From(codecA), codecB, up)` rather than
   `From(codecA).Then(codecB, up)`. The nesting reads outward-in for a long
   chain, and that is the cost.
2. **`eventtest.RoundTrip` cannot be typed over every retained revision.** The
   historical reader types are a heterogeneous list and Go has no variadic type
   parameters, so the helper takes `byRevision ...any` positionally and checks
   each element's dynamic type against the declared reader type for that
   position, failing with the revision and the two type names. A per-revision
   typed helper would test the codec without testing the upcaster chain to the
   current type, which is the half §UC-042 exists for.

### D.12 Rejected alternatives, with the argument each was rejected on

| Shape | Why it is not here |
|---|---|
| A live `View` holding state, version, store binding and accumulator | Five jobs in one object; `architecture.md`'s relocation test fails by inspection, and every one of round 3's twelve open findings was a consequence of it |
| `repo.Append(ctx, id, expectedVersion, changes...)` | Puts the version back in the caller's hands as a number, which is the one thing [ES] calls out by name |
| A two-value `Append` beside the three-value one | Two ways to do one thing; the shorter one becomes the one everybody uses, and the receipt disappears from the shape consumers copy |
| A framework `Decide`/`Do` that loads, decides and appends in one call | It would have to own the retry policy, the conflict policy and the error mapping — three decisions that belong to the application, hidden behind a convenience |
| A `Cloner[S]` constraint on the state type | The dangerous implementation is the one a hurried author writes — `func (this Order) Clone() Order { return this }` — and it is **correct** for a scalar state, so the constraint cannot distinguish a right answer from a wrong one. Recorded here so nobody re-proposes it in phase 2 |
| The transitive copier walk, `event.Copy`, `eventtest.Copies` | Refused `struct{ PlacedAt time.Time }`; its only escape permanently disarmed it; its proxy could not falsify it (GAP-42, §UC-052) |
| `ID comparable` | An identity is never a map key here, so the constraint buys nothing and refuses a composite identity holding a hash or a path slice (§UC-050) |
| A per-aggregate **phantom type parameter** — `Aggregate[S, ID, A]`, `Change[S, A]`, `At[S, A]`, `Repo[S, ID, A]`, `Fact[S, ID, E, A]` — so that two aggregates over one state type are two Go types | It closes §INV-020's second row at the type level and costs more than the row is worth. Every consumer declares a marker type whose only job is to be distinct (`type accountTag struct{}`), four of the five caller-seam types gain a parameter, and `A` cannot be inferred from anything at the declaration site, so `Define[Account, AccountID, accountTag]` spells all three by hand — which is §D.11's whole subject going the wrong way. It also buys nothing for the larger risk, two *instances* of one aggregate, which no type parameter reaches and which the same `ErrWrongStream` comparison already refuses. Recorded so nobody re-proposes it in phase 2 (§INV-020, GAP-80) |
| An opaque `Cursor` struct with `event.NewCursor` | The minting call is equally available to a caller, so the unconstructibility argument becomes a sentence; the defined string type keeps §INV-019 true instead (§UC-053, Q18) |
| `event.Since[V2](2)` and a retention floor | The one hand-typed number in the design, whose off-by-one produces a **successful** misdecode; retention is additive and brings its own refusal and tests |
| An error-returning sibling of the declaration calls | ~~A declaration binds Go types at compile time, so nothing dynamic can use it; the return would be discarded by an `init` and would give a second, unsealed way to build a table. The tree's `Define`/`MustDefine` convention disagrees and is Q21.~~ **Superseded: `TryDefine`/`TryDeclare` ship** ([[D-123]]). The refused shape was the `Define`/`MustDefine` inversion; the sibling that shipped keeps the short name panicking, has no body of its own, and exists for the negative tests |
| A store-owned codec, and `Store.Codec()` | Deferred the encodability check past `main`, made a per-request `Bind` walk the readers, and made per-revision codecs inexpressible (§D.2) |
| A `Config()` bundle instead of `Limits()` and `Capabilities()` | Both are read on every operation; a bundle would be re-validated on every call, and the tree's bundles (`Describe`, `BackendDescription`) are *descriptions*, not values the kernel uses |
| A versioned key rendering, on `cache.KeyCodec`'s model | A cache key may miss; a stream key may not. Bumping a key version orphans every existing stream — the precise failure §INV-005 exists to prevent, with a knob to cause it |
| An optional `Transactional` interface found through a declared chain | An optional interface can be *missing*, so the escape and mismatch checks could be absent; and `Within` returning the inner store removes every decorator for the whole transaction ([[D-115]], GAP-47) |
| A bound `*Repo` returned by `Within` | `bound.Append(otherTxCtx, …)` writes into the first transaction while the caller believes the second, with no refusal available |
| Refusing an ambient transaction the caller did not route through `Within` | Refuses a program `jobs` accepts and [[D-118]] blesses; the escape it feared is autocommit-while-open, which joining prevents (§INV-041) |
| `ErrBatch` as its own row | A payload over the byte cap and a batch over the count cap have one caller obligation and one transport class; two rows for one obligation only ever get tested |
| `Commit.Durability` and `Commit.Positions` | Three values with no reader and no honest answer for an empty commit; and a "for later" API `restrictions.md` §1 forbids |
| `Authority.BoundTo(ctx)` and a shared cross-subsystem mint | Both subsystems refusing is stronger than comparing tokens, needs no shared vocabulary, and adds no import in either direction (§UC-030, Q17) |
| Marking a `Change` consumed by a successful append | Two identical changes in one batch are legal (§UC-058), so consumption would forbid a correct program to catch an incorrect one it cannot distinguish |
| A caller-supplied read page size | Its zero value had two meanings on the one path with no kernel in it (GAP-51); a consumer that needs one gets it additively |
| A refusing close, or a close that commits staged work | `defer store.Close()` beside an explicit shutdown close is the ordinary shape, and two calls answering differently is what the suite exists to eliminate |
| A per-store key byte domain | A dial nobody could calibrate, replaced by one kernel text rule that a store either can hold or cannot host this data at all |
| A `Change` that does not name its stream | Then `repo.Append(ctx, toAt, debitChanges...)` compiles, passes every other check and corrupts two histories in silence; both values are typed by the aggregate, so no type can tell two instances apart (§UC-064, GAP-54) |
| `Fact.New(at, payload)` — binding the change to the **token** rather than the identity | Puts a framework type into every decision signature, and leaves the store-free fold of §UC-041 needing a token from a store it deliberately does not have. The identity is a value the caller already holds and is its own type |
| A `Change` retaining the value decoded from its bytes | It falsified §INV-042's own source check, and a fold that aliased it published a path from the caller's state into a framework-held value, so folding the change twice could disagree with the bytes recorded (§UC-065, GAP-55) |
| An `Authority` as a minted `[32]byte` memoised per live transaction | A `*sql.Tx` has no completion callback, so the memo has no eviction point and grows for the life of the process; deriving from the pointer value instead lets a reused address make two transactions compare `Same`. Holding the transaction's identity by reference needs neither, and the retention is what prevents the reuse (§INV-028, GAP-64) |
| Store-honesty checks at `Bind` alone | A consumer-only deployment never binds; a store answering `MaxRead: 0` then reaches a projector unvalidated and its drain reads nothing (§UC-036, GAP-60) |
| A `Fold` that does not name its instance | Then `accounts.Fold(fromState, toChanges...)` compiles and advances one instance's state with another's facts, and every later check passes because the caller names *this* instance everywhere else. Round 5's weaker "one stream per list" check passes on a list that is entirely the other instance's (§UC-041, §INV-044, GAP-70) |
| A `Fold` that takes the at-token instead of the identity | It would compare the same thing, and it would leave §UC-041's store-free fold needing a token only a store can mint — the argument that already rejected `Fact.New(at, payload)`, applied to the other call |
| A store-side `freeze` obligation on `Envelope.Payload` instead of *clone what you retain* | A full-slice expression over the store's own history satisfies "freeze" literally, and then a consumer's `e.Payload[0] = 0` or an aliasing codec writes into the store's own history through the one path this document calls safe (§INV-021, GAP-68) |
| A kernel-side clone of every `Envelope.Payload` on decode | Pays on every event of every read for every store, including the stores that already own their bytes, and still leaves `Reader.Events()` open because a consumer's envelope never goes through a codec |
| A hand-out rule that binds the store and not the kernel | Two rules with one name. The kernel retains a `Change`'s frozen bytes for that change's whole life, so an aliasing codec published a framework-owned array into the caller's state through a fold the document blesses, and a later append then recorded the mutation (§INV-021, §UC-065, GAP-79) |
| A kernel-side clone into `Record.Payload`, so the store may write into what it is handed | One allocation and one 200 B copy per record on every append on every store — 10 000 clones and ~2 MB to write 10 000 events, 100 000 and ~20 MB for 100 000 — on the **write** path, and most of it paid by the SQL store that hands the bytes to a driver and retains none of them. The obligation costs a **retaining** store the same clone and a non-retaining one nothing, lands on the party §D.14 is addressed to, and is the shape `io.Writer` uses for the same problem; it is a conformance case and a self-falsification defect rather than a clause (§INV-021, §D.13, GAP-87) |
| A `Fold` that clones the state it is given | Go's own shallow copy is what produces the hybrid — a map advanced, the scalars not — and a deep one is the transitive copier walk this table already rejects two rows above. Either would make `Fold` behave differently from the loop the caller would otherwise write, and either would break a fold that mutates a large state deliberately, which `type Ledger map[string]int64` has no alternative to. The answer is a stated contract with a control that fails if a later `Fold` clones (§UC-041, §UC-068, GAP-93) |
| An outbound ownership rule stated as *memory the store will never read again* | It is a claim about the sender's future rather than the memory's lifetime, so it is satisfied by an array a **third party** overwrites — a driver accessor valid until the next fetch, an `mmap`ed page, a library scratch — and the consumer's retained page changes under it with no error. The rule is quantified over the recipient's freedom instead, and it covers the `[]Envelope` as well as the payload (§INV-021's fourth and seventh, GAP-94) |
| A `Repo` that compares its own family against the token's | It would refuse `repoB.Append(ctx, atFromA, changesFromA...)`, which over one store is *the same call* `repoA` would have made and lands each fact in the stream it was decided for; and it would still have nothing to compare at `repoB.Load`, where the repository is the only party naming a family. A refusal for the harmless half and none for the observable one (§INV-020, §D.10's fifth residual, GAP-90) |
| An obligation on `Codec.Decode` not to alias its input | Contradicts §INV-021's own finding that aliasing decoders are ordinary — it is the shape a compact binary format takes — and a codec that could be trusted this way would make the store's hand-off obligation unnecessary too. It is a promise no third-party codec makes and no proxy can falsify |
| Declaring the `E` a fold receives **borrowed** | §UC-065 forbids it: a fold may keep, mutate and publish its `E` because it is the fold's own. It is also level four of the safety ladder — a rule in prose — where the level above it (clone at the boundary) is available and costs nothing on the load path |
| `Close` that discards staged transactional work | True of a store that owns its staging and false of one that stages inside the caller's transaction, which is every SQL store under [[D-118]]'s join — and it was asserted by a section the suite runs `always` (§UC-047, GAP-69) |
| Recovering a panicking fold into a sentinel | Puts a programmer error into a runtime vocabulary a transport branches on, and round 5's choice of `ErrUpcast` did it in the wrong class, with a declared wrap it does not have, on loads where no upcaster ran (§UC-067, GAP-72) |
| A savepoint hook on the conformance factory | Its requiredness would be derivable from no capability, which §D.9's own rule forbids; and the property is about the authority's derivation rather than about a store, so it is asserted where it is a claim (§UC-049, GAP-71) |
| A `FailKind` in `eventtest` beside the store's own classification | Two spellings of one classification, one of them in test code — and it existed only because the contract had no spelling at all (§D.9, GAP-67) |
| A generated event identifier, causation/correlation ids, a metadata map, a caller-supplied timestamp | §2.2, each with its own argument and its own "what would reopen it" |

### D.13 What this costs, in numbers

`universality.md` asks a threshold to be derived rather than asserted, and
`restrictions.md` asks a cost to be stated rather than called acceptable. The
defaults, and the arithmetic they come from:

| Number | Default | Derivation |
|---|---|---|
| `MaxPayload` | 64 KiB | `jobs.DefaultPayloadBytes`. An event payload and a job payload are the same kind of object — a small declared struct, encoded once, stored in a row — so a *different* number would need the argument, not this one |
| kernel payload ceiling | 1 MiB | `jobs.MaxPayloadBytes`. A store's `MaxPayload` is never above it |
| `MaxBatch` | 64 | batch × `MaxPayload` is the memory one append holds: 4 MiB at the caps |
| `StreamPage` | 256 | page × `MaxPayload` is the memory one load page holds: 16 MiB at the caps |
| `MaxRead` | 256 | the same arithmetic for the global read: 16 MiB at the caps |
| `MaxKey` | 512 B | keys are indexed; 512 is generous for a composed identity and small enough to index |
| declared-identifier cap | 128 B | `jobs.MaxNameBytes`, and the same identifiers in the same kind of column |

**The rule those products encode, and it is the part a deployment must
understand:** raising `MaxPayload` without lowering `StreamPage` and `MaxBatch`
multiplies the memory one load or append can hold. The bound is the *product*.

**A real aggregate, at 200 B per encoded payload.**

| | 10 000 events | 100 000 events |
|---|---|---|
| pages read (`StreamPage` 256) | 40 | 391 |
| peak resident during the load | one page (~90 KB) + the state | one page (~90 KB) + the state |
| **retained after `Load` returns** | **the state, and nothing else** | **the state, and nothing else** |
| decodes | 10 000 | 100 000 |
| folds | 10 000 | 100 000 |
| upcast hops | one per event below the current revision, zero for the rest | the same |
| re-folds after an append | **zero** | **zero** |

**What the deleted shape cost, for contrast.** A `View` holding the encoded facts
retained ~20 MB per in-flight view at 100 000 events and re-folded the whole
stream after every append — O(n·k) for k appends, unbounded without snapshots.
Retaining nothing is two orders of magnitude of resident memory per request.

**`Fact.New` costs one encode and one mapper call; a fold costs one clone plus
one decode per change.** For a 200 B payload the encode is roughly a microsecond
and a handful of allocations, paid **at decision time** — including for changes a
decision then discards. The clone and the decode are paid only where a fold runs,
which the ordinary operation (load, decide, append, done) never does; §UC-021's
two-decision shape pays them once per change. Encoding **and** decoding at `New`
unconditionally would be dearer on the common path and would falsify §INV-042
(§UC-065). A decision that returns an error before calling `New` costs nothing.

**What the kernel's clone costs, in the same numbers.** *On a replay through
`Load`: nothing.* The load path decodes an `Envelope.Payload`, which is the
store's hand-out and is already the kernel's to read, so a 10 000- and a
100 000-event replay each pay **zero** kernel clones — that is what makes one rule
over seven hand-offs cheaper than a kernel that cloned defensively everywhere. The
clone falls only on `Aggregate.Fold` over `Change` values, at one allocation and
one 200 B copy per change, none of it retained past the decode. Folding a
10 000-change list therefore copies ~2 MB in total and a 100 000-change list
~20 MB, at one payload live at a time; the ordinary operation folds at most
`MaxBatch` = 64 changes, which is ~13 KB. The exchange is §UC-063 and §UC-065:
the inbound aliasing question is unaskable, the fact recorded is the fact decided,
and neither claim depends on which codec the application declared.

**And what the third hand-off would have cost as a clone, which is why it is a
promise** (§INV-021, §D.14). Cloning into `Record.Payload` is one allocation and
one 200 B copy per record, on every append, on every store: writing 10 000 events
is at least 157 appends at `MaxBatch` = 64, so **10 000 clones and ~2 MB**, and
100 000 events **100 000 clones and ~20 MB** — the fold's arithmetic, except on
the **write** path and on every store rather than only the one that folds. The
promise costs nothing for a store that does not retain, and for one that does
exactly the clone the kernel would have paid on its behalf — `eventmemory`, at the
same 10 000 clones and ~2 MB — so it wins by the whole of the SQL store's share,
which the alternative spends for nothing. §D.9's `payload ownership` inbound half
and the sixth self-falsification defect are what check it.

**Hand-offs 6 and 7 are new numbers and not new work.** A slice per call is what
`eventmemory` and any `database/sql` store already allocate, there being no other
way to build one out of a row scan: **zero extra bytes for every store this
document contemplates**. They cost only the store that wanted to pool a
`[]Envelope` or hand out a driver accessor's buffers, which now copies one slice
plus at most `MaxRead` envelope headers per read, or one payload per row — the
price of a consumer being allowed to keep a page (§INV-021, §INV-038).

**Comparing a change's stream with the token's costs one struct comparison per
change**, on a `Stream` of a string and a string — the same comparison the append
already performs against the backing, and it happens before any bound check, any
transaction question and any statement (§INV-044). **Validating the token's key
costs one scan of at most `MaxKey` = 512 bytes per append**, and it is the check
that refuses a forged token, so it is paid once per append rather than once per
change (§UC-051, §INV-015). **`Fold` pays one mapper call plus the same comparison
per change**, the price of closing the second half of the pairing error (§UC-041):
a mapper is a `Compose` over a handful of strings, against a decode per change
that is two orders of magnitude more.

**A store clones a payload it retains or borrows, on the way out.** For
`eventmemory` that is one allocation and one ~200 B copy per envelope returned, so
a 100 000-event load copies ~20 MB across 391 pages and holds one page at a time.
For a SQL store scanning into its own slices it is **free**, and for one handing
out a driver buffer valid until the next fetch it is the same ~200 B per row. The
cost therefore falls on the store used in unit tests and on the store that chose
to borrow, not on the ordinary production store — which is the right way round,
and it is what buys §UC-061's property against an aliasing codec and A4's freedom
to keep and write into a page it was handed (§INV-021).

**`store.Transaction(ctx)` runs on every `Load` and every `Append`.** It must be
a context lookup, a type assertion and a struct fill, and must never issue a
statement, touch a backend, open anything, allocate a token or consult a map: an
authority *is* its transaction's identity, so there is nothing to mint and nothing
to memoise (§INV-028). What it retains is one pointer to a `*sql.Tx` for as long
as the receipt lives, and a committed `*sql.Tx` has already released its driver
connection, so that is a few words and nothing held open. The suite tests it with
a recording store: the statement sequence of an append under a caller transaction
contains no begin, no commit and no extra round trip. It is the price of
`ErrAmbientNotTransaction` and `ErrTransactionMismatch` existing at all, and the
alternative is a write that escapes a transaction in silence.

**`Bind` costs four constant-time checks and one allocation** (§D.4), which is
what makes a store per request over a borrowed tenant lease ordinary (§UC-055).

### D.14 The store contract, from the implementer's side

This is A5's whole surface. If it is not here, a store neither implements it nor
may rely on it.

```go
package event

// ─── the identity vocabulary a store decomposes, constructs, mints and compares ───

type Key      string   // one aggregate identity, rendered by the declared mapper
type Version  uint64   // dense over one stream from 1; 0 is "never appended to"
type Position uint64   // store-wide, strictly increasing, gaps permitted
type Cursor   string   // store-minted, opaque, persistable; no ordering defined

type Stream struct {
    Family string
    Key    Key
}

func (this Stream) String() string   // "[stream accounts.account]" — never the key

// ─── the contract ────────────────────────────────────────────────────────────

type Log interface {
    Capabilities() Capabilities
    Limits() Limits
    Backing() Backing
    ReadAll(ctx context.Context, after Cursor) ([]Envelope, Cursor, error)
}

type Store interface {
    Log

    // Which of THIS store's transactions the context carries.
    //   valid   authority, nil error  → a transaction of this store's backing
    //   invalid authority, nil error  → nothing of this store's is bound; autocommit
    //   invalid authority, an error   → something of this store's that is not a
    //                                   transaction; the kernel refuses, never autocommit
    // Those are the only three; a CLOSED store answers the second, not an error,
    // and the ErrClosed comes from the operation that follows (UC-047, INV-045).
    // Issues no statement, touches no backend, opens nothing, mints nothing and
    // memoises nothing. Called on every operation. Two calls that resolve to one
    // LIVE transaction must answer Same; two LIVE transactions must not. Nothing
    // is claimed about a transaction that has ended (INV-028).
    Transaction(ctx context.Context) (Authority, error)

    ReadStream(ctx context.Context, s Stream, after Version) ([]Envelope, error)
    Append(ctx context.Context, req AppendRequest) error
    Close() error
}

type AppendRequest struct {
    Stream   Stream   // already validated by the kernel
    Expected Version  // the token's version; 0 means "never written", never "any"
    // The kernel's own slice, and the kernel reads it again: read it for the
    // duration of the call, never write into it, never retain it past the
    // return. Keeping rows for later means taking your own slice (INV-021.6).
    Records  []Record // already encoded, already inside every bound
}

type Record struct {
    Type     string
    Revision int
    // The kernel's own array, and the kernel reads it again: read it, clone it
    // if you retain it, and never write into it, not even temporarily
    // (INV-021.3).
    Payload  []byte
}

type Envelope struct {
    Stream     Stream
    Version    Version
    Position   Position
    Type       string
    Revision   int
    // Yours to give away: from the moment it is returned it belongs to whoever
    // received it, indefinitely and from any goroutine, and that party may write
    // into it. Clone what you retain AND what you do not own — a driver buffer
    // valid until the next fetch, an mmap'ed page, a library scratch — and never
    // reuse a buffer across two calls (INV-021.4). The same holds for the
    // []Envelope itself (INV-021.7).
    Payload    []byte
    RecordedAt time.Time
}

type Limits struct {
    MaxPayload int
    MaxBatch   int
    MaxKey     int
    StreamPage int
    MaxRead    int
}

type Support uint8
const (
    Unstated Support = iota   // the zero value; Bind AND Read refuse it (INV-043)
    Unsupported
    Supported
)

type Capabilities struct {
    Transactions       Support
    Persistence        Support
    MonotoneVisibility Support
    SharedBacking      Support
}

type Backing struct{ /* opaque */ }
// identity must be comparable and not nil by any route: a nil interface, and a
// nil pointer, map, slice, channel or func inside one, are all refused. The
// second half is this package's own predicate, written here over reflect,
// because crud.SameDataSource answers true for (typedNil, typedNil) and crud's
// own equivalent is unexported (INV-016).
func NewBacking(identity any) (Backing, error)
func (this Backing) Equal(other Backing) bool   // crud.SameDataSource; never ==
func (this Backing) Valid() bool                // crud.SameDataSource(id, id)
func (this Backing) String() string             // a bracketed classification, never the identity

type Authority struct{ /* opaque */ }
// transaction is the store's own executor value for THIS transaction — for a SQL
// store the *sql.Tx crudsql.Transaction returned. It must be comparable and not
// nil by any route: a nil interface, and a nil pointer, map, slice, channel or
// func inside one, are all refused here (INV-016, INV-028).
func NewAuthority(over Backing, transaction any) (Authority, error)
func (this Authority) Same(other Authority) bool  // backings Equal, then identities Same
func (this Authority) Valid() bool
func (Authority) MarshalJSON() ([]byte, error)  // always refuses

// ─── the one value that travels from the store to the kernel ─────────────────

// What a store says about a failure of its own. It is the whole of the
// store-to-kernel error channel: a store adds no sentinel of its own (2.3) and
// selects a row of the kernel's table instead.
type Outcome uint8
const (
    Unclassified Outcome = iota // the zero value: the door's fail-safe default
    Conflict                    // the stream was not at AppendRequest.Expected
    NotWritten                  // the write certainly did not land
    Unconfirmed                 // the write was issued and never confirmed
    Closed                      // this store refused rather than tried
    BadCursor                   // not minted over this backing, unparseable, or retired
)

// Wraps cause so errors.Is reaches it while INV-025 keeps its text from
// travelling. A nil cause is legal and means the store has nothing to add. The
// kernel finds the Outcome with errors.As, so a decorator may wrap this (UC-048).
func Failure(outcome Outcome, cause error) error
```

**The classification channel, and the map is the whole of it.** A contract that
requires a store to make four classifications the kernel cannot make for itself,
forbids it to add a sentinel and declares no vocabulary for the value that travels
on **every** method is one no second store can satisfy (GAP-67). `Outcome` and
`Failure` are that vocabulary and this is the map — total, fixed, and the only
thing the kernel reads (§INV-045):

| What the store returns | What the caller gets |
|---|---|
| `Failure(Conflict, cause)` | `ErrConflict`, write class, class-wrapping `crud.ErrConflict`, with the cause reachable and its text not travelling |
| `Failure(NotWritten, cause)` | `ErrBackend`, store class, class-wrapping the framework's retryable class exactly when the cause carries it — which is `errs/sqlerr`'s job and needs no second dial |
| `Failure(Unconfirmed, cause)` | `ErrUncertain`, write class, class-wrapping **nothing**: there is no honest framework class for "maybe" (§UC-034) |
| `Failure(Closed, cause)` | `ErrClosed`, store class |
| `Failure(BadCursor, cause)` | `ErrCursor`, store class |
| `Failure(Unclassified, cause)`, or **any error that is not a `Failure`** | the door's fail-safe default: `ErrUncertain` from `Append`, `ErrBackend` from `ReadStream` and `ReadAll` (§UC-060, §INV-045) |
| a bare `context.Canceled` / `context.DeadlineExceeded`, not wrapped in a `Failure` | itself. This is the one override of the defaults above, and it is §UC-057's first window; the second window is `Failure(Unconfirmed, ctx.Err())` |
| a non-nil error from `Transaction` | `ErrAmbientNotTransaction`, wiring class, wrapping it. **That method has exactly one failure it may report** — *something of mine is bound and it is not a transaction* — so its error carries no classification and the kernel needs none |
| a non-nil error from `Close` | nothing: `Close` refuses nothing and returns nil every time (§UC-047) |

**How many doors there are, and which one the map is scoped by.** A *door* here
is a method that can hand the kernel an error, and there are four —
`Transaction`, `ReadStream`, `ReadAll` and `Append`. **`Close` is not one**, and
its row in the map above is what says so: it returns nil every time by contract.
`Capabilities`, `Limits` and `Backing` have no error return at all. (Four is a
different count from §INV-022's two, which is about the two calls a store *value*
enters the kernel by.) A door decides exactly one thing: **what an unclassified
error means**. `Transaction`'s answer is fixed by its single meaning; the other
three take the fail-safe default of the row above. A **classified** outcome is
mapped the same way at every door, because the store is the only party that knows
what happened and a kernel that filtered a classification by door would be
inferring the thing §INV-045 forbids it to infer — a store that returns
`Failure(Conflict, …)` from a read is lying, with the same non-remedy as one that
says *not written* about a write it issued.

**A closed store must not report closure from `Transaction`.** That method issues
no statement, so it has nothing to refuse; a closed store answers the **invalid
authority and no error**, and the `ErrClosed` comes from the door that would have
written or read (§UC-047). Without that clause the ordinary defensive
implementation — a `closed()` guard first in every method — turns a `Load` after a
`Close` into a wiring-class `ErrAmbientNotTransaction`, while the `lifecycle`
section asserts `ErrClosed` (GAP-84).

**The kernel finds the outcome through wrapping, not by a type assertion.** It
uses `errors.As`, so a forwarding decorator that wraps a store's error with its
own context keeps the classification — which is what §UC-048 blesses and what
[ES]'s obligation 7 requires of it. A type assertion would make a wrapped
`Failure(BadCursor, …)` take a door's fail-safe default and become `ErrBackend`:
one class converted into another **by the kernel**, which is the conversion the
document forbids a wrapper to perform, and which would make §INV-030's "the exact
outer value always answers" true of the method and false of the error.

**Two things the map is deliberately not.** It is not a policeman: nothing catches
a store that says *not written* about a write it issued, or *conflict* about a
read, because those are lies and no kernel can see one — the `store failure
classification` section is what a store runs against itself. And it is not
extensible by a store: `Outcome` is closed, `Failure` is the only constructor, and
a store that wants a sixth classification has found a phase-2 contract change
rather than a spelling.

**How a package other than `event` obtains each of these** is §D.15's per-value
walk, written once there because that is what §INV-019's zero-diff claim is
evaluated against. Rounds 4 and 5 left five of those values unwritten, so the
claim could not be evaluated at all (GAP-57, GAP-67).

**Why exported fields on `Stream` are safe.** A `Stream` a caller constructs has
nowhere to go through the kernel, and the contract's data structs are the **store
seam** rather than the caller seam; §INV-015 argues it.

**Eight methods, and the split is the Interface Segregation answer to the count.**
`Log` is four, separately held by a consumer that must not append (§UC-036); the
write half is four. `architecture.md`'s ≤ 7 applies to a class, and this is a port
with two independently consumable halves.

**Division of labour, and it is total.**

| The kernel does | The store does |
|---|---|
| sealing, folding, upcasting | expected-version admission |
| encoding and decoding through the **declaration's** codec | dense version assignment |
| every bound in `Limits()` | positions and cursors |
| key validity and the text rule | atomicity of one append |
| the backing comparison **and the change-to-token stream comparison** (§INV-044) | the recorded instant, from its injected clock |
| the transaction marker, its selection by backing, and the three-answer rule | honest capabilities and limits |
| the refusal vocabulary, its classes, and the map from `Outcome` to a row of it | capping `ReadAll` at `MaxRead` and `ReadStream` at `StreamPage` |
| returning the **zero** state on a failed load, never the accumulator | classifying its own failure through `Outcome` |
| freezing `Encode`'s output at `New` and cloning a `Change`'s frozen bytes before each decode — its two of §INV-021's seven hand-offs, the third being the one it pays nothing for | handing out an `Envelope.Payload` and a `[]Envelope` **nobody will ever write**, and never writing into or retaining a `Record.Payload` or a `Records` slice it was handed — its four of the same seven |

**Per method: what may be refused, what must never be converted, what is left to
the kernel.**

- **`Capabilities`, `Limits`, `Backing`** — pure and **stable for the store's
  life**: the same value on every call, computed from what the store writes to
  and not from the store value. They refuse nothing and issue nothing. A
  decorator returns the wrapped store's values and never one of its own
  (§UC-048).
- **`Transaction`** — the three answers above and no fourth. Issues no statement,
  and **mints and memoises nothing**: the authority carries the transaction's own
  identity, so two calls that resolve to one live transaction answer `Same`
  without the store remembering anything, and a savepoint answers its parent's
  because `crudsql`'s savepoint returns the parent `*sql.Tx` (§UC-049, §INV-028).
  Two live transactions must not answer `Same`. Its error is **unclassified by
  construction**: it has exactly one meaning, so it is returned bare and the kernel
  maps it to `ErrAmbientNotTransaction`. It reports **nothing else** — in
  particular not closure, for the reason above.
- **`ReadStream`** — returns envelopes at ascending versions strictly above
  `after`, at most `Limits().StreamPage` of them, and a short page means "no
  more at this bound" only when it is genuinely the end. **Everything it returns
  it gives away**: the `[]Envelope` and every `Payload` in it belong to whoever
  receives them, for as long as that party keeps them, read from any goroutine and
  written into if it likes. The question is therefore never *will I read this
  again* but *will anybody write it*, so a store clones what it **retains** and
  what it **does not own** — a driver accessor's buffer valid only until the next
  fetch, an `mmap`ed page, a library scratch — and reuses neither the payload
  buffers nor the slice across two calls (§INV-021's fourth and seventh, §UC-061);
  that is what lets the kernel decode without cloning. Refuses only for
  cancellation (bare), a closed store (`Failure(Closed, …)`), or a backend failure
  — classified, or unclassified and read as `ErrBackend`. Never decodes, never
  inspects a payload, never knows a type name means anything.
- **`ReadAll`** — returns envelopes at ascending positions after the cursor, at
  most `Limits().MaxRead`, plus a cursor that is a **safe resume point**
  (§INV-035), under the same ownership rule as `ReadStream` — and it is the method
  where breaking it is visible, because a `*Reader` hands this slice straight to a
  consumer that §INV-038 lets fan it out to workers and keep it across the next
  `Next`. Refuses a cursor minted over another backing, an unparseable one,
  or one in a format it no longer accepts, with `Failure(BadCursor, …)`, which the
  kernel maps to `ErrCursor`. "Minted" means **this backing**, never this store
  value: in a new process the value that minted it does not exist (§UC-053).
- **`Append`** — admits the batch if and only if the stream's committed version
  equals `req.Expected`; writes the rows and advances the version in one atomic
  unit; assigns dense versions and strictly increasing positions; records the
  instant from its injected clock. Returns **only an error**, and every failure it
  returns names its `Outcome`: `Conflict` for a stream that was not at
  `req.Expected` — a unique violation on (family, key, version) is how a SQL store
  learns it — `NotWritten` for a failure it knows did not land, `Unconfirmed` for
  one it issued and never confirmed, `Closed` for a store that refused rather than
  tried. An unclassified error is read as `Unconfirmed`, which is why a store that
  can tell must (§UC-060). **`req.Records[i].Payload` is the kernel's array and
  the kernel reads it again** — every later fold of that `Change`, and every later
  append of the same slice, which is legal (§D.3). Read it, persist from it, clone
  it if you keep it beyond the call, and **never write into it, not even
  temporarily**: compressing, encrypting, redacting or padding in place rewrites a
  fact that was already decided, so the fold §D.5's two-decision shape runs
  *after* the append disagrees with a reload, with no error anywhere, and
  re-appending the same slice records the mutation (§INV-021's third hand-off,
  §UC-063). A store that wants transformed bytes transforms a copy. **`req.Records`
  is the kernel's slice under the same rule one level out**: read it for the
  duration of the call, never write into it, never retain it past the return, and
  take your own slice if you keep rows for later (§INV-021's sixth). It must not
  retry, chunk or deduplicate, and must not convert a cancellation or an unknown
  commit into another class.
- **`Close`** — idempotent, returns nil every time, refuses nothing, and closes
  nothing it did not open. It **neither commits nor rolls back** staged work, and
  what becomes of that work depends on which of §UC-047's two staging shapes the
  store has; the property true of both is that a close makes no staged work
  visible and commits none (§INV-032).

**Compatibility, made concrete.** The contract is frozen at the end of phase 1.
Growth is additive by **new optional interface**, never by a method on an existing
one — a method added to `Store` breaks every store, and an optional interface must
arrive together with the [[D-061]] discipline (`Next()` on every decorator, a
named bounded unwrap, a `(capability, bool)` answer). **`Outcome` grows like the
sentinel set and for the same reason**: adding a value is additive, because no
store returned one that did not exist; removing one, or changing what it maps to,
is breaking. Structs grow by field, and a field whose false value would skip a
conformance section arrives as a `Support`, not a `bool` (§INV-043, §INV-024).

### D.15 The zero-diff obligation, and phase 2's proof

This is an **obligation**, not an aside: §INV-019 is the phase's defining
architectural test and §1 states what `eventpg` must and must not write. Here is
the walk that shows the claim is true, value by value.

```go
package eventpg

type Store struct {
    db      *sql.DB
    source  crud.Source
    limits  event.Limits
    backing event.Backing
}

func New(spec Spec) (*Store, error) {
    // Verifies the settings row the data depends on: MaxPayload, MaxKey.
    // jobspg's managed/verified start-up split, unchanged ([[D-101]]).
    backing, err := event.NewBacking(crud.KeyOf(spec.Source))
    if err != nil {
        return nil, err                               // a nil or non-comparable source
    }
    return &Store{
        source:  spec.Source,
        limits:  event.Limits{MaxPayload: 64 << 10, MaxBatch: 64, MaxKey: 512, StreamPage: 256, MaxRead: 256},
        backing: backing,
    }, nil
}

func (this *Store) Backing() event.Backing { return this.backing }
func (this *Store) Limits() event.Limits   { return this.limits }
func (this *Store) Capabilities() event.Capabilities {
    return event.Capabilities{
        Transactions:       event.Supported,
        Persistence:        event.Supported,
        MonotoneVisibility: event.Unsupported,
        SharedBacking:      event.Supported,
    }
}

// No closed() guard here, deliberately: this method reports exactly one failure
// and a closed store answers the invalid authority with a nil error, so the
// ErrClosed comes from the Load or Append that follows (D.14, UC-047).
func (this *Store) Transaction(ctx context.Context) (event.Authority, error) {
    executor, ok := crud.ExecutorFor(ctx, this.source)
    if !ok {
        return event.Authority{}, nil                 // nothing of mine: autocommit
    }
    tx, ok := crudsql.Transaction(executor)
    if !ok {
        return event.Authority{}, errNotATransaction  // one meaning, so it is bare;
                                                      // the kernel refuses, never autocommit
    }
    return event.NewAuthority(this.backing, tx)       // the transaction IS the identity
}

func (this *Store) ReadStream(ctx context.Context, s event.Stream, after event.Version) ([]event.Envelope, error) {
    rows, err := this.query(ctx, s.Family, string(s.Key), uint64(after), this.limits.StreamPage)
    if err != nil {
        return nil, this.classify(err)                 // one call, and it is below
    }
    return append(out, event.Envelope{
        Stream:     event.Stream{Family: family, Key: event.Key(key)},
        Version:    event.Version(version),
        Position:   event.Position(position),
        Type:       typeName,
        Revision:   revision,
        // No clone because the scan ALLOCATED this slice for this row and
        // nothing else will ever write it -- not because this store does not
        // keep it. Scanning into a driver accessor whose buffers are valid
        // until the next fetch (sql.RawBytes, a pgx RawValues-shaped API) is
        // the same predicate answered the other way and needs bytes.Clone
        // here, and a fresh []Envelope per call (INV-021.4, INV-021.7).
        Payload:    payload,
        RecordedAt: recordedAt,
    }), nil
}

func (this *Store) Append(ctx context.Context, req event.AppendRequest) error {
    // req.Stream.Family and string(req.Stream.Key) are two columns;
    // uint64(req.Expected) is the admission predicate; versions and positions
    // are assigned by this store and converted on the way back out.
}

// The whole of the store-to-kernel error channel, in one method this store owns
// and both write-and-read doors call. errs/sqlerr turns a driver error into a
// code; this store turns a code into the one thing only it can say. The conflict
// and unconfirmed arms cannot fire on a read because only an append issues a
// write, which is a property of THIS store and not of the map — the kernel maps a
// classified outcome at whatever door it arrives (INV-045). Nothing here needs
// event-internal access.
func (this *Store) classify(err error) error {
    switch {
    case this.closed():
        return event.Failure(event.Closed, err)
    case uniqueOnStreamVersion(err):
        return event.Failure(event.Conflict, err)
    case issuedAndUnconfirmed(err):                     // a connection lost mid-commit
        return event.Failure(event.Unconfirmed, err)
    default:
        // retryable rides the cause: this store's own helper over errs/sqlerr
        // gives the cause the framework's retryable class when the code says so.
        return event.Failure(event.NotWritten, this.classified(err))
    }
}
```

Every type in that sketch is either `eventpg`'s own or a plain `event` struct,
defined string, defined integer or exported constant. Nothing is minted through a
call `event` would have to grow — including the error, the one value on the seam
neither GAP-44 nor GAP-57 enumerated (GAP-67).

**Where every returned value comes from.**

| What `eventpg` must return | How it produces it, from its own package |
|---|---|
| `Envelope`, `Limits`, `Capabilities`, `Record`, `AppendRequest` | plain structs with exported fields, filled directly, no constructor |
| `Stream` | a struct of two exported fields: decomposed into two columns on the way in, `event.Stream{Family: f, Key: event.Key(k)}` on the way out |
| `Key` | a defined string type: `event.Key(column)` and `string(s.Key)` |
| `Version`, `Position` | defined `uint64`s: converted from and to the store's own columns, compared and assigned by the store |
| `Cursor` | a defined string type; `eventpg` mints `"v1:" + watermark`, embeds the database's `system_identifier` so a cursor from another database is refused, and parses only its own format |
| `Backing` | `event.NewBacking(crud.KeyOf(spec.Source))`, which returns an error for a nil or non-comparable identity and is checked in `New`; a transaction-bound derivation and a second `New` over one source compare `Equal`, and two databases do not |
| `Authority` | `event.NewAuthority(this.backing, tx)` — the `*sql.Tx` itself, so nothing is minted, nothing is memoised and there is nothing to evict; forging one buys nothing (§INV-028) |
| `Support`, `Outcome` | exported constants |
| a classified `error` | `event.Failure(outcome, cause)` — an exported constructor over an exported closed enum. `errs/sqlerr` turns the driver error into a code, `eventpg` turns the code into an `Outcome`, and the kernel turns the `Outcome` into a row of §2.3. The retryable class rides the wrapped cause, so there is no second dial for it (§INV-045) |
| the *unknown commit* classification specifically | `event.Failure(event.Unconfirmed, err)`. It is worth its own row because it is the one classification the framework has no code for anywhere: `errs` has codes for a unique violation and a serialisation failure and **none that means "the outcome is unknown"**, which is exactly why the channel had to exist rather than being inferred from `sqlerr` (GAP-67) |

**Nothing sealed crosses the store boundary in the store's direction.** `At[S]`
never reaches a store — the kernel unpacks it into `AppendRequest.Expected` — and
`Commit` never comes from one, because `Append` returns only an error. Those are
the two values whose unconstructibility carries weight, and neither needs a
minting call. **`eventpg` imports** `event`, `crud`, `crudsql`, `errs/sqlerr` and
`database/sql`.

**`event` imports the standard library and two first-party packages, and this is
the whole list.** It is stated as a contract because Q4 and Q11 are settled
against it and `make check-deps` measures it.

| Import | What for, exhaustively |
|---|---|
| the standard library | `context`, `errors`, `fmt`, `bytes` (`Clone`, at `Fact.New`'s freeze and at `Fold`'s pre-decode copy — the two hand-offs §INV-021 answers with a copy inside the kernel), `reflect` (the nil-identity predicate of `NewBacking`/`NewAuthority`, which restates `crud`'s unexported one because no package can reach it — §INV-016, GAP-85 — and nothing else), `strings` (`Builder`, in `Compose`'s frozen rendering), `unicode` and `unicode/utf8` (the kernel text rule, and the escape set `Compose` reads through it), `encoding/json` and `encoding` (the shipped codec and its encodability walk), `sync` and `sync/atomic` (the declaration's seal and the binding's family table), `testing` in `eventtest` only (Q9), `time` for the recorded instant — the first three added by [PLAN] S6, which found the row omitted what `Compose` and the text rule already needed |
| `crud` | **three** symbols and no more. `crud.SameDataSource`, which is `Backing.Equal`, `Backing.Valid` and half of `Authority.Same` (§INV-016, §INV-028). And the two error classes that two of §2.3's four declared class wraps name: `crud.ErrConflict` and `crud.ErrUnavailable` (Q1). It does **not** name `crud.ExecutorFor`, `crud.Source` or anything else in the executor vocabulary — a store resolves its own executor inside its own package (Q4) |
| `errs` | the **third** class wrap: `ErrTooLarge` carries the framework's too-large class, and the tree has no too-large sentinel, so it is an `*errs.Fault` with `errs.KindTooLarge` (Q1, §UC-017). It is named here because a wrap list of four that omits the package one of them lives in is how GAP-58's fix reintroduced GAP-58 at half scale (GAP-75) |

The fourth class wrap — `ErrUpcast` over the application's own error — needs no
import, because the application's error arrives as a value. §INV-025's opaque
rendering cites `cache/errors.go` as a **shape** and not as an import. The import
is what resolves GAP-58: "are these two things writing to the same place" is a
question the framework answers once, `crud` and `errs` are first-party and cost no
`require` line, and `tenancy/errors.go` already imports `crud` on exactly the
terms [[D-116]] argued.

**Its conformance test is `eventmemory`'s, with a different factory:**

```go
func TestPostgresStoreSatisfiesTheContract(t *testing.T) {
    eventtest.Run(t, eventtest.Factory{
        New: func(t *testing.T) event.Store { return open(t, dsn) },
        Begin: func(t *testing.T, ctx context.Context, s event.Store) (context.Context, eventtest.Tx) {
            pg := s.(*eventpg.Store)
            tx := beginOrFatal(t, pg)   // t.Fatalf names the store that refused
            return crud.WithExecutorFor(ctx, pg.Source(), tx), tx
        },
        Sibling: func(t *testing.T, s event.Store) event.Store { return open(t, dsn) },
        Fail:    func(t *testing.T, s event.Store, outcome event.Outcome) bool { … },
    })
}
```

**How the obligation is held rather than promised.** By the two mechanisms
§INV-019 states: the empty `git diff` under `event/` as an arm of `make check`,
and the trivial store `eventtest` builds in its own test package — which compiles
today or the seam is wrong today.

### D.16 The extension point, against `microkernel.md`'s four parts

| Required part | Where it is |
|---|---|
| **A contract** — a protocol with a stable typed data shape in **both** directions | §D.14. Round 5 wrote out the data types its signatures name — `Stream`, `Key`, `Version`, `Position` — and **round 6 finished the other direction**, which is the one value that travels on every method: a classified `error`, spelled `Outcome` and `Failure` (§INV-045, GAP-67). Every value in both directions is now a plain struct, a defined string, a defined integer, a `[]byte`, an exported constant, an error built by an exported constructor over an exported enum, or an opaque value with an exported minting constructor, and §D.14's two tables say how a package other than `event` obtains each. **No method of `Store` or `Log` names an `any` or a type parameter**; the only `any` in the whole seam is the identity a store hands `NewBacking` and `NewAuthority`, which is the store's own value and is compared, never inspected |
| **A registration mechanism** — explicit, at the composition root, greppable | `event.Open(store)` then `event.Bind(binding, aggregate)`. No registry, no import side effect, no discovery. Two lines a grep finds |
| **A failure policy** — what happens when an extension raises, times out or returns garbage | §D.14's `Outcome` map, which is total, is found through a decorator's wrapping with `errors.As`, and has a fail-safe default at each of its four doors (§INV-045); §UC-057's two windows; §INV-043's unstated-capability refusal; §D.4's four `Bind` checks; and the panic discipline, which is now stated per callback rather than as one word — an upcaster's panic is recovered into `ErrUpcast`, a fold's is not recovered at all (§INV-017, §UC-067). A store that returns garbage fails a named conformance section rather than corrupting a caller |
| **A version/compatibility note** — what may change and what may not | §D.14's last paragraph, which covers methods, structs, `Support` and `Outcome`, plus §INV-024's three-sentence rule for the sentinel set |

**[ES]'s ten capability and wrapper obligations, each with a home.** 1 →
§UC-048's forward-exactly-once; 2 and 3 → §INV-030; 4 → §UC-048's
before-the-forwarded-call clause; 5 → §UC-048 plus §INV-043; 6 → §UC-048's
replay/buffer/re-inspect clause, extended by §INV-021 to in-place writes and to
retaining a `Records` slice; 7 → §INV-029 plus §UC-018 and §UC-057; **8 and 9 →
vacuous, and non-goal 21 is where that is argued** rather than left to a reader
to infer from silence; 10 → §9 item 9 plus §D.16's inventories.

| `architecture.md` metric | This design |
|---|---|
| kernel imports of a concrete extension | 0 — `event` names no store package |
| diffs to kernel files to add a store | **0**, and it is checked (§INV-019) |
| type switches over extensions in the kernel | 0 |
| public methods per type | `Store` is 8 as a **port** with two consumable halves of 4; every other exported type is ≤ 6 |
| can the object move to its own package and be tested with ≤ 2 fakes | `At[S]`, `Change[S]`, `Commit`, `Backing`, `Authority` need **zero** fakes — they are inert values. `Repo[S, ID]` needs **one**: a `Store`. `Reader` needs **one**: a `Log` |
| **public symbols exported by the package** | `event` **≈ 71**, against a threshold of 7, and the breach is deliberate and argued below. `eventmemory` is **8** (`Log`, `LogSpec`, `Store`, `Spec`, `Tx`, `NewLog`, `New`, `WithTransaction` — the `Tx` is counted, because a caller must name it). `eventtest` is **6** (`Run`, `Factory`, `Tx`, `RoundTrip`, `Keys`, `Families`) |

**The exported-symbol breach, justified in writing because `architecture.md` says
a breach must be** (GAP-77). The count is roughly 13 functions, 27 types, 9
constants and 22 sentinels. The ≤ 7 threshold is a metric over a **class**, where
every extra method is another way for one object to be inconsistent. `event` is
not a class but the **vocabulary of a subsystem**, and three of the four groups
exist precisely so that nothing has to be discovered at runtime:

- **The 22 sentinels are a partition a caller matches on** (§2.3). Collapsing them
  puts a string or a code where `errors.Is` is now, which is [[D-015]]'s defect.
- **The 27 types are the two seams** — the caller seam (`Aggregate`, `Fact`,
  `Chain`, `Codec`, `Change`, `At`, `Commit`, `Repo`, `Binding`, `Reader`,
  `Declaration`) and the store seam (`Store`, `Log`, `AppendRequest`, `Record`,
  `Envelope`, `Limits`, `Capabilities`, `Support`, `Outcome`, `Backing`,
  `Authority`, `Stream`, `Key`, `Version`, `Position`, `Cursor`) — and §INV-019
  requires every store-seam type to be **constructible from another package**,
  which is what forces them to be exported. A smaller count is a smaller
  *extension point*.
- **The 13 functions are the declaration idiom and the two minting constructors:**
  `Define`, `Declare`, `From`, `Then`, `JSON`, `Compose`, `Open`, `Bind`,
  `ReadOnly`, `Read`, `NewBacking`, `NewAuthority`, `Failure`. Every one has a
  named reader here; none is a "for later" API, which is the rule
  `restrictions.md` §1 cares about.

The per-type measurement is not breached: no exported type has more than 8
methods, and the one with 8 is a port with two independently consumable halves.
The only honest lever for a smaller count is fewer *concepts*, and each group
above names what that would cost.

---

## 7. Open questions — the reconciliation agenda

These are decisions this document deliberately does not close. Each names what it
would change and what the recommendation costs if the owner picks the other way.
Numbers are stable from round 1; two were dissolved outright and one added, and
later rounds rewrote statuses without renumbering anything.

| # | Question | Status |
|---|---|---|
| **Q1** | Which framework symbol carries the conflict, too-large and retryable classes, may `event` import it, and do the structural checks stay green? | **Answered, pending the owner's assent.** `crud.ErrConflict`, `crud.ErrUnavailable` and an `*errs.Fault` with `errs.KindTooLarge`, which is why `event` imports `errs` as well as `crud`; §D.15's import table is the enumeration. `tenancy/errors.go` is the precedent and [[D-116]] is the argument: `crud` is the tier "whose error taxonomy decides what a refusal renders as". `check-deps` measures third-party weight and sees nothing, and §2.3's partition gives this a **per-class** list to settle rather than a per-row one |
| **Q2** | Numbering and placement of the repository-facing docs this phase owes | Open. The `UC-nnn`/`INV-nnn` numbering in this file stays internal to it and is not carried into `docs/` |
| **Q3** | Name collisions across the tree | **Answered.** Every name this document uses is either free or qualified at the call site, and [[D-035]]'s rule is that a package-level collision is not a collision. Two worth a second look were found and both are already taken account of: the memory store must not name a `Limits` beside `event.Limits` (it names a `Spec`), and the aggregate declaration is `Aggregate`, not `Type`, because `reflect.Type` sits beside it in a file that does both. Round 5's four written-out identity types — `Stream`, `Key`, `Version`, `Position` — are all qualified at every call site and none collides inside `event`; `event.Key` beside `crud.KeyOf` is [[D-035]]'s non-collision exactly. Round 6 adds `Outcome`, `Failure` and six constants, and the two worth naming are deliberate rather than accidental: `event.Conflict` (an `Outcome`) sits beside `event.ErrConflict` (a sentinel) and `event.Closed` beside `event.ErrClosed`, which is the point — the constant *selects* that sentinel, so the echo is the map (§D.14). Neither is a Go collision, and `event.Failure` beside `jobs.HandlerFailure` is two packages' own words for two different things |
| **Q4** | The transaction binding's concrete mechanism | **Answered.** The *question* the kernel asks is a **required** `Store` method (§D.14), so nothing is discovered and nothing can be missing. What stays store-private is the **binder**: `crud.WithExecutorFor(ctx, source, tx)` for a SQL store, `eventmemory.WithTransaction(ctx, tx)` for the memory store. `event` names no executor vocabulary at all — a store resolves its own executor inside its own package — and §D.15's import table is what it does name |
| **Q5** | Empty append: a no-op with a visibly empty receipt, or a refusal? | Open, and the document takes the no-op. The tree agrees where it has the same question (`crud.UnsafeBulkInsertFor` returns `(0, nil)` for zero rows). The cost is §INV-002's carve-out, one sentence; the cost of the other answer is an `if len(changes) == 0` branch in every call site, which is the boilerplate [[D-021]] exists to delete |
| **Q6** | Should the reader chain state its revision numbers redundantly, for greppability? | **Answered: no**, and the tree is the counter-example that settles it. Because `jobs.JSON[V](version)` carries a hand-typed number, `jobs` needs sorting, a duplicate-source check, a contiguity check and a termination check — four validations that exist only because the number can be wrong. Greppability is bought back by a `Describe()` on the declaration |
| **Q7** | The paging shape | **Answered and simplified by round 4.** No page struct and no query struct: `ReadStream` returns `([]Envelope, error)` and `ReadAll` returns `([]Envelope, Cursor, error)`. An iterator was considered and refused — the resume cursor must come back *with* the page, and an `iter.Seq2` has nowhere to put it |
| **Q8** | ~~Does the per-commit durability class need a fourth value?~~ | **Dissolved.** The per-commit class is deleted: three values, no reader in phase 1, and no honest answer for an empty commit (GAP-53). What survives is `Capabilities.Persistence`, which the conformance suite actually reads |
| **Q9** | `*testing.T` in an exported non-test package, or a minimal interface? | **Answered: `*testing.T`.** `cache/cachetest/suite.go` already imports `testing` in a non-test file of the root module and `make check-deps` is green, because `testing` is standard library. A minimal interface loses `t.Run`, which is what makes a section name a failure's vocabulary |
| **Q10** | The numbers | **Mostly answered**, in §D.13, from `jobs/bounds.go` for the payload, identifier and revision caps, and from the stated product arithmetic for `MaxBatch`, `StreamPage` and `MaxRead`. What is still owed is a real consumer's measurement rather than a derivation |
| **Q11** | Does `event` belong in the structural checks' subsystem list and graph tests? | Open, with a recommendation: yes to `SUBSYSTEMS`, and a new per-extension graph test on `scripts/tenancy_test.go`'s model. The existing list already omits four subsystems, which is a separate finding and not this phase's work |
| **Q12** | Should the in-memory store have transactions, when neither `jobsmemory` nor `cachememory` does? | Open, and the document says **yes** with the asymmetry argued in §UC-040. The cheaper fallback is honest and available — declare `Transactions: Unsupported`, report the section skipped, and ship a `## Debt` item naming the ten use cases with no test |
| **Q13** | What proves "no goroutine started" and "no package-level mutable state"? | Open, **with the predicate corrected** so the work handed forward is writable. The goroutine half exists in the tree and is copied. The mutable-state half is an AST check over §INV-013's table, which targets **mutation** rather than kind for the reason stated there (GAP-63) |
| **Q14** | Is the aggregate's `ID` type parameter worth its cost? | **Answered: yes.** The tree pays the identical cost in the identical place — `crud.Core[M, ID]`, `port.Service[M, ID, U]`, `cache.Cache[K, V]` — and dropping it would make `event` the one subsystem where loading an order through the accounts repository compiles |
| **Q15** | What must the decision doc that supersedes [ES]'s "no root `event` package" say? | Open. It owes the module-boundary argument ([[D-116]]), the second-implementation argument ([[D-048]]), and an explicit answer to [ES]'s E0 blocker: the blocker is aimed at a live schema and an operational surface, and phase 1 builds neither |
| **Q16** | An append idempotency key | Open, and phase 1 ships none (§UC-035). If a consumer ever justifies one, the tree's shape is the answer: a **declared, typed, digested payload identity**, not a caller-supplied opaque string |
| **Q17** | Where a comparable cross-subsystem transaction identity would be minted | Open, and phase 1 must not decide it. The measured reason: `jobs.TransactionBinding` is fresh entropy per call, so it is not comparable even with itself across two calls on one transaction. A shared mint would be a change to `jobs` **and** a new dependency edge, which is two decisions in one. §UC-030's answer — both subsystems refusing — needs neither. Round 5 adds one datum for whoever takes it up: `event`'s authority is comparable **because it is the transaction rather than a token minted from it** (§INV-028), so the shared answer, if there is ever one, is a derivation and not a registry |
| **Q18** | The cursor's type | Open, and the document takes the defined string type with its cost stated (§UC-053, §D.12). Whoever wants the opaque struct must also say what the exported minting call it costs is worth |
| **Q19** | The family collisions `Bind` cannot see | Open. §INV-039 states the two escapes and their scope. Both have one candidate answer, which the tree already runs: a composition-root catalogue on `jobs.NewCatalog`'s model, seeing every declaration one application composes whichever store or binding each went through. It is **additive** to phase 1 and belongs in `## Debt` |
| **Q20** | ~~The spelling of the values the kernel reads from a store~~ | **Dissolved by half.** The codec moved to the declaration, so `Store.Codec()` is gone and the question shrinks to `Limits()`, which is settled: two methods, not one `Config()` bundle, because both are values the kernel *uses* on every operation and a bundle would be re-validated on every call |
| **Q21** | Panic-only declaration, or the tree's `Define`/`MustDefine` pair? | **Answered in [PLAN] and shipped as [[D-123]]: `Define`/`Declare` panic, `TryDefine`/`TryDeclare` return.** Neither of the two shapes this row named: the tree's own precedent is `crud/sqlrepo/blueprint.go:74:Define` / `:82:TryDefine`, which [[D-021]] already cites by line, and it keeps the short name for what an application writes at package level. The negative-test argument is what earned the sibling — twenty-two refusals asserted as a table instead of twenty-two `recover()` blocks — and §INV-013 survives because the panicking call has no body of its own |

---

## 8. Deferred

Carried here so the phase boundary cannot drop them. Each is a decision this
document could take and deliberately does not, because nothing else depends on
the answer and both branches are one line.

- **GAP-25 — a conformance defect does not fail one section, and round 9 found
  that the first relaxation was still too strong.** A store that ignores the
  expected version fails `expected version` **and** `concurrency`; the short-page
  decorator also fails `dense versions` and `conservation`; the position-reusing
  one fails whatever else compares positions. So neither *exactly one section* nor
  *at least one and no unrelated section* is true, and §UC-045 now ships the claim
  the suite can hold — **each defect fails the section named for it** — with the
  three exceptions listed there by name. Partitioning the sections so a stronger
  claim holds is left to when the suite is written.
- **GAP-41 — "a payload type that is not a struct" is a kernel rule about a
  codec's domain.** The argument for it is now written (§UC-004): a non-struct
  payload cannot gain a field, so it has no evolution path and the reader chain
  buys it nothing. What stays deferred is whether that is reason enough to
  refuse, rather than to let the codec answer as everything else codec-shaped
  does. Decided when the declaration checks are written.
- **GAP-53 — the empty receipt's accessors.** The two with no honest answer
  (`Durability`, `Positions`) are deleted and the one whose contract drifted
  (`Version`) is replaced by `First`, `Last` and `Count` (§D.5). What is deferred
  is only the wording of `Stream()` on a receipt for an append that never reached
  a store: it answers the token's stream, which is the only value it has, and
  nobody in phase 1 reads it. Confirmed when the receipt is written.

**GAP-26 left this list** when `Replay` was deleted and the one surviving fold
gained named causes (§UC-041). **Rounds 6 to 9 add nothing to it**: each took its
own deferred items rather than carrying them, including round 9's GAP-98 and
GAP-99. Three items remain, all from rounds 1–3, and none is load-bearing for
anything else in the document.

---

## 9. Debt this phase hands to the next

Named here, and not in `## Deferred`, because these are things a later phase
**does** rather than decides.

1. **`event/eventpg`**, with everything §1 and §D.15 say it must write and
   nothing they say it must not.
2. **The zero-diff check** wired into `make check` as §INV-019 requires, with the
   phase-1 tag it compares against.
3. **A composition-root catalogue** on `jobs.NewCatalog`'s model, if Q19's two
   residuals are judged worth closing. Additive; `eventtest.Families` is what an
   application runs until then.
4. **The decision doc** Q15 names, recording [ES]'s superseded half, the
   second-implementation argument, and the answer to its E0 blocker.
5. **`docs/modules/`, a flow, a repository use case and `make api`** for the
   three packages, in the same change as the code, in both language trees.
6. **A position range on the receipt and a projector**, when there is a reader
   for them (non-goal 20, non-goal 3).
7. **Whatever the owner decides for Q12**, including the `## Debt` item that
   answer owes if the cheaper fallback is taken.
8. **The savepoint half of §INV-028, in `eventpg`'s own package test** — that
   `crudsql`'s savepoint resolves to the parent's `*sql.Tx`, so an append inside a
   savepoint and one inside its parent carry authorities that are `Same`. Not a
   conformance section, because a savepoint is `crudsql` vocabulary and the memory
   store has none (§UC-049).
9. **An exported-surface baseline for the three packages**, which is what
   `make api` already is for the rest of the tree. [ES]'s tenth obligation asks
   for something that *fails when the seam grows*, and this document has method
   inventories and no baseline; after the phase-1 tag a line that disappears from
   it is a breaking change (§D.14's compatibility paragraph).

