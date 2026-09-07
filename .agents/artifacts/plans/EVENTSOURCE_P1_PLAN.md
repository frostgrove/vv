# EVENTSOURCE PHASE 1 — IMPLEMENTATION PLAN

Phase 3. Grounded in the real tree at `frostgrove/vv/framework`, commit `72e7d22`.

Inputs:
[EVENTSOURCE_P1_USECASES.md](../usecases/EVENTSOURCE_P1_USECASES.md) — "[SPEC]", 68 UC (UC-023 withdrawn), 45 INV (INV-037 withdrawn);
[EVENTSOURCE_P1_RECONCILE.md](../usecases/EVENTSOURCE_P1_RECONCILE.md) — "[REC]";
[EVENTSOURCE_P1_USECASES_GAPS.md](../gaps/EVENTSOURCE_P1_USECASES_GAPS.md) — "[GAPS]", ten rounds, closing with a ten-item **Carry into the plan** list;
`docs/roadmaps/2026-09-01-postgres-event-sourcing-roadmap.md` — "[ES]";
`docs/roadmaps/2026-09-01-extension-architecture-roadmap.md` — "[EXT]".

---

## What this plan delivers, and what it does not

Three packages of the **root module**, no new `go.mod`, no `go.work` edit, no
third-party dependency:

```
event/              the vocabulary, the aggregate seam, the store contract
event/eventmemory/  a complete, transaction-capable in-memory store
event/eventtest/    the exported conformance suite every store runs
```

PostgreSQL is phase 2. No SQL, no schema, no migration, no driver, no
telemetry, no container binding, no codegen. [SPEC] §1's twenty-one non-goals
stand unchanged and are not restated here.

**[SPEC] is the authority on shape.** Where this plan changes something [SPEC]
says, it says so in the row that changes it and again in `## Disagreements`.
Everything changed here is either a **carried gap [SPEC] deliberately handed
forward** (§ Carried gaps, ten rows) or a **question [SPEC] deferred to the
phase that writes the code** (GAP-41, GAP-53, Q12, Q21). Nothing else moves.

---

## Carried gaps — the ten [GAPS] round-10 items, decided

[GAPS] round 10 closed with ten constraints and never received a resolution
round; by `references/gaps.md` its three `[high][immediate]` findings block a
gate, and [REC] §5 is right that they are **contract-shaped**: they must be
closed here, in the section that writes the contract, not by whoever implements
`Outcome` first. Each row below is a decision, its argument, the section that
lands it, and the test that catches its violation.

### C1 — GAP-100 `[high]` A decoded value must not alias memory its codec will reuse

**Decided.** The obligation is the **codec's**, stated on the `Codec[V]`
interface: *`Decode` may return a value that aliases the input slice and may
return freshly allocated memory; it must not return a value that aliases memory
the codec itself will write or reuse.* No kernel copy of `E` — that is
§UC-052's deleted transitive copier walk arriving through a back door, and
§D.12 already rejects both "do not alias your input" (contradicts §INV-021's own
finding that aliasing decoders are ordinary) and a borrowed `E` (§UC-065 forbids
it in terms). Cost: **zero** — an obligation, not a copy.

§INV-021's outbound table gains **hand-off 8**: a codec → the kernel or a
caller, the value `Decode` returns; sender-owed; the sender is the codec.

*Runnable proxy* (this is what makes it not a sentence): `Fact.RoundTrip` decodes
the caller's payload for a revision **twice, from two separate input buffers**,
and refuses when the two answers hold one slice or map in common — a value that
aliases the payload it was handed aliases its own copy of it, and one that
aliases memory the codec keeps is the same address in both. Behind that it also
re-encodes the first answer either side of a decode of a **second, different**
payload of the same revision, and refuses if the two encodings differ.
`event.JSON` passes; a scratch-reusing codec fails, and so does the
length-prefixed one that reuses a buffer without clearing it, which the
disturbance arm alone reported clean (S2 round 2, GAP-180). **S1** (the clause) +
**S2** (the proxy).

**Where the second payload comes from, and what happens when there cannot be
one.** The second payload must be decoded by *that revision's own codec* — that
is the codec whose scratch buffer is under test — so it has to be a value of that
revision's reader type, and the signature supplies only one per revision. The
kernel manufactures it: **the encoding of that revision's zero value**, produced
by the erased `selfEncode` the chain already holds, so no `reflect` and no second
sample. Re-encoding the caller's own sample instead would produce identical bytes,
a scratch-reusing codec would write the same content twice, and the comparison
would pass — vacuously and silently, which is the one outcome this proxy exists to
prevent.

That leaves exactly one hole and it is closed rather than tolerated: when the
caller's sample **encodes to the same bytes as the zero value**, the two decodes
are the same decode again and the aliasing half tests nothing. `RoundTrip` then
returns `ErrSample` naming the revision, rather than a nil error. A zero-valued
sample proves nothing about *fidelity* either — it round-trips through any codec —
so the refusal costs the caller nothing it had. Sentinel count becomes **24**;
`ErrSample` is **request class**, beside `ErrEncode`, because the sample is the
caller's own argument. Comparing *encodings* rather than values is what makes the
detection total: it needs no comparability, and it also catches a sample that is
not literally zero and encodes as if it were.

*Test:* `TestACodecThatDecodesIntoAReusedBufferIsCaught` in `event/`, with
`event.JSON` in the same test as its control, and **two samples per case**: a
populated one, where the scratch-reusing codec must fail and `event.JSON` must
pass; and a zero-valued one, where **both** codecs must answer `ErrSample` — the
refusal is what stops the zero sample from reading as a pass. Round 2 added the
two cases that keep the proxy honest about the codecs it is *for*: a
length-prefixed codec that reuses one buffer and never clears it, with an
allocating codec of the same wire format beside it, and a codec that records
three bytes of a six-byte payload, with the shipped one beside it.

### C2 — GAP-101 `[high]` A policing decorator has no way to spell its own refusal

**This is the one contract element the plan adds.** [REC] and [GAPS] both
confirm it is kernel-side and additive, so §INV-019's zero-diff obligation
survives it: a store gains a constant it may return, and no `event` file has to
change for a second store.

**Decided — option (b): a seventh `Outcome` and a twenty-third sentinel.**

```go
Refused    Outcome = ...   // this store, or a decorator over it, refused as a
                           // matter of its own policy; nothing was written
var ErrRefused = ...       // §2.3 store class
```

`Failure(Refused, cause)` maps to `ErrRefused`, **store class**, wrapping no
framework class of its own and carrying `cause` reachable by `errors.Is` while
§INV-025 keeps its text from travelling. The token is **unchanged** — nothing
reached the backing — which is the whole difference from `ErrUncertain`.

*Why the store class rather than a seventh class.* Adding a class is breaking by
§2.3's own growth rule and a class is what a transport maps; adding a sentinel to
an existing class is additive. §2.3's store row already reads *"the store itself
refused or failed"*, and `ErrClosed` is already its **refused-rather-than-tried**
member. `ErrRefused` sits beside it.

*Why the kernel does not derive a transport class from the cause.* A policy
refusal's status is the policy's to state, and it states it in the cause: a quota
wrapper that wants a 403 wraps `crud.ErrForbidden`, one that wants a 409 or a 503
wraps `crud.ErrConflict` or `crud.ErrUnavailable`, one that wants any other of
`errs.Kind`'s ten builds an `*errs.Fault`. **Not 429** — this framework has no
rate-limit kind and no such status branch (GAP-145), and adding one is a change
to `errs` and `port` that `## Debt` carries. A kernel that re-derived it would be a fifth declared class
wrap for a value the kernel does not understand. For the cause to *arrive* —
`crud.ErrForbidden` travels by `errors.Is`, an `*errs.Fault` only by `errors.As`
— `ErrRefused` is the one sentinel whose declared wrap **is** its cause; see
`event/errors.go`, which states what the opaque wrapper answers for each
traversal and why the same permission is refused to every other sentinel.

*Rejected, on the record, so nobody re-proposes them in phase 2:*

| Rejected | Why |
|---|---|
| (a) a policy refusal is `Failure(NotWritten, err)` → `ErrBackend` | `ErrBackend` class-wraps the framework's **retryable** class when the cause carries it, and *the store itself failed* is what a transport reads as a 5xx. A tenant over quota is neither a failure nor retryable, so the caller's obligation and the transport's status are both wrong |
| (c) policy does not belong on the store seam; §UC-048 drops the quota example | The honest minimal answer and the one I would prefer (see `## Disagreements` D1), but §UC-048's [happy] path **is** the policing wrapper and [ES] obligation 4 states the before-the-forwarded-call rule for it. Removing the actor's own use case to avoid an enum value is narrowing the spec, not implementing it |
| a `bool` "this was a policy refusal" beside the outcome | Two spellings of one classification, which is GAP-67's finding and §D.9's `FailKind` row |

**Also decided, because C2 has a second half [GAPS] names:** §UC-060's
fail-safe-default scoping list gains one line — *a decorator's own refusal is
inside the append attempt from the kernel's side, so an **unclassified** error
from a decorator still becomes `ErrUncertain`.* That is not a defect; the kernel
cannot tell a wrapper's `return errQuota` from a store's lost connection, and the
remedy is the obligation on A7 to classify, stated at §UC-048 and exercised by a
conformance case. **S1** (enum, sentinel, map) + **S5** (the case).
*Test:* the `refusal classes` section drives a decorator that refuses **before**
forwarding and asserts `ErrRefused`, its cause reachable, its text absent, and
**zero events**; the same decorator returning a bare `errQuota` asserts
`ErrUncertain`, which is the pair that makes the obligation visible. **A third
case, from round 5** (GAP-147): the same decorator returning
`Failure(Refused, event.ErrConflict)` asserts the refusal is `ErrRefused` and is
**not** `ErrConflict`. **A fourth, from round 6** (GAP-159): the same decorator
returning `Failure(Refused, fmt.Errorf("%w: %w", errQuota, event.ErrConflict))`
asserts the refusal is still **not** `ErrConflict` and **is** `errQuota` — the
decorator's own error stays matchable while the vocabulary sentinel does not
cross a class, which is the pair that pins why the gate is on the target of the
traversal rather than on the promotion of the cause.

### C3 — GAP-102 `[high]` `errors.Is(err, context.DeadlineExceeded)` on an `ErrUncertain`

**Decided: the answer is `false`, and §2.3's "never wrapped by one" becomes
literally true.** Derived, not asserted: §UC-057's precedence rule exists because
the two outcomes have opposite caller obligations and reporting an uncertain
commit as a cancellation *cannot be recovered*. That asymmetry is established at
the store→kernel boundary and must survive to the boundary the caller reads. The
ordinary Go shape in this tree — `if errors.Is(err, context.DeadlineExceeded)`
before the subsystem's own branches — is precisely the branch that must not fire.

*Mechanism, one place, total.* `event`'s opaque cause wrapper (the
`cache/errors.go` shape) answers `false` for `context.Canceled` and
`context.DeadlineExceeded` unconditionally. A bare cancellation from a store is
not wrapped at all — it travels as itself (§UC-057's first window) — so the two
windows answer opposite and neither can leak into the other. **Round 4's GAP-135
widened the suppression from the two context sentinels to the whole cause**: `Is`
reaches the sentinel and the declared wrap and stops, because six of `port`'s
status branches are `errors.Is` over `crud` sentinels and a store's cause carries
those. What `ErrBackend` needs is unaffected — its `crud.ErrUnavailable` is a
declared wrap the kernel decides from the cause at construction — and the cause
itself is reachable by name through `CauseOf`; `event/errors.go` carries the
argument.

*What the `errors.As` traversal does to this, which is nothing.* A context
sentinel is an `errors.errorString`; nothing does `errors.As` for one, and the
wrapper's `As` reaches the refusal's declared wrap only (`event/errors.go`). The
suppression is therefore a property of `Is` alone and the two traversals cannot
disagree about cancellation.

**S1.** *Test:* `TestAContextCauseNeverTravelsThroughARefusal` in `event/`,
table-driven over every sentinel; and §D.9's `cancellation` section asserts both
halves in both windows — first window: the cancellation sentinel matches **and**
`ErrUncertain` does not; second window: `ErrUncertain` matches **and** the
cancellation sentinel does not.

### C4 — GAP-103 `[medium]` The store applies two bounds and nobody verifies them

**Decided: the kernel verifies.** §INV-018 is restated as it actually is — the
store applies `StreamPage` and `MaxRead` **because only it issues the read**, the
kernel applies the other four, **and the kernel checks the two it did not
apply**: a page longer than the bound the store itself published is
`ErrBackend` (store class), the same answer as a zero limit at a door. It is not
the import inversion §INV-018 forbids, because the number comes from `Limits()`
and never from a store's package. Cost: one `len()` per page.

**And the kernel verifies a page's *order*, not only its length.** The fold is
order-dependent by construction — that is what an event-sourced state is — so a
store, a decorator, or a SQL statement that lost its `ORDER BY` returning
`[v3, v1, v2]` produces a state that is wrong with the right version, the right
count and no refusal anywhere. The verification is stated on `event/store.go`:
**the page's first version is `after + 1` and each subsequent one is exactly one
higher**, every envelope is of the requested stream, and `ReadAll`'s positions
ascend; three comparisons per envelope against one decode and one fold. *Dense
from 1* is a property of the concatenation the kernel builds, never of a page —
page two of any stream longer than `StreamPage` starts at `after + 1` and would
fail a literal per-page reading of it. The
cheap check was already being paid for; the consequential one costs the same
order of magnitude and is the difference between §INV-006 holding for every cause
and holding for all but one.

**S4** (both checks) + **S5** (`bounds` case, `stream paging` case, and three
defects: the over-long page, the mis-ordered page, the foreign-stream page).
*Test:* `bounds` asserts an at-the-bound page is accepted and the
over-long-page defect fails it; `stream paging` asserts a correctly ordered page
at exactly `StreamPage` is accepted and that each mis-paging defect fails it.

### C5 — GAP-104 `[medium]` Two sentinels sit in classes their use cases contradict

**Decided, both moves.**

1. **A stored payload over the cap is `ErrPayload`, history class** — not
   `ErrTooLarge`. §UC-017's bytes are *a fact's recorded bytes that cannot be read
   by this declaration*, which is §2.3's history row word for word; the request
   class would answer a client `413` for a `GET` that carried no entity and
   would state an obligation ("send different data") the caller cannot discharge.
   `ErrTooLarge` keeps §UC-024's and §UC-025's caller-side payload and batch,
   where the request class and the too-large wrap are exactly right. One sentinel
   per caller obligation is §D.12's own `ErrBatch` argument applied here.
2. **`ErrCursor` moves store → wiring class.** §UC-053 says in terms that none of
   its three causes is a store failure; a cursor minted over backing A and
   presented to backing B is §2.3's wiring definition verbatim — two values the
   framework minted apart, put together by a hand, neither of which arrived with
   a request.

Sentinel count: **24** (22 + `ErrRefused` + `ErrSample`); `ErrTooLarge` loses one
raising site and `ErrCursor` changes class.

**The split only buys anything if both halves reach a transport, and as first
drafted neither did.** `port.KindOf` (`port/kind.go:17-24`) asks
`errs.AsFault(err)` first, which is `errors.As` (`errs/fault.go:106-112`);
`errors.As` walks `Unwrap` and `As` and **never consults `Is`**. The wrapper
shape this plan names — `cache/errors.go:26-37` — declares `Error`, `Is` and an
unexported accessor and **no `Unwrap`, no `As`**, so `ErrTooLarge`'s declared
wrap `*errs.Fault{errs.KindTooLarge}` was invisible from outside it,
`sentinelKind` (`port/kind.go:70-91`) has no `KindTooLarge` branch, and an
oversized caller payload rendered **500**. Measured, not reasoned: an `Is`-only
wrapper over `fmt.Errorf("%w", fault)` answers `errors.Is` true and `errors.As`
false. `ErrConflict → crud.ErrConflict` and `ErrBackend → crud.ErrUnavailable`
were never affected, because those two ride `errors.Is` and `sentinelKind` has
their branches. The repair is on `event/errors.go`: the wrapper answers
`errors.As` for the refusal's **declared wrap**. `port` is not edited.
**Round 4's GAP-135 is the same finding through the other door**: those
`sentinelKind` branches are `errors.Is`, so a wrapper that answered `Is` for the
**cause** let a store's incidental `crud` sentinel set the status of every
refusal that carries one — an uncertain commit as a 503. The matching half of the
repair is that `Is` reaches the sentinel and the declared wrap and stops, and
`CauseOf` is what reaches the cause.

**S1** (the wrapper's two traversals, the classes) + **S4** (the rendering, which
needs `Repo.Load` and a store and therefore cannot execute in S1).
*Test:* §INV-024's ground-truth table in S1; and, in S4,
`TestARequestClassRefusalRendersAClientStatusAndAHistoryClassOneDoesNot` in
`event/status_test.go`, which asserts **both** directions — an oversized
**caller** payload (§UC-024) renders `http.StatusRequestEntityTooLarge`, and a
load of an oversized **stored** payload (§UC-017, `ErrPayload`) renders
`http.StatusInternalServerError`. The second assertion alone is vacuous:
`StatusFor`'s `default` is 500, so "does not render as a client error" is true of
every sentinel the table does not name, including a broken `ErrTooLarge`. The
first is the one that fails today. **It runs over every member of the request
class, not one** (GAP-157): `ErrKey`, `ErrEncode` and `ErrSample` each render
`http.StatusBadRequest` and `ErrTooLarge` `http.StatusRequestEntityTooLarge`,
with a history-class (`ErrPayload`) and a wiring-class (`ErrCursor`) sentinel as
the 500 controls — because "renders a client status" was true of one member and
false of three, and a table with one row could not see it.

*The test file imports `port/porthttp`, and that is intended.* It is a **test**
import of `event`, not an import of `event`: `go list -deps` without `-test`
cannot see it, so S1's four-line import-graph assertion and
`scripts/event_test.go`'s cost arm — which reads `{{range .GoFiles}}` and
`go list -deps` without `-test`, on `scripts/tenancy_test.go:37,120`'s model —
are both unchanged, and the check is not loosened on its first run. `check_deps`
does list with `-test` (`scripts/checks.sh:32`) and stays green, because
`port/porthttp` is first-party.

### C6 — GAP-105 `[medium]` The inbound enumeration claims a totality it does not have

**Decided: scope the claim rather than grow the table.** §INV-021's inbound rule
is stated over *every call on the caller seam that hands the framework a value it
may retain, write, or invoke*. The remainder is answered in one line each and
named, so nobody looks for it: `Compose(parts ...string)`,
`eventtest.Keys(ids ...ID)`, `eventtest.Families(...)` and
`Fact.RoundTrip(byRevision ...any)` are **read, not retained, not written**;
`eventmemory.Spec.Clock` is **retained by design and invoked**, and falls under
C9's concurrency obligation. The four/two/one count is recomputed against the
scoped table and every citation of *the one row whose answer is not read only*
(§UC-041, §UC-052, §D.10 item 6) is checked against it. **S1** (clause) + **S6**
(the module page carries the enumeration). *Test:* the enumeration is a table in
`docs/modules/en/event.md`, and `scripts/docs_test.go` already fails on a symbol
it names that does not exist.

### C7 — GAP-106 `[medium]` What a read returns is the recipient's **including its capacity**

**Decided.** `Envelope.Payload` and the `[]Envelope` a read returns are handed
over with a **full-slice expression** or as a fresh allocation — the same
`b[i:j:j]` shape §INV-021 row 1 already requires of `Fact.New`'s freeze. A store
that sub-slices one page buffer per row satisfies every other clause and hands a
consumer slices whose `append` overwrites the next event; the existing
`payload ownership` write-every-byte case cannot see it. **S3** (`eventmemory`
obeys it) + **S5** (the case). *Test:* `payload ownership`'s `spilled` reads a
page, clones it, appends **one byte** to the **first** payload and asserts every
**other envelope of that same page** is byte-identical to its clone; then reads a
second page, appends **one envelope** to the returned `[]Envelope`, and asserts
both that the page it kept is unchanged and that the store's next read answers
the bytes it answered before. **S5 correction (round 1, GAP-1):** as first
written the case overwrote every byte of every payload *and then* appended, so
the byte the `append` spilled into the next row was immediately overwritten by
that row's own fill, and the only comparison afterwards was against a fresh read
rather than against a sibling of the same page — a store that packs a page into
one buffer and sub-slices it was certified green. The thirteenth defect
(`packedPages`) is the fixture that proves it is not any more.
**S5 correction (round 2, GAP-21):** the `[]Envelope` half as round 1 left it
compared `batch[:len(beside)]` with a clone taken one line earlier, which
`append` cannot make differ on any input, and it read the **whole** four-event
history rather than a page, so an `append` to it landed past every row the store
would ever serve. The stream is now `max(StreamPage, MaxRead) + 1` events long,
the case reads a **proper prefix** — the page the store publishes — clones the
page after it, appends one envelope to the first and asserts the page the store
still serves is unchanged. The fourteenth defect (`retainedPages`, a store that
cuts every page out of one array it keeps) is what that assertion reports, and
with the assertion gone the same corruption is caught three assertions later by
the kernel's own page check rather than at the hand-off.

### C8 — GAP-107 `[medium]` One defect count, one fixture inventory

**Decided: the inventory lives in code, not in five prose sites.**
`event/eventtest/defects.go` holds one slice of `{name, section, build}` and the
suite iterates it; a test asserts **every defect fails the section named for it**
and that every section a defect names exists in the suite. The count is
**fifteen**: [SPEC]'s six, plus the pooling decorator §INV-021's falsification
already describes (GAP-107), plus five this plan's own rounds add — the
over-long-page decorator (C4), the mis-ordered-page decorator and the
foreign-stream-page decorator (C4's order verification), the store whose
factors are each legal and whose **read** product is not (`event/bounds.go`), and
the **decorator that forwards the append and then reports `Failure(Refused, …)`** —
an operation that both wrote and claimed it refused rather than tried, which
`refusal classes`'s zero-events assertion is what sees. Round 5 put a different
twelfth here, the decorator that returns one of this vocabulary's own sentinels
as its cause, and **round 6 retired it** (GAP-159): the kernel now enforces the
partition on the **target** of the traversal rather than trusting the party that
built the cause, so that decorator has no observable consequence, and a defect
that cannot fail a section is not a defect. What it stood for is not lost — it is
C2's third and fourth `refusal classes` cases, which is where a claim about the
kernel belongs rather than in a store's fixture. **Round 1 of S5's review added
the thirteenth of what are now fifteen** (GAP-1): `packedPages`, a decorator that packs a page into one
freshly allocated buffer and hands out `buffer[at:]` per row with no third index,
which is the ordinary shape for a driver that scans a page into one row buffer.
It pools nothing across reads, so every clause the twelfth pins holds for it, and
before C7's case was rewritten it passed nineteen of twenty sections.

**Round 2 added the fourteenth and the fifteenth**: `retainedPages`, a store that
answers every page of a stream out of one array it keeps and cuts it without a
third index (GAP-21), and the store that claims persistence and builds a second
value over its backing holding none of what the first wrote (GAP-20) — the third
of the three that are stores rather than decorators, because what a factory
builds is not a call a decorator can forward. A
count in prose is what drifted; a count a test computes cannot, and the
enumeration above is the record rather than a total to keep in step.
**S5.** *Test:* `TestTheSuiteStillDetectsEveryDefectItWasBuiltToDetect`.

### C9 — GAP-108 `[medium]` Codec, mapper, fold, upcaster and clock concurrency

**Decided: stated on the contract, half policed, half recorded unpoliced with the
reason.** §INV-038 gains a row for the retained callbacks: *the framework calls a
codec, a fold, an upcaster, an identity mapper and a store's injected clock from
every request goroutine concurrently; each must be safe for that.* It is written
where an implementer reads it — on `Codec[V]`, on `Define`'s mapper parameter, on
`eventmemory.Spec.Clock` — and not only in the invariant.

*Policed:* the `concurrency` section reads one `*Aggregate` and its `*Fact`s from
many goroutines under `-race`, which is the seal's own property, and folds one
`Change` from two goroutines through an aliasing codec.
*Unpoliced, with the reason:* a fixture codec that deliberately races cannot be a
suite section, because a race is detected by the tool and not by an assertion, so
such a fixture would make **every** run red rather than one section. Recorded the
way injectivity is recorded, which is [SPEC]'s own standard. **S1**, **S2**,
**S5**.

### C10 — GAP-109 `[low]` Six one-clause residuals

| # | Decided |
|---|---|
| 1 | **The sealing readers are exhaustive and enumerated in code**: one unexported `seal()` on the declaration, called from `Bind`, `Aggregate.Fold`, `Fact.New`, `Fact.RoundTrip`, `eventtest.Keys` and `eventtest.Families`. A test enumerates the six call sites and fails when a seventh reader appears without a row (§UC-005, §INV-013). **S2 correction:** the last two live in another package and cannot call an unexported method, so they seal through the two exported methods they call — `Aggregate.Family` and `Aggregate.Key`. The six `seal()` call sites inside `event` are therefore `Aggregate.Family`, `Aggregate.Key`, `Aggregate.Fold`, `Fact.New`, `Fact.RoundTrip` and `Bind`; S2 delivers five and S4 adds `Bind` to both the code and `TestTheSealRefusesALateFact`'s table. **S4 correction:** `Bind` calls `seal()` itself rather than reaching it through `Family()`, because it reads the family field and the fold table directly, and the AST arm of that test names a function with no receiver by its own name. `Fact.Name` and `Fact.Revisions` read the fact's own immutable chain and not the aggregate's table, so they do not seal |
| 2 | **§UC-068's `Ledger` fold is the guarded one** — `if this == nil { this = Ledger{} }` — and the case states the non-nil precondition its "folding twice applies every event twice" claim needs. Written that way in the module page's example and in the test |
| 3 | **`MaxKey` is step (1) of `Append`'s order, with the kernel's text rules.** `Limits()` is **read once at `Bind` and retained**, so step (1) makes no call on the store at all and the whole key check is one step before anything else; step (4) is the payload byte cap, the batch count and the batch's actual bytes. `Load`'s order is unchanged: (1) mapper + text rules + `MaxKey`, (2) `Transaction(ctx)`, (3) the paged `ReadStream` |
| 4 | **On a closed store `Within` answers the `Transaction(ctx)` question and nothing else**: with nothing bound it is `ErrNoTransaction`; with a transaction of this store's backing bound it **succeeds**, and the next `Load` or `Append` is `ErrClosed`. `lifecycle` asserts both |
| 5 | **`Repo.Authority(ctx)`** returns the store's answer for this context: the valid authority when a transaction of this store's backing is bound; the **invalid** authority and a nil error when nothing is; `ErrAmbientNotTransaction` when something of this store's that is not a transaction is. **A closed store answers exactly what an open one would** and never reports closure here, which is row 4's answer for `Within` read once — the two rows disagreed until S3's GAP-3 settled it. The refusal comes from the `Load` or `Append` that follows |
| 6 | **§INV-036's grep covers the hyphenated form.** A new arm in `scripts/docs_test.go`, `TestNoDocPromisesExactlyOnceDelivery`, matches `exactly.?once` case-insensitively over `docs/`. There is no such arm today — verified — so it is written, not adjusted |

**S1** (3, 5), **S2** (1), **S4** (4, 5), **S5** (2, 4), **S6** (6).

---

## Questions [SPEC] deferred to the phase that writes the code

| # | [SPEC]'s status | Decided here |
|---|---|---|
| **GAP-41** | `[low][deferred]` — "whether a non-struct payload is reason enough to refuse … decided when the declaration checks are written" | **The codec answers; the kernel does not refuse a non-struct payload.** §INV-023 states that encodability "is answered by the codec declared beside it, never by a rule the kernel holds", and a kernel rule about a codec's domain is policy in the kernel by `microkernel.md`'s own table. §UC-004's trigger list loses that row; `Codec.CanEncode()` keeps it if the codec wants it. Leaves `## Debt` |
| **GAP-53** | `[low][deferred]` — "the wording of `Stream()` on a receipt for an append that never reached a store" | **An empty `Commit.Stream()` answers the token's stream**, which is the only value it has. `First()` is 0, `Last()` is the token's version, `Count()` is 0, `Authority()` is the invalid one, `Empty()` is true, and every accessor answers rather than panics (§INV-031). Leaves `## Debt` |
| **Q12** | open, [SPEC] says yes, [REC] agrees narrowly and prices it at 30–40 % of `eventmemory` | **Yes.** `eventmemory` is transaction-capable. §UC-040's asymmetry is the argument: `jobs.Stager` is an optional producer path, `Within` is on the main write path of any application that writes anything else in the same transaction. Refusing it leaves group E — ten use cases, five sentinels and a whole conformance section — unexercised by anything in the root module. The fallback and its cost are in `## Disagreements` D3 |
| **Q21** | open, "recorded so the owner decides rather than an implementer" | **`Define` panics, `TryDefine` returns the error** — [REC] §3.7's find, `crud/sqlrepo/blueprint.go:74:Define` / `:82:TryDefine`, which [[D-021]] already cites by line. §INV-013 survives because both return the *same* value through the same code path; `Define` is three lines over `TryDefine`. §UC-004's "there is no error-returning sibling" and §D.12's row against it are stated against the `Define`/`MustDefine` **inversion**, which is not what this is, and both are corrected in [SPEC] in S6's doc change |

Q1, Q3–Q11, Q13–Q20 are answered in [SPEC] and confirmed against the tree in
[REC] §4; nothing here moves them. **Q2** (doc numbering) is answered by [REC]
and extended by one here: **D-121…D-125**, **FL-036**, **UC-032**. **Q15**'s
decision doc is D-121.

---

## Microkernel classification

Required in writing by `references/microkernel.md` before any of this is planned.

### Kernel/extension classification: the event vocabulary and the store contract

- **Mechanism or policy?** Mechanism. `event` owns how a fact is declared, how it
  is encoded, how a stream is identified, how an append is admitted-by-token, how
  a failure is classified and rendered, how a read is bounded and resumed. It
  holds **no** rule that can vary per domain, per customer or per backend: the
  encodability rule is the codec's (§INV-023), the fold is the application's, the
  key rendering is the application's mapper, the bounds are the store's numbers,
  the failure classification is the store's selection.
- **Which kernel, and why one level in rather than out?** A new nested kernel for
  one axis of variation — *what a fact history is written to*. It is not the
  application kernel (`app`) and not `crud`'s: `crud` is a row-shaped kernel with
  a query language, and §INV-012 forbids event history from being one of its
  collections.
- **The second implementation exists on day one.** `eventmemory` is not a fake;
  `eventpg` is the third. That is what earns the seam at all, and it is [ES]'s own
  E0 decision 3 condition ("a second store would reopen it") being met.
- **Blast radius if removed:** exactly one capability disappears — an append-only
  per-aggregate fact history. Nothing under `crud/`, `auth/`, `port/`, `jobs/`,
  `cache/`, `storage/` or `tenancy/` imports it, which `scripts/event_test.go`
  proves rather than asserts.

### Per capability

| Capability | Kernel mechanism / which kernel / new extension | Why |
|---|---|---|
| `Store` / `Log` — eight methods, all required | **kernel contract** (`event`) | The extension point. Required rather than optional so the exact outer value always answers and a decorator cannot be walked past (§INV-030, [[D-115]]) |
| `eventmemory` | **extension** | A store. Registered explicitly at `event.Open(store)` — two greppable lines at the composition root, no registry, no import side effect |
| `eventpg` (phase 2) | **extension** | The zero-diff test's subject |
| A store **decorator** (quota, counter, recorder) | **extension**, same contract | §UC-048's actor. C2 gives it the one mechanism it lacked: a way to spell its own refusal |
| `Codec[V]` and `event.JSON` | **kernel contract + one shipped extension** | The second extension point. `JSON` is one implementation, not the mechanism; the encodability question is the codec's (§INV-023) |
| An aggregate declaration (`Define`/`Declare`) | **application policy** through the declaration seam | The kernel never names an aggregate, a family, a wire type or a state type |
| The identity mapper | **application policy** | The kernel supplies `Compose` and validates the output; it never derives a key |
| `Outcome` → sentinel map | **kernel mechanism** | Total, fixed, and the only thing the kernel reads. A store selects; it never renders |
| `eventtest` | **kernel-side test kit**, not an extension | It is the contract's executable statement, and it is where the compile-time half of §INV-019 lives |

### The defining test — can a second store be added with **zero** diffs to `event/`?

**Yes.** The proof is (1); (2) and (3) are change-detectors beside it, and saying
so is the point of this list — a detector named as the proof is how an invariant
ends up with no test.

1. **The proof: compile-time, available on day one.** `eventtest`'s own **test**
   package — a third package — builds the trivial store, the admit-everything
   store and the leaky-rollback store, each returning a classified failure of
   every `Outcome`, and runs `Run` against them. If any value the contract makes
   a store return needed `event`-internal access, that fixture would not compile.
   Every value on the seam is a plain struct with exported fields, a defined
   string, a defined `uint64`, an exported constant, an error from
   `event.Failure`, or an opaque value with an exported minting constructor whose
   forgery buys nothing (§D.15's per-value walk, re-verified in [REC] §2.1).
   *Anti-vacuity, and it is demonstrated once rather than claimed:* unexport one
   field of `Envelope` and the third package must fail to compile. Whoever writes
   `TestATrivialStoreNeedsNoInternalAccess` does that, watches the build break,
   restores it, and says so in the section report — otherwise the test proves
   only that a package compiles.
2. **The `event/…` sections of `docs/api/surface.md`, regenerated by `make api`
   in S6 and read by a person.** An earlier draft of this plan made that
   comparison an arm of `make check`, and it is **withdrawn**: `CLAUDE.md` says
   in terms *"`make api` regenerates `docs/api/surface.md`. Nothing checks it and
   nothing should: a diff there is a question for a person"*, and quoting the
   second half of that sentence as support for turning the diff into a build
   failure inverts it. The plan does not implement around a binding rule. The
   detector was also unsound for §INV-019 in both directions — a store can be
   admitted by widening an unexported `Outcome` switch with the surface
   byte-identical, and an unrelated additive export anywhere under `event/` turns
   `make check` red until somebody regenerates the baseline, which is R6's "a
   structural check loosened under pressure on its first run" arriving by design.
   S6's checkpoint therefore ends with `make api` and
   `git --no-pager diff --stat docs/api/surface.md`: the diff is produced and
   read, and nothing gates on it.
3. **The git arm, recorded in D-121 as what may join (2) after the first tag, if
   a person wants it:** `git diff --stat <phase-1-tag> -- event/ ':!event/eventpg'`.
   Same predicate, same two weaknesses, wider net — and it is a report, not a
   gate, for the same reason.

The kernel imports **no** store package; there is no registry row, no type switch
and no `if name ==` anywhere in `event/`. C2 grows a closed enum by one value and
the sentinel set by one — additive by §D.14's own compatibility paragraph, and it
is the last such growth phase 1 makes.

---

## Contracts

Written before code, grouped by file. Someone must be able to disagree with the
API from this document alone. Receivers are `this`, the tree's dominant receiver
([REC] §1.7).

### `event/` — the kernel

#### `event/identity.go`

```go
type Key      string   // one aggregate identity, rendered by the declared mapper
type Version  uint64   // dense over one stream from 1; 0 is "never appended to"
type Position uint64   // store-wide, strictly increasing, gaps permitted
type Cursor   string   // store-minted, opaque, persistable; no ordering defined

type Stream struct {
	Family string
	Key    Key
}

func (this Stream) String() string   // "[stream accounts.account]" — never the key
                                     // "[stream unnameable]" when the family fails the text rule
func Compose(parts ...string) Key
```

**`Stream.String` renders the family only when the family passed the kernel's own
text rule** (`MaxNameBytes`, valid UTF-8, no NUL, no control character), and
`"[stream unnameable]"` otherwise. A kernel-minted stream carries a declared
identifier and always passes; the reachable path is a `Stream` that arrived on an
**`Envelope`**, which is a store's data under the trust boundary on
`event/store.go` — a restored dump, a shared database another service writes, a
store with its own grants. Without the guard the framework's own refusal is how a
newline, a forged log line or a megabyte of family name reaches an operator's log,
and it is C4's own verification refusals (the foreign-stream and mis-ordered pages,
S4) that render one. Round 5, GAP-150.

**`Compose`'s rendering, byte for byte, and it is frozen (§INV-005, D-125).**

Each part is escaped and the escaped parts are joined with `/`:

- `%` becomes `%25` and `/` becomes `%2F`;
- every byte that is not part of the UTF-8 encoding of a **non-control** rune —
  an invalid UTF-8 byte, a C0 byte, `0x7F`, a C1 rune's two bytes, NUL — becomes
  `%` followed by two upper-case hex digits, one escape per byte;
- everything else travels as itself.

`Compose("acme", "A-17")` is `acme/A-17`. `Compose("acme/evil", "A-17")` is
`acme%2Fevil/A-17`, which is not `Compose("acme", "evil/A-17")` = `acme/evil%2FA-17`.
`Compose("рога", "17")` is `рога/17` — a multi-byte identity stays readable.

*Why this and not the length prefix [REC] §3.6 was read as recommending.*
`cache/address.go:32-53:NamespaceOf` writes a 4-byte big-endian length before each
part **into a `sha256.New()`** — the prefixed bytes are hashed and never become a
string; the `Namespace` keeps a `[32]byte` digest and the cloned parts. A binary
length prefix used as a *key* emits bytes in `0x00`–`0x1F` for every part shorter
than 16 MiB, so every composed key would contain NUL and control bytes and be
refused with `ErrKey` at all four doors — §UC-050's happy path could not execute
and the `stream identity` section's "a NUL is refused" case would fail the
framework's own recommended mapper.

*Why escaping and not a hash.* A hash is collision-**resistant**; this is
injective **by construction** and reversible, so an operator reading a store's key
column sees the identity that produced it and a support question is answered by
looking. §INV-033's obligation is byte-exact identity, not merely improbable
collision.

*Why it is total.* A Go `string` is arbitrary bytes and §D.1's mapper is
infallible (`func(ID) Key`), so `Compose` returns no error and must therefore
produce a **legal** key from any part list. The escaper is defined as the exact
complement of `event/text.go`'s rule and reads the same predicate, so the two
cannot drift apart: what the key rule refuses is what the escaper escapes. The
one pair that renders equal is `Compose()` and `Compose("")`, both `""`, and both
are refused as empty at every door — so injectivity holds over the whole legal
domain.

*What it costs, so a deployment can size `MaxKey`.* One separator byte per part
after the first, and three bytes instead of one for each escaped byte. A part
list of *P* parts totalling *B* bytes renders to `B + P - 1` bytes when nothing
needs escaping — the ordinary case for an identifier or a UUID — and never more
than `3B + P - 1`. An identity that cannot fit `MaxKeyBytes` is digested by the
application's own mapper before it reaches `Compose`; see `event/bounds.go`.

*Test (as shipped by S1, which is stronger than the pairwise table this row
first named):* `TestComposeRendersTheFrozenKey` is a twelve-row golden table of
literal renderings — the empty part, a part containing `/`, one containing `%`,
one containing a NUL, one containing invalid UTF-8, a C1 rune and a multi-byte
part — plus the one pair that renders equal, `Compose()` and `Compose("")`, both
asserted to be refused as empty. `FuzzComposeRendersAKeyThatIsLegalAndReversible`
carries the other two properties over an unbounded domain rather than a table:
every composed key passes the kernel key rule, and `decompose(Compose(a, b, c))`
is `[a b c]` byte for byte, which is injectivity by reversibility. Removing the
escaping of `/` fails both.

| Symbol | Why exported |
|---|---|
| `Key`, `Version`, `Position`, `Cursor` | §INV-019: a store in another package converts to and from its own columns. Defined types rather than parsed structs because a `Version` is assigned per append and a `Position` per row, so a parsed type would need minting calls whose only caller is a store — API with no reader ([REC] §2.1) |
| `Stream` | The store seam's composite; decomposed into two columns on the way in, a composite literal on the way out |
| `Stream.String` | §INV-025: the one rendering, and it never carries the key — nor a family that failed the kernel's text rule (GAP-150) |
| `Compose` | The application's mapper calls it. Making the correct rendering the shortest one to write is the framework's whole answer to §INV-033's unverifiable obligation |

#### `event/bounds.go`

```go
const (
	MaxPayloadBytes  = 1 << 20    // the ceiling on Limits().MaxPayload
	MaxNameBytes     = 128        // a family and a wire type name
	MaxKeyBytes      = 2 << 10    // the ceiling on Limits().MaxKey
	MaxBatchCount    = 1024       // the ceiling on Limits().MaxBatch
	MaxPageCount     = 4096       // the ceiling on StreamPage and MaxRead
	MaxResidentBytes = 64 << 20   // the ceiling on MaxPayload x each of the three counts
)

// The one place MaxResidentBytes becomes a count of envelopes.
func ResidentPage(maxPayload int) int
```

Six ceilings, because §INV-022 requires the kernel to refuse a limit **above** its
own for each bound a store publishes — and because five of them bound only the
**factors** of a product, which is what the memory actually is. Each carries its
derivation, what it protects, and what a deployment does when it is the wrong
number, in §D.13's form. All six are exported: a deployment reads them to choose
its store's numbers and a test builds an at-the-ceiling case from them.

| Constant | Derived from | What it protects | When it is wrong for a deployment |
|---|---|---|---|
| `MaxPayloadBytes` `1 << 20` | `jobs.MaxPayloadBytes` (verified, `jobs/bounds.go:19`). An event payload and a job payload are the same kind of object: a small declared struct, encoded once, stored in a row | One event's resident bytes, on both the encode and the decode side | A payload that does not fit is not an event. It is a blob in `storage` with an event naming it — which keeps the history readable and the index small |
| `MaxNameBytes` `128` | `jobs.MaxNameBytes` (verified, `jobs/bounds.go:6`). A family and a wire type name are registry names in an indexed column | A store's identifier columns, and the bytes a refusal may name (§INV-025) | It is not. A 128-byte family name is already a design smell; nothing legitimate needs more |
| `MaxKeyBytes` `2 << 10` | The narrowest index tuple among the engines phase 2 targets — PostgreSQL's btree tuple limit is ~2704 B, MySQL InnoDB's index key prefix 3072 B — less `MaxNameBytes` for the family, 8 for the version and per-tuple overhead, rounded down to a power of two. §D.13's **recommended** store `MaxKey` stays 512 B; this is the ceiling above it | A store's `(family, key, version)` unique index, which is the mechanism §UC-022's conflict is | The mapper is the application's: an identity that does not fit is rendered through a digest of its own — `Compose(tenant, hex(sha256(url)))` — which is `cache/address.go:NamespaceOf`'s own answer to the same problem. **Never by editing `event/bounds.go`** |
| `MaxBatchCount` `1024` | `MaxResidentBytes / jobs.DefaultPayloadBytes` = 64 MiB / 64 KiB: the largest batch that still fits the resident ceiling at the framework's own default payload size | The **count** on its own — the per-envelope fixed cost and the per-change work, neither of which `MaxResidentBytes` prices. The bytes are bounded by the measurement at `Append`, never by a product against `MaxPayload` | One append is one atomic unit and cannot be split, so a decision that needs more than 1024 events at once is a modelling question, not a bound. §UC-021 puts two decisions in **one** append; it does not put a migration there |
| `MaxPageCount` `4096` | One `Envelope` header costs ~120 B before a single payload byte (two strings, three integers, a string, a slice header, a `time.Time`), so 4096 caps a page's fixed cost at ~480 KiB | The read path's per-row cost, which `MaxResidentBytes` cannot see because it prices payloads | Lower `StreamPage`. A load is **paged** (§UC-011) and a global read is drained in pages (§UC-036), so page size is a throughput knob and never a capacity one — a deployment never needs a bigger page, it needs more of them |
| `MaxResidentBytes` `64 << 20` | Four times §D.13's recommended load page (`MaxPayload` 64 KiB × `StreamPage` 256 = 16 MiB), so a deployment may quadruple any one factor without the kernel in the way | The memory one `Load` page, one `ReadAll` page or one `Append` may hold — the thing §UC-011's Observed is about. On the two read paths it is enforced as a **product** of the store's published numbers, because the store allocates first; on the append path as the **actual sum** of the payload bytes, because the kernel is holding them | It is the one ceiling a deployment does not raise: 64 MiB per concurrent request is already the point at which the answer is a smaller page, not a larger allowance |

**The kernel enforces the resident bound in two different places, because the
two read paths and the write path are not the same problem.** §UC-011's Observed
("memory in use is bounded by one page and by the state itself") is a claim about
a product, and checking only the factors admits a store publishing
`MaxPayload: 1<<20, StreamPage: 4096` — each field legal, one page up to
**4 GiB**.

*The two read paths: a worst-case product at both doors.* A store fills a page
before the kernel sees one byte of it, so the only bound available is the one
computed from the numbers the store published. `Bind` and `Read` check

```
StreamPage <= MaxResidentBytes / MaxPayload
MaxRead    <= MaxResidentBytes / MaxPayload
```

— **written as divisions, never as multiplications**, so the check cannot
overflow an `int` on a 32-bit build, where `1<<20 * 4096` does. The zero-field
check runs first and refuses `MaxPayload: 0`, which is what makes the divisor
safe. Both answer `ErrWrongStore`, the sentinel every other store-honesty failure
already carries (§INV-022). Cost: two integer divisions per door, so §D.4's
constant-time claim is unchanged.

**That division is `event.ResidentPage`, exported, and it is the only copy of
it.** `Bind` and `Read` call it, and so does a store where it chooses its own
numbers — `eventmemory.New` derives its page default and its refusal from it
(§S3, GAP-6). A store cannot otherwise apply this rule without re-deriving it,
which is a policy the kernel owns being reimplemented in every extension, and two
copies that round one domain quantity differently is what makes a store boot and
then be refused at `Bind`. It guards its own divisor — a `maxPayload` of zero or
less admits no page at all — because `Limits` is a store's data and the kernel
divides by it.

*The write path: the measurement, at step 4 of `Append`.* Here the kernel is
already holding every `Record.Payload` and can add up `len()`, so a worst-case
product would be enforcing an estimate against a number it can read.
`sum(len(Record.Payload)) > MaxResidentBytes` is **`ErrTooLarge`**, request
class, beside the per-record cap and the batch count — the caller's own
obligation, discharged by appending fewer or smaller records.

**There is therefore no `MaxBatch ≤ MaxResidentBytes / MaxPayload` check, and
withdrawing it is deliberate.** A deployment that declares one document fact of
up to 1 MiB publishes `MaxPayload: 1<<20`; the product form then forces
`MaxBatch ≤ 64` and refuses an import that appends 400 events of 200 B — 80 KB,
one thousandth of the ceiling — while §INV-003 forbids splitting the append. The
only remedy the product form leaves is lowering `MaxPayload` below what the
application needs, which is making a correct bound wrong to satisfy a bound that
was measuring the wrong thing. One big fact beside many small ones is the
ordinary shape of every domain with a document and an audit trail
(`universality.md`), so it is not a modelling error. `MaxBatch` keeps its own
ceiling — `MaxBatchCount` — as a count bound, and `MaxResidentBytes` bounds the
bytes.

*Conformance:* the `bounds` section gains a store whose factors are each legal
and whose **read** product is not, and asserts it is refused at **both** doors;
its control is the store at exactly the product, which must be accepted. The
write side is asserted in `event`'s own tests, where a batch can be built byte by
byte against `store.Limits()`. `defects.go` carries the read-side store as a
fixture.

#### `event/text.go` — no exported symbol

The kernel text rule, in one place: non-empty, valid UTF-8, no NUL, no control
character, within its cap. Applied to a key at four doors and to a declared
identifier at declaration. No case folding, no trimming, no Unicode
normalisation (§INV-033).

**Not exported**, because there is no reader outside `event` in phase 1: a store
never validates a key, and a caller never renders one except through a mapper.

> **[REC] §3.5 recommends a narrower domain for declared identifiers**
> (`jobs/identity.go:273:validRegistryName` — ASCII, non-repeating `.-_`
> separators). **Not taken**, and it is the owner's to overturn: §INV-033's
> single-rule argument is stated over the key and [SPEC] extends it to
> identifiers deliberately. The cost is stated where [REC] states it — a family
> with an emoji is legal here and is not a PostgreSQL identifier — and phase 2
> discovers it at the first `CREATE TABLE`. Recorded in `## Risks` R3.

#### `event/backing.go`

```go
type Backing struct{ /* opaque */ }

func NewBacking(identity any) (Backing, error)
func (this Backing) Equal(other Backing) bool   // crud.SameDataSource; never ==
func (this Backing) String() string             // a bracketed classification, never the identity

func (this Backing) valid() bool                // crud.SameDataSource(id, id)
```

`Equal` **is** `crud.SameDataSource(this.identity, other.identity)` — imported,
not restated, because "are these two things writing to the same place" is a
question the framework answers once and two answers diverging would change which
programs are admitted (§INV-016, GAP-58).

`NewBacking` refuses with **`ErrWrongStore`**, wiring class — the sentinel
§INV-022 already assigns to every other dishonest store, and the value it refuses
is a store's own — and so does `NewAuthority`. `NewBacking` refuses an identity
that is **nil by any route** — a nil interface, or a nil pointer, map, slice,
channel or func inside one — over `reflect`,
because `crud.SameDataSource(typedNil, typedNil)` correctly answers `true` for
its own job and `crud/executor.go:507:isNilValue` is **unexported** (verified).
That restatement is a constructor-time total function whose two answers, if they
ever diverged, both refuse — a different risk from a per-append comparison
(§INV-016, GAP-85).

**It refuses a second reason, and that is API rather than an implementation
detail** (GAP-153): an identity whose dynamic type is **not comparable** — a
slice, a map, a func, or a struct containing one — is `ErrWrongStore` too, with
its own message. [SPEC] §D.14:5030-5034 and §INV-028's *Falsified by* both require
it of `NewAuthority`, and `NewBacking` reads the same predicate for the same
reason. Refusing at construction is strictly better than the alternative it
replaces: without it `crud.SameDataSource` answers `false` for an uncomparable
pair, so the store's backing never matches **itself** and the failure surfaces one
door later as a refusal the store cannot act on.

| Symbol | Why exported |
|---|---|
| `Backing` | On `Store.Backing()`, so every store returns one |
| `NewBacking` | §INV-019: `eventpg` mints one from `crud.KeyOf(spec.Source)` in its own package |
| `Equal` | The kernel's comparison at `Bind`, `Read` and every `Append` — and `eventtest`'s `shared backing` section, which asserts a sibling reports the **same** backing directly rather than inferring it from a round trip. That is the reader outside `event` that earns the export |
| `String` | §INV-025 |
| `valid` — **not** exported | Its only caller is the store-honesty check at the two doors. `NewBacking` already told a store whether its identity was accepted, by returning an error, so nothing outside `event` has a question left to ask. `Authority.Valid` is different and stays exported: §UC-030's caller reads it |

#### `event/authority.go`

```go
type Authority struct{ /* opaque */ }

func NewAuthority(over Backing, transaction any) (Authority, error)
func (this Authority) Same(other Authority) bool  // backings Equal, then identities Same
func (this Authority) Valid() bool
func (this Authority) String() string             // "[event authority]"
func (Authority) MarshalJSON() ([]byte, error)    // always refuses
```

An authority **is** its transaction's identity held by reference — for a SQL
store the `*sql.Tx` `crudsql.Transaction(executor)` returned, for `eventmemory`
its own `*Tx`. Nothing is minted, nothing is memoised, nothing has to be evicted,
and a savepoint answers its parent's because `crudsql`'s savepoint returns the
parent `*sql.Tx` ([REC] §2.3, verified). `NewAuthority` applies `NewBacking`'s
nil-by-any-route predicate to `transaction` as well, and **refuses an
uncomparable identity with `ErrWrongStore`** on the same argument (GAP-153).

*What it does not refuse, on the record.* A **value** identity — a counter, a
sequence number, a UUID string, a small struct — passes every check, and two live
transactions carrying equal values would compare `Same` and make §UC-030's
atomicity claim silently false. A blanket refusal of non-reference kinds is not
taken: a value struct naming a connection and a sequence is a legitimate identity
for a store whose transactions are not pointers. The obligation is stated where an
implementer reads it and certified by `eventtest`'s `transactions` section, whose
`Begin`-twice case asserts two live transactions are **not** `Same`. GAP-152,
carried in `## Debt`.

Exported for §INV-019: a store constructs one. Forging one buys nothing — the
kernel never *accepts* an authority as an argument, it obtains one from
`store.Transaction(ctx)` (§INV-028).

#### `event/outcome.go`

```go
type Outcome uint8

const (
	Unclassified Outcome = iota // the zero value: the door's fail-safe default
	Conflict                    // the stream was not at AppendRequest.Expected
	NotWritten                  // the write certainly did not land
	Unconfirmed                 // the write was issued and never confirmed
	Closed                      // this store refused rather than tried
	BadCursor                   // not minted over this backing, unparseable, or retired
	Refused                     // C2: refused as a matter of this store's own policy
)

func (this Outcome) String() string
func Failure(outcome Outcome, cause error) error
```

**`Outcome` is a defined `uint8`, so `event.Outcome(200)` is legal Go**, and a
store or a decorator can build one by arithmetic, from configuration, or by being
compiled against a later `event` and vendored into an older tree. The map's rows
cover seven values and "not a `Failure` at all"; an out-of-range `Failure` **is**
a `Failure` and would fall through every row into whatever a missing `default:`
does — which on the append door is the difference between `ErrUncertain` and
returning **nil for a write that never happened**. So:

- **`Failure` normalises**: an outcome outside the seven becomes `Unclassified`
  at construction, before the value can travel. Totality is a property of the
  constructor, not a hope about callers.
- `Outcome.String()` answers `"[outcome unclassified]"` for any value outside the
  seven and never renders the number — a store's number is data, and §INV-025
  does not let data travel in a rendering.
- **The `Failure` value renders its outcome and nothing of its cause**, for the
  same reason a refusal does (GAP-148). The kernel never renders one — it maps it
  and returns a `refusal` — but `Failure` is an exported constructor, so the value
  travels through decorators, and a decorator that logs the failure it is
  forwarding would otherwise print a driver's text: `pq: password authentication
  failed for user "admin"` is what one such text is, measured. The cause is the
  store's own and the store already has it.
- The map's switch still carries an explicit `default:` arm equal to
  `Unclassified`'s row, so a value that reached it unnormalised takes the door's
  fail-safe default rather than the zero value of an error.

*Tests, and there are two because the work is in two sections* (GAP-161).
**S1** is `TestAnOutcomeOutsideTheVocabularyNormalises` — that
`Failure(Outcome(200), c)` holds `Unclassified` before it travels, and that
`String()` renders `[outcome unclassified]` for every out-of-range value and
never a number. **S4** is `TestAnOutcomeOutsideTheVocabularyTakesTheFailSafeDefault`
— the same failure driven through `Append` and through `ReadAll`, asserting
`ErrUncertain` and `ErrBackend` respectively, with the seven in-range values as
its control table. Both checkpoints name the one that belongs to them.

The whole of the store→kernel error channel. A store adds no sentinel; it selects
a row and the kernel maps it. The kernel finds the outcome with a **bounded
`errors.As`** (`findAs`, GAP-158) and never with a type assertion, so a
forwarding decorator that wraps with `%w`
keeps its classification — which is the one thing `jobs/queue.go:808:normalizeSenderError`
gets wrong today with `err.(rejectedPlacement)` ([REC] §3.1), and D-122 records it
as the rule's origin. A nil cause is legal.

#### `event/errors.go`

Twenty-four sentinels in six classes; `errors.Is` never crosses a class
(§INV-024). Two intra-class wraps, eight declared class wraps, and a **cause**
is not a wrap unless the row says it is.

| Class | Sentinels |
|---|---|
| declaration (panic, never returned) | `ErrDeclaration`, `ErrSealed` → `ErrDeclaration`, `ErrCodecType` → `ErrDeclaration` |
| wiring | `ErrFamily`, `ErrWrongStore`, `ErrWrongStream`, `ErrNoTransaction`, `ErrNoTransactionBinding`, `ErrAmbientNotTransaction`, `ErrTransactionMismatch`, **`ErrCursor`** (C5) |
| request | `ErrKey`, `ErrEncode`, `ErrSample` (C1), `ErrTooLarge` — **all four → `crud.ErrBadRequest`, declared on the sentinel** (GAP-157), and `ErrTooLarge` additionally → `*errs.Fault{errs.KindTooLarge}` at the one door that raises it |
| history | `ErrUnknownType`, `ErrRevision`, `ErrPayload`, `ErrUpcast` → the application's own error |
| write | `ErrConflict` → `crud.ErrConflict`, `ErrUncertain` (wraps nothing) |
| store | `ErrBackend` → `crud.ErrUnavailable` iff the cause is retryable, `ErrClosed`, **`ErrRefused`** (C2) |

**The request class promises a client status, and the promise is on the sentinel
rather than at the door** (GAP-157). §2.3 states the class as *a transport
answers a client error* and §UC-051 chose it for `ErrKey` precisely so that a
request which omitted an id is not an operator page. In the code the class
decides nothing on its own: `port.KindOf` asks `errs.AsFault` and then
`sentinelKind`'s six `errors.Is` branches (`port/kind.go:70-91`), so a sentinel
with no declared wrap falls through to `errs.KindInternal` and renders **500**.
Measured over all twenty-four before the fix: `ErrKey`, `ErrEncode` and
`ErrSample` each rendered 500, and only `ErrTooLarge` reached a client status,
and only through the `*errs.Fault` `tooLarge()` builds. So all four wrap
`crud.ErrBadRequest` in their own declaration — 400 — which is one line, adds no
dependency (`event` already imports `crud`), and cannot be forgotten by the four
S4 doors that raise `ErrKey`. `ErrTooLarge` keeps its fault: `port.KindOf` asks
`errs.AsFault` first, so a too-large refusal still renders 413 and only a bare
`ErrTooLarge` falls back to 400. `tooLarge()` therefore returns the plain
sentinel with the fault as its wrap, and the rule name it used to append to the
sentinel's text lives in the fault's message, which is the value-bearing channel
§INV-025 already permits it in.

The unexported cause wrapper is `cache/errors.go:26:opaqueError`'s shape —
`Error()` returns the category's text, and every chain it walks is walked under a
bounded hop budget and a `recover`, because the chains it reads are a store's, a
decorator's and an application's — with **C3's addition**: it answers `false` for
`context.Canceled` and `context.DeadlineExceeded`, always.

**The budget bounds the number of errors visited, not the depth reached**
(GAP-149). `errors.Join` branches, so a budget passed by value into each sibling
costs `b^depth` visits rather than `budget` — measured at 2.68 s for a balanced
binary join tree of depth 26, doubling per level, on the request goroutine inside
the kernel, on the hot path of every classified store failure. The counter is
shared across branches through a `*int`, which is `cache/errors.go:96-125`'s own
shape and what was lost in the port. A store that fans an append across shards and
joins per-shard errors of per-row errors is the ordinary producing input; the
budget exists precisely because the kernel does not trust the party that built the
chain.

**And it bounds every question the kernel asks of a foreign chain, not one of
them** (GAP-158). Bounding the walk while `refuse`, `CauseOf` and `refusal.As`
still called the stdlib `errors.As` left the defence on the arm that was not on
the hot path: `refuse` runs the `*failure` lookup as the **first statement on
every store error**, and measured on the same balanced tree it cost 8.66 ms /
136 ms / 555 ms at depths 18 / 22 / 24 while the kernel's own `matches` cost
1.9 µs, and a **cyclic** `Unwrap() error` chain hung it forever with the
caller's ambient transaction open. So the kernel performs exactly three
traversals — `matches`, `findAs` and `withinBudget` — and no stdlib `errors.Is`
or `errors.As` over a chain it did not build. `findAs[T]` is the bounded
`errors.As`: it visits under the same shared budget and the same `recover`, and
at each node it asks what the stdlib asks — is this node a `T`, or does it
answer for one through an `As(any) bool` method. It replaces the three stdlib
calls and `faultIn`. The one stdlib `errors.As` left is inside `refusal.As`,
over the refusal's **own wrap**, and it is bounded at promotion time instead:
`causeAsWrap` promotes a cause only when the kernel could read it to the end
within the budget. That closes the second half of the same finding — that `Is`
stopped at 64 hops and `As` did not, so past the budget a `crud.ErrForbidden` a
decorator meant as a 403 was invisible to `Is` while an `*errs.Fault` at the
same depth still set the status. Now neither sees it and the refusal keeps its
own class. What no budget can bound is the work a foreign `Is` or `As` method
does inside one visit; the comment on the walk says so.

**"Does this chain carry a `T`" has one answer** (GAP-160). The walk follows
`Unwrap() error` and `Unwrap() []error`; a cause that carries its `*errs.Fault`
behind an `As(any) bool` method — which is the shape `event`'s own `refusal` has
— was opaque to `faultIn` and transparent to `errs.AsFault`, and the divergence
rendered a retryable store failure **500** instead of 503, so a caller did not
retry something it should. `findAs`'s `As` arm removes the second answer rather
than documenting it, and no change to `errs` is needed.

**It holds three things and answers the two traversals differently, and that is
the whole mechanism.** `sentinel`, `wrapped` — the refusal's own **declared
wrap** — and `cause`.

| Traversal | Reaches |
|---|---|
| `errors.Is` | `sentinel` and `wrapped`, minus the two context sentinels — and **never `cause`** (GAP-135). For a target that is one of the twenty-four, **`sentinel` alone** (GAP-147) |
| `errors.As` | `wrapped` **only** |
| `Error()` | the sentinel's own text, and nothing from `cause` (§INV-025) |
| `event.CauseOf(err)` | `cause`, and it is the only thing that does |

**Why `Is` stops where `As` does, which is round 4's GAP-135 and is implemented
rather than argued.** `port.KindOf` asks `errs.AsFault` first and then falls
through to `sentinelKind` (`port/kind.go:70-91`), which is **six `errors.Is`
branches** over `crud` sentinels. A wrapper whose `Is` reached the cause therefore
hands a store's incidental error the caller's transport status through the second
door after C5 closed the first: an `ErrUncertain` whose cause carries
`crud.ErrUnavailable` — which is exactly what a `crudsql` classification produces
— renders **503 "retry me"**, and a retry of an unconfirmed append writes the same
decision twice into an append-only history. So `Is` stops at `sentinel` and
`wrapped`, and the cause is reachable **deliberately and by name** through the
exported `CauseOf`, which is the one reader that wants it (an operator's log
line). Nothing is lost: `ErrBackend`'s `crud.ErrUnavailable` is a **declared
wrap** the kernel decides from the cause at construction, not a value read out of
it later, and `ErrRefused`'s and `ErrUpcast`'s causes **are** their declared
wraps, so both still arrive by `Is` and by `As`. [SPEC] §2.3's "`errors.Is`
reaches it" and §INV-045's falsification clause say the opposite today and are in
S6's edit list.

`As` exists because a declared class wrap is otherwise unreadable. `port.KindOf`
asks `errs.AsFault` first and `errors.As` never consults `Is`, so an `Is`-only
wrapper hides `ErrTooLarge`'s `*errs.Fault{errs.KindTooLarge}` completely and a
2 MiB append renders 500 rather than 413 (C5). `As` reaching `wrapped` and
nothing else is what makes the declared wraps readable **without** letting a
store's incidental cause set the caller's status — an `*errs.Fault{KindNotFound}`
attached deep inside a driver stack must not turn an uncertain commit into a 404,
and §UC-057's precedence rule is the reason.

The `wrapped` column, by sentinel:

| Sentinel | `wrapped` |
|---|---|
| `ErrKey`, `ErrEncode`, `ErrSample` | `crud.ErrBadRequest`, declared on the sentinel, so the class decides the status at every door that raises them (GAP-157) |
| `ErrTooLarge` | `*errs.Fault{errs.KindTooLarge}`, built by `errs.TooLarge()`, naming the bound and the byte count and never the payload — and `crud.ErrBadRequest` on the sentinel beneath it, which only a bare `ErrTooLarge` falls back to |
| `ErrConflict` | `crud.ErrConflict` |
| `ErrBackend` | `crud.ErrUnavailable`, iff the cause is retryable |
| `ErrUpcast` | the application's own error |
| `ErrRefused` | **the store's own cause** — the one row where the cause *is* the wrap, because a policy refusal's status is the policy's to state (C2). A decorator that wants a 403 wraps `crud.ErrForbidden` and it arrives through `Is`; one that wants a 409 or a 503 wraps `crud.ErrConflict` or `crud.ErrUnavailable`, or builds an `*errs.Fault` of any of `errs.Kind`'s ten and it arrives through `As`. **429 is not among them** — `errs.Kind` has no rate-limit member and `porthttp.StatusFor` no such branch (GAP-145), and adding one is a change to `errs` and `port`, which `## Debt` carries. The decorator owns what its fault says, and §INV-025 binds the kernel's renderings, not a value the application deliberately built |
| every other sentinel | nothing — `As` from outside finds no framework type at all |

**The two rows whose wrap is a cause are the two doors §INV-024's partition is
promised through, and the kernel closes them itself** (GAP-149's sibling,
GAP-147). §2.3 already states the obligation — *a store's own error must not
itself be a sentinel of this vocabulary* — and calls it a **checkable
prohibition**; nothing checked it. The enforcement is on the **target** of the
traversal, not on the promotion: `refusal.Is` answers a target that is one of
the twenty-four from `sentinel` alone and never from `wrapped`, so no refusal of
one class can reach a sentinel of another however its cause was built — for
every row, not only the two that promote one. The sentinel list is what an
unexported `vocabulary()` returns in `event/errors.go` — a function rather than
a package-level slice, so §INV-013 stays literal — rather than a number in prose.

**And the two lists are held together by a check rather than by care** (GAP-163).
`TestTheRefusalVocabularyIsAPartition`'s first subtest walks `event/errors.go`
with `go/ast` and compares three multisets: the exported `Err*` variables the
file declares, the identifiers `vocabulary()` returns, and the test's own
class-labelled table. A sentinel that exists and is missing from the gate's list
is treated as foreign to the vocabulary, so a cause carrying it reaches it across
a class — GAP-147 reopened for exactly that row — and the 24 × 23 table would run
over the survivors and pass. Set equality, not a count: a count passes on a swap.

*Why the target and not the promotion* (GAP-159). The failure a quota decorator
writes is the natural one — `event.Failure(event.Refused, fmt.Errorf("tenant over
quota: %w", event.ErrConflict))`, because C2 tells it to state its status in the
cause and `ErrConflict` is what it reaches for to mean 409. Reachable, §UC-022's
caller loop — `if errors.Is(err, event.ErrConflict) { reload and decide again }` —
spins forever on a policy refusal that will never clear, holding the caller's
transaction open, with no error surfaced anywhere. `event.ErrUncertain` in the
same position is the mirror and worse: a refusal where **nothing was written**
sends a caller down the reconcile-a-possibly-duplicated-append branch, which under
non-goal 35 means a human reads a history to decide whether a fact exists that
never did. **Dropping the whole cause instead was the first answer and it was
wrong**: the gate is all-or-nothing, so an application error that *also* carries
a vocabulary sentinel took its own matchability down with it — measured,
`upcastRefusal(fmt.Errorf("%w: %w", errAppUnknownShape, event.ErrRevision))`
answered `errors.Is(err, errAppUnknownShape) == false`, silently, and §2.3's
reason for `ErrUpcast`'s wrap is *the application must be able to match its own
error with `errors.Is`*. An upcaster whose complaint **is** "this revision is not
one I can read" reaches for `event.ErrRevision` because the vocabulary offers it
by that name, and Go's multi-`%w` makes co-wrapping the ordinary way to say two
things at once. Gating the target keeps both promises at once: the application's
own error is matchable, and no vocabulary sentinel crosses a class. It costs the
decorator nothing it may legitimately want — `crud.ErrForbidden`,
`crud.ErrConflict`, `crud.ErrUnavailable` and every `*errs.Fault` still arrive,
and a cause that wrapped `event.ErrConflict` to mean 409 still renders 409
through `crud.ErrConflict`, which is what it asked for and is not a member of
this vocabulary. It is the kernel enforcing a partition the kernel is the one to
promise.

The `Outcome` → refusal map, total and fixed:

| Store returns | Caller gets |
|---|---|
| `Failure(Conflict, c)` | `ErrConflict`, class-wrapping `crud.ErrConflict` |
| `Failure(NotWritten, c)` | `ErrBackend`, class-wrapping `crud.ErrUnavailable` iff `c` carries it — the framework spells that class two ways and both count: the sentinel a package wraps, and an `*errs.Fault` whose `Kind` is `errs.KindRetryable`, which is the spelling a SQL driver's classification actually produces |
| `Failure(Unconfirmed, c)` | `ErrUncertain`, wrapping no class; **`c` is unreachable by `errors.Is` and by `errors.As`**, and reachable through `CauseOf` (C3, GAP-135) |
| `Failure(Closed, c)` | `ErrClosed` |
| `Failure(BadCursor, c)` | `ErrCursor` (wiring, C5) |
| `Failure(Refused, c)` | `ErrRefused` (store, C2); `c` is the declared wrap, reachable by `errors.Is` **and** `errors.As`, its text not travelling — except for a target that is one of the twenty-four, which `sentinel` alone answers (GAP-147), and except for a `c` the kernel cannot read to the end inside `causeHops`, which is not promoted at all and is reachable through `CauseOf` alone (GAP-158) |
| `Failure(Unclassified, c)`, an outcome outside the seven (which `Failure` normalised), or any error that is not a `Failure` | the door's fail-safe default: `ErrUncertain` from `Append`, `ErrBackend` from `ReadStream` and `ReadAll` |
| a page whose first version is not `after + 1`, whose versions do not step by one, which carries another stream's envelope, or whose positions go backwards | `ErrBackend`, store class — the kernel's own verification, not a store's classification |
| an error that reaches `context.Canceled` / `DeadlineExceeded` | **the bare sentinel** — the one override. Not the store's value: `fmt.Errorf("read %s at v%d: %w", key, after, ctx.Err())` is what a competent implementer writes, and returning it puts the stream key and the version — the two values §INV-025 names first — into the caller's error string and the operator's log (GAP-148) |
| a non-nil error from `Transaction` | `ErrAmbientNotTransaction`, **carrying it as a cause** — reachable through `CauseOf` and by neither traversal, which is what the row two above says of every sentinel with no declared wrap (GAP-165). An implementer who read "wrapping it" would write `fmt.Errorf("%w: %w", …)` and hand a store's incidental `crud.ErrUnavailable` the transport status of a wiring refusal |
| a non-nil error from `Close` | nothing; `Close` returns nil every time by contract |

Every sentinel is exported because `errors.Is` is what a caller matches; §D.16's
argument for the count is restated in `## Architecture metrics`.

#### `event/store.go` — the extension point

```go
type Support uint8
const (
	Unstated Support = iota   // the zero value; Bind AND Read refuse it (INV-043)
	Unsupported
	Supported
)
func (this Support) String() string

type Capabilities struct {
	Transactions       Support
	Persistence        Support
	MonotoneVisibility Support
	SharedBacking      Support
}

type Limits struct {
	MaxPayload int
	MaxBatch   int
	MaxKey     int
	StreamPage int
	MaxRead    int
}

type Record struct {
	Type     string
	Revision int
	Payload  []byte
}

type AppendRequest struct {
	Stream   Stream    // already validated by the kernel
	Expected Version   // the token's version; 0 means "never appended to", never "any"
	Records  []Record  // already encoded, already inside every bound
}

type Envelope struct {
	Stream     Stream
	Version    Version
	Position   Position
	Type       string
	Revision   int
	Payload    []byte
	RecordedAt time.Time
}

type Log interface {
	Capabilities() Capabilities
	Limits() Limits
	Backing() Backing
	ReadAll(ctx context.Context, after Cursor) ([]Envelope, Cursor, error)
}

type Store interface {
	Log
	Transaction(ctx context.Context) (Authority, error)
	ReadStream(ctx context.Context, s Stream, after Version) ([]Envelope, error)
	Append(ctx context.Context, req AppendRequest) error
	Close() error
}
```

`Support` is a tri-state and it is the **first in this repository** —
`jobs/durability.go:223` and `storage/types.go:211` are plain `bool`s and neither
can tell *unclaimed* from *not stated* ([REC] §3.2). D-122's sibling paragraph
records what it is for, or the next capability struct is `bool`s again.

Every ownership obligation §INV-021 places on a store is a comment on the field
it governs — the one place a comment earns its keep under this repository's rule,
because it is an invariant the code cannot make visible:

- `AppendRequest.Records` — the kernel's slice; read it for the call, never write
  into it, never retain it past the return (hand-off 6).
- `Record.Payload` — the kernel's array and the kernel reads it again; read it,
  clone it if you retain it, **never write into it, not even temporarily**
  (hand-off 3). `io.Writer` states the identical rule for the identical problem.
- `Envelope.Payload` and the `[]Envelope` — yours to give away, **including their
  capacity** (C7); clone what you retain **and what you do not own** — a driver
  buffer valid until the next fetch, an `mmap`ed page, a library scratch — and
  reuse neither across two calls (hand-offs 4 and 7).

**`Expected: 0` carries its clause on the field, and it is the one clause of the
three that a store gets wrong by reading the field alone** (GAP-151). §D.14 writes
all three verbatim and an earlier draft of this section dropped them; the file
whose whole purpose is to be read by an implementer in another package is the one
place they are not optional. A store author reading a bare `Expected Version`
writes the idiomatic guard — `if req.Expected != 0 && current != req.Expected` —
because an unsigned zero reads as "no expectation" in Go and
`AppendRequest{Stream: s, Records: r}` produces it by default. The result is that
**two concurrent creations of one fresh aggregate both succeed**: a lost update at
exactly the point §UC-022's optimistic concurrency is supposed to bite, on the one
version where a re-read afterwards cannot see it.

**What a read must return, as a property of the contract rather than of one
store.** These are stated here because `eventtest` must be able to certify two
stores as one contract, and because §INV-006 is otherwise enforced for every
cause but this one:

- `ReadStream` returns envelopes of **the stream that was asked for**, whose
  **first version is `after + 1` and each subsequent one exactly one higher**.
  Because the kernel's only caller replays from `after = 0` (§INV-008), the
  concatenation of the pages is dense from 1 — *dense from 1* is a property of
  that concatenation and never of a page, and reading it as a per-page rule fails
  every page but the first.
- **A short page is the end of the stream**, and the kernel trusts it: it stops
  when a page is shorter than `Limits().StreamPage` and issues no confirming
  read. The alternative — read until an empty page — costs one extra round trip
  on *every* load, which on the common shape (three events, one page) is a second
  read for a first, and on a 391-page load is 0.25 %. A store that returns a
  short non-final page truncates a stream silently, and that is what the
  **short-page defect** is for: it fails `stream paging`, and per GAP-25 also
  `dense versions` and `conservation`.
- `ReadAll` returns envelopes at **ascending positions**. Gaps are permitted and
  normal (§INV-009); going backwards is not.
- A read issued while a transaction of this store's backing is bound returns that
  transaction's own **staged, uncommitted** appends as well as the committed
  ones. Read-your-own-writes is the SQL behaviour and it is what makes §UC-021's
  re-load an ordinary program; a store whose staging buffer is invisible to its
  own reads is not conformant.
- A **second append inside one transaction** is admitted at the version the
  first produced, not at the committed version — that is the same statement seen
  from the write side, and §UC-028's "two appends in one transaction carry
  authorities that compare `Same`" depends on it.
- **A conflict is reported from `Append`, never from the caller's own commit.**
  §UC-022's `[edge]` Observed is a *typed* conflict, and a caller that writes
  `if errors.Is(err, event.ErrConflict) { reload and decide again }` around
  `Append` must take that branch on every store. `eventpg` gets it from the
  unique index on `(family, key, version)`, whose losing `INSERT` waits and then
  raises; `eventmemory` gets it from a per-stream claim. **Whether the store waits
  for the competing transaction or refuses at once is not promised** — only the
  door and the class are.
- **A store refuses for cancellation with the bare `context.Canceled` or
  `context.DeadlineExceeded`, never with a `Failure` carrying one.** C3 makes the
  opaque wrapper answer `false` for both sentinels unconditionally, so a
  cancelled-before-issue append classified as `Failure(NotWritten, ctx.Err())` —
  a natural choice for an implementer who classifies everything — loses its
  cancellation identity entirely: the caller sees `ErrBackend`, treats a client
  disconnect as a backend failure, retries it and alerts on it. A classified
  cancellation is **not** recoverable by `errors.Is`, and that is by design
  (§UC-057's precedence rule); the bare form is how the first window is spelled.

**The kernel verifies the two shape clauses, per page.** §INV-018's import
inversion is about a store's *package*, not its numbers: the versions come from
the envelopes themselves and the stream from the request the kernel issued. Cost:
three comparisons per envelope, against one decode and one fold — and the
alternative is the worst observable this subsystem has, because a page returned
as `[v3, v1, v2]` folds to a silently wrong state with the right version, the
right count and no refusal anywhere. A violation is **`ErrBackend`**, store
class, beside C4's over-long page.

**What a refusal leaves the caller's ambient transaction in, per class.** Under
[[D-118]] the store writes **inside the caller's transaction**, and on PostgreSQL
any statement that raises aborts the block: every later statement answers
`25P02` until `ROLLBACK` or `ROLLBACK TO SAVEPOINT`. So the aftermath is part of
the contract or `eventtest` certifies two stores that need two different callers:

- a refusal the **kernel** raises before it reaches the store — every
  declaration, wiring and request-class refusal, the empty-append short circuit,
  and every history-class refusal, which the kernel computes from a page that
  already arrived — leaves the transaction exactly as it found it, because
  nothing was issued;
- `Failure(Closed, …)` and `Failure(Refused, …)` leave it usable too. Both mean
  *refused rather than tried*, which is precisely what those two outcomes are
  for, and it is what keeps §UC-048's quota wrapper from forcing a caller to
  abandon an unrelated transaction;
- **every other refusal from `Append`, `ReadStream` or `ReadAll` must be assumed
  to have made the caller's transaction unusable.** The caller's obligation is to
  roll back — to its own savepoint if it took one, otherwise the whole
  transaction — and to issue nothing else on it first.

A store **may** be more forgiving; it may not **promise** it. `eventmemory` is
more forgiving — a losing `Append` staged nothing and released nothing, so its
`*Tx` is intact — and no conformance case is permitted to rely on that, or
`eventpg` would be certified against a promise PostgreSQL cannot keep without an
internal savepoint per append. Turning it into a promise was considered and
refused twice over: a savepoint the store issues is transaction control on a
transaction the framework does not own, which is what §INV-027 exists to forbid;
and a **capability** carrying the answer per store would make the caller write
two programs, which is the thing `eventtest` exists to prevent.

The consequence for §UC-022 is that **the retry unit is the transaction, not the
append**: on a conflict the caller rolls its own transaction back, begins a new
one, and reloads there. That program is correct on both stores, and it is the
ordinary optimistic-concurrency shape on SQL.

**`Capabilities()` and `Limits()` are constant for the store's life, and the
kernel reads each once per door** — at `Bind` and at `Read` — and retains the
answer. That is what makes §INV-031's "an empty append touches no store"
literally zero calls rather than two, and what makes §D.4's constant-time claim
true by construction. A store that varies either after binding is bound by its
first answer; the suite cannot see that and does not claim to (`## Debt`).
**`Backing()` is read per operation**, alone among the three, because it is the
one whose staleness lets a write land in the wrong database: comparing a token
against a *remembered* backing cannot see a store that re-pointed, and comparing
against the live one can. On any store it is a field read.

**A store's panic is not recovered.** The kernel recovers a codec's and an
upcaster's, and the rule that separates them is on `event/codec.go`. A store has
an error channel *and* an `Outcome` vocabulary, so recovering its panic would
mean the kernel classifying a failure the store did not classify, which is
§INV-045's line. What a panicking store leaves behind is the caller's own open
transaction, and closing it is already the caller's job under [[D-118]] —
`## Risks` R14.

**The seam carries no transaction control, and that is §INV-027's first
enforcement.** None of the eight methods opens, commits or rolls back anything;
`Transaction(ctx)` *asks* what is bound and answers. So "the framework never
opens, commits or rolls back a transaction" is true by the shape of the
interface before any test runs — the tests pin that the kernel does not reach
around it (S6's AST walk) and that it makes exactly the calls the contract names
(S4's recording store).

**The trust boundary, stated rather than assumed.** A store is any
`event.Store` — a shared database another service also writes, a restored dump,
a store behind a decorator, in phase 2 a PostgreSQL table with its own grants. So
the kernel trusts a store's bytes and strings **exactly as far as it trusts its
numbers**, which is not at all:

- `Envelope.Type` is checked against the kernel's identifier rule —
  `MaxNameBytes`, valid UTF-8, no NUL, no control character — before it is used
  for anything. A type name that fails it names no declared fact and can name
  none, so it is `ErrUnknownType`, history class, the same answer a legal-but-
  unknown name gets.
- **`Envelope.Stream.Family` is store data under the same rule, and a rendering is
  a use** (GAP-150). `Stream.String` reads the identifier rule before it renders
  the family and answers `"[stream unnameable]"` when it fails, so the refusals
  C4's page verification raises over an envelope a store built cannot carry a
  newline or a megabyte into a log line.
- **A stored type name travels in a refusal only when it passed that rule.** It
  is data from a store, not a declared identifier, and §INV-025 does not let data
  travel: a refusal over a type name that failed the rule names the stream and
  the byte count and nothing else. That is what keeps a 1 MiB type name out of a
  log line and a newline out of the middle of one.
- `Envelope.Revision` below 1 or above the fact's `Revisions()` is `ErrRevision`,
  history class — a bound, not an assumption.
- `Envelope.Payload` above `Limits().MaxPayload` is `ErrPayload`, history class
  (C5).

What the kernel does **not** bound on the decode path is stated on
`event/codec.go` and recorded in D-124, so phase 2 knows what it inherits.

#### `event/codec.go`

```go
// A codec is called from every request goroutine concurrently and must be safe
// for that (C9). Decode may return a value that aliases the input and may return
// freshly allocated memory; it must not return one that aliases memory the codec
// itself will write or reuse (C1). A panic out of any of the three is recovered
// into the refusal that method's own error produces.
type Codec[V any] interface {
	Encode(V) ([]byte, error)
	Decode([]byte) (V, error)
	CanEncode() error
}

func JSON[V any]() Codec[V]
```

**The panic policy for every extension point, and the rule that decides it.**
`microkernel.md` requires a failure policy in writing for each; §INV-017 already
states one for two of the callbacks, and the asymmetry it describes is arbitrary
until the rule is named. The rule:

> **The kernel recovers a panic from an extension that has a stated refusal
> channel for the same failure, and maps it to the sentinel that channel already
> produces. It recovers nothing else.**

| Extension | A panic |
|---|---|
| `Codec.Encode` | **recovered** → `ErrEncode`. Reachable with the shipped codec: `encoding/json` re-panics a user `MarshalJSON` panic, and `Fact.New` already carries "a marshaller that errors" |
| `Codec.Decode` | **recovered** → `ErrPayload`, history class — at `Load`, at `Fold` and inside `RoundTrip`. A protobuf, msgpack, gob or hand-rolled binary reader panics on a truncated or hostile buffer, and `Codec[V]` exists to admit exactly those; the bytes are a store's and the trust boundary on `event/store.go` says so. Unrecovered it unwinds mid-page, past the kernel's page release, into the request goroutine with the caller's transaction open |
| `Codec.CanEncode` | **recovered** → `ErrCodecType` → `ErrDeclaration`, panicked at declaration as every declaration refusal is |
| an upcaster | **recovered** → `ErrUpcast` (already decided; `jobs/upcast.go:71:upcastOwned`'s shape) |
| a fold | **not** recovered (§UC-067) |
| the identity mapper | **not** recovered |
| a store method | **not** recovered (`event/store.go`) |
| `eventmemory.Spec.Clock` | **not** recovered |

*Why the line falls there.* A codec and an upcaster return an `error` for the
failure a panic is a second spelling of, so mapping the panic to that sentinel
tells the caller what the error would have told it. A fold, a mapper and a clock
have **no** error channel at all — §INV-017 says a fold cannot fail — so
recovering one would have to invent a failure mode the contract denies, and the
application's own bug would arrive as a framework refusal. A store has both an
error channel and an `Outcome` vocabulary, so recovering its panic would be the
kernel classifying what the store did not classify (§INV-045).

*Test:* `TestAPanickingCodecBecomesTheRefusalItsErrorWouldHaveBeen` (S4) drives a
panicking `Decode` through `Load`, a panicking `Encode` through `Fact.New` and a
panicking `CanEncode` through `Declare`, with the same codec returning errors
instead as its control — so a kernel that recovered nothing and a kernel that
recovered into the wrong sentinel both fail.

`event.JSON` is `encoding/json` plus a bounded, visited-set `CanEncode` walk on
`jobs/json.go:1062:discover`'s model with its three limits — and **nothing else**:
no hardened scanner, no transient-work charge model, no `json_mode_v*.go`
build-tag pair. `jobs/json.go` (1407 lines) and `cache/codec.go` (1564 lines)
already share **20** identically-named unexported functions; a third copy would be
the third ([REC] §3.3).

**What the walk asks, corrected in S2 (GAP-170, GAP-171, and again in round 2 by
GAP-178 and GAP-179).** `discover`'s question is *"does this type graph contain a
kind `encoding/json` refuses"*, and that is the wrong question for a fact log:
`encoding/json` answers `nil` for a type it writes as `{}`, the change then
carries no refusal, and the empty payload is recorded **durably and irreversibly**
and replayed as the zero value with the right version and no error at any door.
The walk asks instead *"does a value of this type survive `Encode` then `Decode`
through this codec"*, and refuses eight shapes `discover` accepts:

1. a type that **writes itself and declares no matching unmarshaller that
   `encoding/json` can reach** — `MarshalJSON` without `UnmarshalJSON`,
   `MarshalText` without `UnmarshalText`. The reader must be on the **pointer
   receiver**, because that is how `encoding/json` calls it; one declared on the
   value receiver is refusal 7. The bytes are written and are then unreadable
   forever, which in an append-only log is worse than a rejected write;
2. a type whose marshaller sits on a **pointer receiver at a position
   `encoding/json` cannot address** — a map key or a map value. There it is
   written field by field and read back through `UnmarshalJSON`, so the two
   halves disagree;
3. a **map key whose two text methods do not come as a pair**, whatever the
   key's kind. `encoding/json` routes a key through `MarshalText` when the key
   type has one and reads it back through `UnmarshalText` when `*K` has one, and
   it asks both **before** the string and integer kinds it renders on their own —
   so `type Currency string` with a `MarshalText` written for display and no
   reader writes `cur:usd` into the log and reads it back as the currency code
   `cur:usd`, for ever, with no refusal anywhere. A key that declares only
   `UnmarshalText` is refused for the mirror reason, and a key is never
   addressable, so a `MarshalText` on the pointer receiver is not one it has.
   The reader is read the same way refusal 1 reads it — on the **pointer
   receiver**, since a key whose `UnmarshalText` sits on the value receiver
   writes a rendered name and reads back the zero key, which collapses a whole
   map to one entry;
4. a **struct with fields and no field `encoding/json` writes** — every field
   unexported, or every field `json:"-"`. `struct{}` stays legal: a marker fact
   carries no data by design;
5. **one JSON name claimed by two fields**, whether both are declared on the
   struct, both are promoted from embedded structs at one depth, or one shadows
   the other from a shallower one. `encoding/json` writes at most one of them and
   `go vet`'s `structtag` sees neither the second nor the third: it reads tags
   against tags, never a tag against the field name beside it
   (`Amount int \`json:"Reason"\`` next to `Reason string`), and it says nothing
   at all about an **untagged** promoted pair, which is how embedding is
   ordinarily written. So the walk computes the names a struct renders the way
   `encoding/json` does — following embedded fields, one type per path — and
   refuses a name claimed twice by any route, with one remedy for all of them:
   tag one, or spell it `json:"-"`;
6. a **struct written by a marshaller it promoted from an embedded field, with a
   field of its own beside it** (S2, round 3, GAP-185). Go promotes an embedded
   type's methods, so `struct{ Money; SKU string }` and `struct{ time.Time; Note
   string }` — the textbook Go embedding — write themselves as the embedded
   value and nothing else: `SKU` and `Note` are written by nobody and read back
   by nobody, with `nil` at `CanEncode`, at `Encode`, at `Fact.New` and at
   `Fold`. Tagging the embedded field does **not** undo the promotion, and
   `reflect` will not say whether a method is declared or promoted, so the
   question is asked of the embedded fields: one carrying the route the struct
   writes by, and any field beside it, is the shape. A struct that declares its
   own pair *and* embeds a marshalling type is refused too — a conservative
   false positive that fails closed with one remedy, name the embedded field;
7. a type whose **unmarshaller sits on the value receiver** (S2, round 3,
   GAP-186). `encoding/json` calls it through the addressable pointer, so the
   method runs against a copy, everything it writes is discarded and
   `json.Unmarshal` answers `nil`: the fact is durable and every replay of it,
   for ever, produces the zero value with the right version and the right count.
   The compiler is silent and `go vet` has no check for it. It is refusal 1's
   own failure reached through a reader that is present and inert, so it carries
   a message of its own rather than *"declares no `UnmarshalJSON`"*, and the
   same question is asked of a map key;
8. a field reached through an **embedded pointer to an unexported struct type**
   (S2, round 3, GAP-187). `encoding/json`'s promotion rule is asymmetric: it
   **writes** the promoted fields and cannot read one back, because `reflect`
   may not allocate a pointer it may not set. The fact is minted with `nil` at
   every door, lands durably, and every `Load` of that stream fails for ever with
   no repair possible from inside the application. The embedded **value**
   spelling round-trips and stays legal, so the refusal is raised where a name is
   recorded rather than where the pointer is seen: a promoted name that is never
   rendered — which is what a type embedding a pointer to itself has — costs
   nothing and stays legal too.

`Encode` marshals `&value` rather than `value`, which is the other half of (2):
without the pointer the top-level reader type is not addressable either, and the
ordinary `func (v *Doc) MarshalJSON()` is never called for the payload itself.
The walk therefore carries **addressability** beside the type — set at the reader
type, behind a pointer, at a slice element and at an element of an addressable
array; cleared at a map key and a map value — and its visited set is keyed by
that pair, because one type is legal at the reader type and illegal as a map
value.

All eight refusals share `ErrCodecType` and differ only in what they say, and
each is subsumed by the arm beside it as a *verdict* — so
`declaration_test.go`'s "a refusal names which asymmetry it found" pins the
diagnosis by name, seventeen rows of it. Without it, deleting the
write-without-a-reader arm leaves every case still refused, through a message
that sends the reader to the wrong repair.

**The walk lives in `event/encodable.go`, not in `event/codec.go`** (S2, round 2).
The two answer different questions — one is the extension point and the shipped
implementation of it, the other is *what a payload may be*, which is the walk plus
`encoding/json`'s routing rules restated as refusals — and the second was 290 of
the file's 393 lines after GAP-178 and GAP-179, seven lines under the 400-line
threshold with GAP-176's repair still owed. `event/codec.go` keeps `Codec[V]`,
`JSON`, `jsonCodec` and the three recovering call helpers.

*The argument for the smaller codec, and what it does not claim.* A job payload
and a cache value are values a **caller** hands the framework, so their analyser
hardens against a caller. An event payload arrives from a store, behind
`Limits().MaxPayload` which the kernel applies to the stored bytes before they
reach a codec (C5's `ErrPayload`), and behind the identifier, revision and
ordering checks stated on `event/store.go` — so the *decode* side is bounded in
**bytes** and unbounded in what those bytes expand into. What that leaves, named
so phase 2 inherits it knowingly rather than by silence:

- there is no `MaxDecodedBytes` and no `MaxPayloadDepth` as `jobs` has
  (`jobs/bounds.go:22-23`); depth is bounded only by `encoding/json`'s own
  nesting limit and the decoded size only by the payload cap times the expansion
  factor of the reader type, which for a declared struct is small;
- the trust boundary is therefore **the byte cap and the field checks, not the
  provenance of the log**. "It is our own log" is not the argument and is not
  true in general;
- a deployment whose store is written by something outside this system, or whose
  events are restored from an untrusted dump, declares a codec of its own —
  which is the whole reason `Codec[V]` is an extension point (§INV-023).

D-124 records all of that, including the trigger for extracting a shared
analyser, and `## Debt` carries hardening the decode path as the change that
retires it.

`CanEncode` is charged **once at codec construction** and returned from a stored
field, exactly as `jobs/json.go:35:jsonCodecFor` caches `chargeErr` — which is
what makes §D.4's "`Bind` costs a fixed number of constant-time checks" true by
construction
([REC] §2.2).

#### `event/chain.go`

```go
type Chain[V any] struct{ /* opaque */ }

func From[V any](codec Codec[V]) Chain[V]
func Then[A, B any](prev Chain[A], codec Codec[B], up func(A) (B, error)) Chain[B]
```

A chain's **position is the revision**: `From` is revision 1, each `Then` the
next. There is no revision number to type, so none to typo, duplicate or leave a
gap in (§INV-010), and four of `jobs/definition.go:341:normalizeUpcasters`'s five
validations become unnecessary rather than implemented ([REC] §1.2).

Internally a chain holds one erased reader per retained revision:
`decode func([]byte) (V, error)` — that revision's own codec, then the declared
upcasters to `V` — plus that revision's own `selfEncode func(any) ([]byte, error)`,
`selfDecode func([]byte) (any, error)` and `selfZero` over its own type, and its
type name, all built by generic closures so **no `reflect` is needed**. The two
self readers pass values as `any` (S2, GAP-172): the aliasing half of a round trip
compares two encodings of **one value in the revision's own type**, and an
upcaster standing between the aliased buffer and `V` converts and therefore
copies, so a comparison made after one is vacuous for every revision but the
last. Decoding is **once into the revision's reader type and a typed value
carried forward**, never `jobs`'s byte-at-every-hop replay, which would be *n*
encode/decode round trips per stored event on the path §UC-011 pages to keep
bounded ([REC] §1.2).

An upcaster's **panic is recovered** into `ErrUpcast` wrapping nothing
(`jobs/upcast.go:71:upcastOwned`'s shape). A **fold's panic is not recovered**
(§UC-067) — the asymmetry is §INV-017's whole content, and the rule that decides
which extension falls on which side is stated once, on `event/codec.go`.

#### `event/aggregate.go`

```go
type Aggregate[S any, ID any] struct{ /* opaque */ }

// The mapper is retained on the declaration and called from every request
// goroutine concurrently; it must be safe for that (C9), and it must be
// injective over the aggregate's identity domain (INV-033) — an obligation the
// framework cannot check and eventtest.Keys is the runnable proxy for. Its
// panic is not recovered: it has no error channel to be a second spelling of
// (event/codec.go).
func Define[S any, ID any](family string, key func(ID) Key) *Aggregate[S, ID]
func TryDefine[S any, ID any](family string, key func(ID) Key) (*Aggregate[S, ID], error)

func (this *Aggregate[S, ID]) Family() string

// S2 ADDITION. eventtest.Keys asserts the mapper is injective and therefore
// needs the rendered key; an unexported seal() cannot be called from another
// package, so C10.1's two eventtest readers reach the seal through the two
// exported methods they were always going to call. Family() is eventtest.Families'
// and Bind's; Key(id) is eventtest.Keys'. Both seal, and it is also the one
// spelling of "which stream does this identity live in" a caller can write.
func (this *Aggregate[S, ID]) Key(id ID) (Key, error)

// Runs the caller's own folds over the caller's own value: `state` is CONSUMED,
// written through for every reference kind it reaches. Take the result (UC-041).
func (this *Aggregate[S, ID]) Fold(id ID, state S, changes ...Change[S]) (S, error)

type Declaration interface {
	Family() string
	// contains an unexported method, so only *Aggregate implements it
}
```

`Fold`'s four causes, in `Append`'s own order — (1) the identity renders an
illegal key → `ErrKey`; (2) a change decided for another stream → `ErrWrongStream`;
(3) a change's own carried refusal; (4) a decode failure at fold time →
`ErrPayload`. Causes 1–3 run before any fold and return the input untouched;
cause 4 fires mid-list and returns the state as of the last change applied, which
for a reference kind **is** the argument (§UC-041, GAP-96).

**Cause 2 defers to cause 3 for a change that never reached a stream** (S2,
GAP-173). A change whose mapper rendered an illegal key carries `ErrKey` and a
**zero** `Stream`, and a zero stream equals no legal one — so comparing streams
first answers a request-class refusal with a wiring-class crossing, and tells a
caller that mints a change from a request-supplied identity that the server broke
over data only the caller can correct. `Change.decidedFor` is the one comparison:
equal stream, then a carried refusal, then the crossing. `ErrWrongStream` is
raised through `fmt.Errorf` naming the wire type — never the key, §INV-025 — as
every other refusal in the section is; it was the one bare sentinel.

`Declaration` has exactly two readers in phase 1 — `eventtest.Families` and the
`Binding`'s bound-family set — and no third is invented for it.

**The seal is monotone, so the second observation and every one after it answer
without the lock** (S2 round 2, GAP-181). `sealed` is an `atomic.Bool`, `seal()`
returns on a `Load` that is already `true`, and only the first observation takes
the mutex; `declare` still reads it **under** the mutex, so a fact arriving
concurrently with a first observation is either accepted into the table or
refused with `ErrSealed` and never both. A real deployment holds one `*Aggregate`
per aggregate type and shares it with every request, so the unconditional
`Lock`/`Store`/`Unlock` under every `Fact.New` and every `Aggregate.Fold` made one
declaration serialise the process: measured here, 20 goroutines minting on one
aggregate 165.6 ns/op against 67.1 ns/op on 64 distinct ones, and 42.4 against
43.7 after the fast path. S4 adds four more sealing readers to the same mutex.

#### `event/fact.go`

```go
type Fact[S any, ID any, E any] struct{ /* opaque */ }

func Declare[S, ID, E any](a *Aggregate[S, ID], name string, chain Chain[E], fold func(S, E) S) *Fact[S, ID, E]
func TryDeclare[S, ID, E any](a *Aggregate[S, ID], name string, chain Chain[E], fold func(S, E) S) (*Fact[S, ID, E], error)

func (this *Fact[S, ID, E]) New(id ID, payload E) Change[S]
func (this *Fact[S, ID, E]) Name() string
func (this *Fact[S, ID, E]) Revisions() int

// Encodes each value with its own revision's codec, decodes it back, carries it
// to the current type through the declared upcasters — and decodes that
// revision's zero value in between, so a codec that decodes into a buffer it
// reuses is caught rather than described (C1). A sample that encodes to the same
// bytes as the zero value cannot exercise that half and is ErrSample, not a
// pass. Reading the chain seals.
func (this *Fact[S, ID, E]) RoundTrip(byRevision ...any) ([]E, error)
```

**Six S2 clauses the contract needed and did not have**, four from the section
itself and two from round 2 of its review. (1) The second decode
is that revision's **own `selfDecode`**, not its whole reader chain: the buffer
under test is the codec's, and running the declared upcasters over a zero value
would let an upcaster that validates its input turn a working codec's round trip
into `ErrUpcast`. (2) The aliasing verdict is **`ErrPayload`**, history class —
the bytes cannot be read reliably by this declaration, which is the same sentinel
a `Decode` panic and a `Decode` error already take inside `RoundTrip`; `ErrSample`
stays for what is wrong with the **sample** (wrong count, wrong dynamic type, an
encoding equal to the zero value's). (3) `RoundTrip` **clones every codec output
it keeps across another call on the same codec** — the sample's encoding, the zero
value's, and the first re-encoding — which is §INV-021's hand-off 1 discharged by
the recipient. Without any one of the three, a codec that reuses its *encode*
buffer (which hand-off 1 permits) defeats the proxy silently: the kept bytes are
rewritten under it and the two encodings compare equal.

(4, GAP-173's sibling GAP-172) **The whole aliasing comparison stays in the
revision's own type.** Both encodings are `selfEncode` of the value `selfDecode`
returned; the reader chain runs once afterwards, for **fidelity** and to produce
what `RoundTrip` returns. Taken through `read.decode` — this revision's codec and
then every declared upcaster — the comparison is made on a value an upcaster
already **copied**, so whatever the old codec aliased is gone before the second
decode can disturb it. Since only the last link is `linkOf(codec)` with nothing
carried over it, that spelling tested exactly one revision of an *n*-revision
chain and reported a clean pass for the other *n−1* — precisely the codecs
nobody re-reads. The consequence when it is missed: a `Load` page of *n* events
of an old revision decodes into one reused buffer, and a fold that retains the
payload ends with *n* copies of the last event, with no error (§INV-021 hand-off
8).

(5, GAP-180) **The aliasing verdict is taken by comparing the two answers, not by
disturbing the codec.** Re-encoding the first answer either side of a second
decode detects reuse only where that second decode *overwrites the bytes the
first answer points at*, and the disturbing payload is the reader type's **zero
value**, whose encoding is shorter than any sample's. A codec that slices one
buffer to the payload's width and copies into it — a length-prefixed binary
codec, the shape `Codec[V]` exists to admit — never touches that region, so the
proxy passed it and caught only a fixture that writes `[32]byte{}` over its whole
buffer on every decode, which is a property of that fixture and not of the
contract. The exact question is asked instead: **decode the same bytes twice,
from two separate input buffers, and refuse when the two answers share a slice or
a map**. A value that aliases the payload it was handed aliases its own copy of
it and is permitted (§INV-021); one that aliases memory the codec keeps is the
same address in both answers. The walk compares only the **exported** half of a
struct — an unexported field is the application's own business, and a decoded
value holding a pointer to something a package keeps for ever, a time zone or an
interned constant, is ordinary and is not the reuse this asks about. The
zero-value disturbance stays as a second arm, because it reaches reuse through a
`string` leaf that the value walk cannot see the address of without `unsafe`.

(6, GAP-178) **Fidelity is a comparison, not a decode that returned no error.**
The value read back is compared with the sample **in the revision's own type**,
before the upcasters run, so a codec that records less than it was handed — the
route every promoted-field defect took to the log — is caught by the proxy rather
than handed back with a `nil` error. The refusal is `ErrPayload` and names the
reader type, never the value (§INV-025).

`reflect.DeepEqual` is **not** that comparison, and using it would have been a
false accusation on the two most ordinary payload fields there are: measured, a
`time.Time` from `time.Now()` carries a monotonic reading and a `Local` location
that no encoding preserves and whose own `Equal` says are not the difference, and
a populated **unexported** field is never written by `encoding/json` and was
never going to be read back. So the comparison walks the **exported** half of a
struct, asks a type's own `Equal(T) bool` wherever it declares one — which is how
`time.Time`, whose every field is unexported, answers at all — treats two NaNs as
one value, and **runs out of budget in the caller's favour**: this refuses a
declaration, so what it cannot answer it does not accuse. A codec that writes the
note and forgets the time is what makes the `Equal` arm load-bearing; without it a
field walk sees nothing to compare.

`RoundTrip` lives on `*Fact` rather than in `eventtest` because the algorithm is
the kernel's and needs the erased per-revision readers; `eventtest.RoundTrip` is
the `*testing.T` wrapper that reports it, and its row on the module page says
which of the two properties it proved — **fidelity** for every sample, and
**non-aliasing** only where the sample carried data, only through a slice or a
map the two answers share or a re-encoding the zero value disturbed, and only
over the exported half of what the codec returned — so a store author reading a
green run knows what it did not test. `Revisions()` is §INV-010's falsification
reader. `Name()` is what a refusal names (§INV-025).

`New` returns **one** value so a slice literal of changes stays writable; a
failure — a non-finite float, a marshaller that errors, a payload over the
**kernel's** ceiling, a mapper output that is empty or breaks the text rule — is
carried on the `Change` and surfaced by `Append` before the store is reached and
by `Fold` rather than folded as a no-op.

#### `event/change.go`

```go
type Change[S any] struct{ /* opaque */ }

func (this Change[S]) Stream() Stream
func (this Change[S]) Err() error
```

**"Three things" is a statement about application values, and the struct has
six fields** (S2, said here so nobody reads the count as a field count and
"fixes" it): the stream, the **declared wire name and revision** — which is what
`Append` builds `Record{Type, Revision, Payload}` from, and both are strings and
integers the framework itself minted — the frozen bytes, the adapter, and the
carried refusal. Not one of the six is an `S`, an `E` or an `ID`.

A `Change` keeps exactly three things: the **frozen bytes** (`bytes.Clone` plus a
full-slice expression at `New`, `jobs.EncodedPayload`'s shape), the **stream** it
was decided for, and an **adapter** that decodes those bytes and applies that
fact's fold. It keeps **no application value at all** — not the caller's payload
and not one decoded back from the bytes (§INV-042), and the adapter closes over
the *declaration* and nothing else.

Every fold decodes a **clone** of the frozen bytes, so two folds of one change
cannot disagree even through an aliasing codec (§UC-065) and the frozen array is
written by nobody.

#### `event/token.go`

```go
type At[S any] struct{ /* opaque; no exported field, no constructor, no unmarshaller */ }

func (this At[S]) Stream() Stream
func (this At[S]) Version() Version

type Commit struct{ /* opaque */ }

func (this Commit) Empty() bool
func (this Commit) Stream() Stream    // C10.6: an empty commit answers the token's stream
func (this Commit) First() Version    // the first version this append wrote; 0 when empty
func (this Commit) Last() Version     // the version the stream is at afterwards
func (this Commit) Count() int
func (this Commit) Authority() Authority
```

`At[S]` is the phase's strongest safety claim ([[D-117]]'s shape): a value that
authorises a write exists only where the framework produced it. `event.At[Account]{}`
*is* legal Go, so the claim rests on three defences in the order they hold —
(1) its key is empty and `Append` applies the kernel's key rules to the key it is
handed, (2) its backing is invalid and matches nothing, (3) the forms that
genuinely do not compile, pinned by a build-failure fixture (§INV-015).

#### `event/binding.go`

```go
func Open(store Store) *Binding                                                   // starts nothing, cannot fail
type Binding struct{ /* opaque */ }
func Bind[S, ID any](b *Binding, a *Aggregate[S, ID]) (*Repo[S, ID], error)
```

`Bind`'s five constant-time checks and one allocation:

1. the store is not nil — neither a nil interface nor a nil pointer inside one,
   which is `NewBacking`'s own predicate applied one level out → **`ErrWrongStore`**.
   `Open` still cannot fail; a composition root whose constructor returned an
   error the caller ignored reaches `Bind`, and it must get the sentinel §INV-022
   assigns to every other dishonest store rather than a nil-interface panic.
   **S4 widened this check to the two other values one missing error check
   produces**, for the same argument and at the same cost: a nil `*Binding` takes
   `ErrWrongStore` (it names no store) and a nil `*Aggregate` — what `TryDefine`
   returns beside its error — takes `ErrDeclaration`, which is the sentinel
   `TryDeclare` already returns for exactly that value. Both were a nil
   dereference at start-up before
2. the family is not already bound through this `Binding` to a *different*
   declaration → `ErrFamily`
3. `Backing()` is valid → `ErrWrongStore`
4. `Limits()` has no zero field, none above its kernel ceiling, and neither
   **read** product — `StreamPage` and `MaxRead`, each against
   `event.ResidentPage(MaxPayload)`, which is the kernel's one copy of that
   division and the one a store derives its own numbers from — above the ceiling
   → `ErrWrongStore`. The answer is **retained**; the append path's resident bound
   is the measurement at step 4 and not a third product (`event/bounds.go`)
5. `Capabilities()` has no `Unstated` field → `ErrWrongStore`. Retained too

No codec walk, no reflection, no state shared between requests — which is what
makes a store per request over a borrowed tenant lease ordinary (§UC-055).
**S4 corrects "no lock" to one uncontended lock over the family set**: §INV-038
says a `*Binding` is safe for many goroutines and a map written under none is
not. It is taken once per `Bind` at the composition root, never per request, so
the property the sentence was defending is unchanged.

Checks 1, 3, 4 and 5 are **store-honesty checks and they run at both doors**,
`Bind` and `Read` (§INV-022). The family check is `Bind`'s alone.

`Bind` is also the sixth `seal()` call site (C10.1), and it calls it directly
rather than through `Aggregate.Family`, so `seal.go`'s enumeration and
`TestTheSealRefusesALateFact`'s table both name `Bind`.

#### `event/repo.go`, `event/marker.go`

```go
type Repo[S any, ID any] struct{ /* opaque */ }

func (this *Repo[S, ID]) Load(ctx context.Context, id ID) (S, At[S], error)
func (this *Repo[S, ID]) Append(ctx context.Context, at At[S], changes ...Change[S]) (At[S], Commit, error)
func (this *Repo[S, ID]) Within(ctx context.Context) (context.Context, error)
func (this *Repo[S, ID]) Authority(ctx context.Context) (Authority, error)
```

**`Append`'s order, fixed, six steps** (C10.3 moves `MaxKey` into step 1):

1. the token's key — the kernel's text rules **and** the `MaxKey` retained at
   `Bind`, neither of which touches the store → `ErrKey`. Before the empty-append
   short circuit, so a forged `At[S]{}` is refused even when nothing would be
   written (§INV-015, §INV-031) — **then the short circuit, and steps 2-6 do not
   run**
2. every change's `Stream()` against the token's → `ErrWrongStream` (§INV-044)
3. each change's own carried refusal → `ErrEncode` / `ErrTooLarge` / `ErrKey`
4. the retained `MaxBatch` count, the retained `MaxPayload` per record, and the
   **sum of `len(Record.Payload)`** against `MaxResidentBytes` → `ErrTooLarge`.
   **S4 put the count first** — it was written second: it is the one O(1) check
   of the three, and a caller that passes a million changes would otherwise buy a
   record per change before being told the batch bound is 64
5. the backing, read from the store now and compared with `Equal`, never `==` →
   `ErrWrongStore`
6. the transaction question → `ErrNoTransaction*`, `ErrAmbientNotTransaction`,
   `ErrTransactionMismatch`

Nothing reaches the store until all six pass.

**Where the empty-append short circuit sits, and the two consequences that are
stated rather than discovered.** It sits between step 1 and step 2. §INV-031 is
explicit — *"The one check it still performs is the token's own key"* — and its
falsification is *"a recording store asserting zero calls"*, which forbids step 5
(`store.Backing()`) and step 6 (`store.Transaction(ctx)`) as much as it forbids a
statement. **Zero means zero**: step 1 reads the `MaxKey` retained at `Bind`, not
`store.Limits()`, so a recording store sees no call of any of the eight methods,
and the assertion needs no exemption list to survive.

*And the retention does not move the short circuit.* Retaining `Backing()` too
would make step 5 free as well, and it is still refused — but the reason is
§UC-031's stated observable ("a token minted over backing A and presented to a
repo over backing B *succeeds* with an empty change list"), not the cost. The two
reasons are independent, which is why GAP-121's repair stays rejected:
`Backing()` is deliberately **not** retained, because a remembered backing cannot
see a store that re-pointed at another database (`event/store.go`). So:

- **`ErrWrongStore` is unreachable for an empty append, by design.** A token
  minted over backing A and presented to a repo over backing B *succeeds* with an
  empty change list and is `ErrWrongStore` with one change (§UC-031). The wiring
  mistake surfaces at the first append that writes anything, which is the first
  append that could have done damage. The alternative costs §INV-031 its
  zero-calls falsification, and that invariant is what makes "an empty append is
  free" a fact a caller may rely on.
- **`Commit.Authority()` on an empty append is the invalid one**, per §INV-031
  and §UC-020, both of which state it in terms with the reason ("an append that
  wrote nothing was written through nobody's transaction, so it is atomic with
  nothing"). §UC-030's cross-subsystem comparison therefore **does not use the
  receipt**: an application that must prove two subsystems wrote in one
  transaction asks **`Repo.Authority(ctx)`** (C10.5), which answers the store's
  question for the context and is valid whenever a transaction of this store's
  backing is bound — including for an operation that decided nothing. That is the
  spelling the module page and the flow show, and it is the one that does not
  produce a false negative on §UC-020's happy path.

*Test:* an empty append through a foreign-backing token asserting **success**,
and an empty append inside a bound transaction whose `Commit.Authority()` is
invalid while `Repo.Authority(ctx)` compares `Same` with a second append's — each
with the one-change case as its control, so neither can pass vacuously.

**`Load`'s order:** (1) the mapper, then the same whole key check;
(2) `store.Transaction(ctx)`; (3) the paged `ReadStream`, each page verified —
its first version is `after + 1`, each subsequent one is exactly one higher,
every envelope is of this stream, and the page is no longer than the retained
`StreamPage` — then folded and released. So a `Load` with a **zero identity
against a closed store** is `ErrKey` and not `ErrClosed`, and a page that arrives
out of order is `ErrBackend` before it is folded rather than a silently wrong
state after.

**The loop stops on a short page**, and issues no confirming read; the cost of
the alternative and the defect that catches a truncating store are on
`event/store.go`.

**`Load` returns the zero state and the zero token with any non-nil error** —
never a partially folded state, which is §INV-006 in one line. `Fold` returns the
opposite (the state as of the last change applied) and the asymmetry is
deliberate: `Fold` runs over the caller's **own** value and consumed it, so
handing it back is the only honest answer (§UC-041, GAP-96), while `Load` builds
a value the caller has never seen and can therefore keep to itself.
*Test:* `TestALoadThatFailsMidStreamReturnsNothing`, whose control is a
successful load of the same stream — otherwise a `Load` that always returned the
zero state would pass it.

**`Within` returns a context**, and on a refusal it returns **the context it was
given** rather than a nil one (S4): a nil `context.Context` is a panic one line
later for a caller who ignored the error, where the unmarked context it already
held is [[D-118]]'s own answer — an ambient transaction is still joined. It is not
a bound repository and not an inner store —
which keeps a policing decorator in the path for the whole transaction (§UC-048),
deletes the declared-chain question (§UC-033), and makes
`bound.Append(otherTxCtx, …)` inexpressible. Its marker chains on any marker
already there and is resolved **by backing**, never innermost-first, so two
stores' `Within` contexts compose (§UC-028, GAP-73).

`Authority(ctx)` is C10.5's contract, stated above.

#### `event/reader.go`

```go
func ReadOnly(store Store) Log
func Read(log Log, after Cursor) (*Reader, error)

type Reader struct{ /* opaque */ }
func (this *Reader) Next(ctx context.Context) (bool, error)
func (this *Reader) Events() []Envelope
func (this *Reader) Cursor() Cursor
```

`Store` embeds `Log`, so a store passes where a log is wanted; `ReadOnly` exists
for the case where the consumer must not be able to assert its way back to the
append surface — a projector that can append is how a replay writes.

**`ReadOnly` is deliberately the one wrapper in this repository with no `Next()`,
and that is not a [[D-061]] violation but the point of it.** [[D-061]]'s rule
exists so a *capability* is not lost behind a decorator; `ReadOnly`'s entire
purpose is to remove one, and a `Next()` on it would be the method that hands the
append surface back to the consumer it was taken from. `crud.SourceOf` and its
siblings are for finding what a wrapper is hiding by accident; nothing here hides
anything by accident. Recorded in D-121 and on the module page so a [[D-061]]
review closes rather than reopens it.

`Read` refuses a nil `Log` — a nil interface or a nil pointer inside one — with
`ErrWrongStore`, and then applies the same four store-honesty checks `Bind` does
(§INV-022). `ReadOnly` of a nil store answers a **nil `Log`** rather than a
wrapper around nothing (S4), so that refusal is the one a read-only consumer
gets too instead of a nil dereference on the first page. **No
page-size parameter anywhere**: the kernel is the only party that fills a read
(§D.7, GAP-51). The page `Events()` returns is the consumer's, survives the next
`Next`, and may be fanned out to workers (§INV-038).

`*Reader` is the **one** stateful value in the caller-facing surface and is for
one goroutine at a time.

### `event/eventmemory/` — the second implementation

```go
package eventmemory

type LogSpec struct {
	MaxPayload int   // zero means the store's default
	MaxKey     int   // zero means the store's default
}
type Log struct{ /* opaque */ }
func NewLog(spec LogSpec) (*Log, error)

type Spec struct {
	Log        *Log              // required; two stores over one Log are one store
	Clock      func() time.Time  // zero means time.Now; retained and called on every append, from every goroutine (C9); its panic is not recovered
	MaxBatch   int               // zero means the store's default
	StreamPage int               // zero means the store's default, which the log's MaxPayload caps
	MaxRead    int               // zero means the store's default, which the log's MaxPayload caps
}
type Store struct{ /* opaque */ }
func New(spec Spec) (*Store, error)

// event.Store, all eight
func (this *Store) Capabilities() event.Capabilities
func (this *Store) Limits() event.Limits
func (this *Store) Backing() event.Backing
func (this *Store) Transaction(ctx context.Context) (event.Authority, error)
func (this *Store) ReadStream(ctx context.Context, s event.Stream, after event.Version) ([]event.Envelope, error)
func (this *Store) ReadAll(ctx context.Context, after event.Cursor) ([]event.Envelope, event.Cursor, error)
func (this *Store) Append(ctx context.Context, req event.AppendRequest) error
func (this *Store) Close() error

// UC-008: health.Probe's shape, satisfied structurally — nothing imports health
// ctx.Err() first, then event.ErrClosed on a closed store, then nil
func (this *Store) Check(ctx context.Context) error

// A *Tx is safe from more than one goroutine, and the binding is keyed by the
// log it was begun on, so one context carries one transaction per backing
type Tx struct{ /* opaque */ }
func (this *Store) Begin(ctx context.Context) (*Tx, error)
func WithTransaction(ctx context.Context, tx *Tx) context.Context
func (this *Tx) Commit(ctx context.Context) error
func (this *Tx) Rollback(ctx context.Context) error
```

**Eight package-level exported symbols.** The two **data-bearing** numbers live
on the `Log`, so §UC-054's agreement requirement is **structural** rather than a
check: two store values over one log cannot disagree about `MaxPayload` or
`MaxKey` because neither owns them ([REC] §3.4). The operational numbers stay on
the store, where two values may differ. `Backing()` is derived from the `*Log`,
never from the store value (§INV-016).

Capabilities: `Transactions: Supported`, `Persistence: Unsupported`,
`MonotoneVisibility: Supported`, `SharedBacking: Supported`. It certifies
everything §UC-044 lists and says, in `eventtest`'s own words, what it cannot: the
*unknown* half of failure classification, because it has no commit window.

#### The transaction and position design, stated

"Stage and re-validate at commit" decides three semantics and states none of
them, and two of the three answers it implies are wrong: a conflict reported from
`Tx.Commit` is not `event.ErrConflict`, is not produced by the kernel, and is not
in the `Outcome` map at all, so §UC-022's caller branch never fires — while a SQL
store reports the same conflict from `Append`. The store the root module tests
against would differ from the store phase 2 ships on the one observable §UC-022
exists to fix, and `eventtest` would certify both.

**State.** Everything lives on the `*Log`: per stream, the committed envelopes in
version order; store-wide, the committed envelopes in position order; one
position counter; one map of live per-stream claims. Two `Store` values over one
`*Log` share all of it, which is what makes §UC-054 structural.

**Admission is at `Append`, against committed + staged.**

1. the stream's *effective* version is its committed version plus this
   transaction's staged count for that stream;
2. `req.Expected` ≠ the effective version → `Failure(Conflict, …)` → the kernel's
   `ErrConflict`, from `Append`, exactly where a SQL store's unique-index
   violation arrives;
3. another **live** transaction holds a claim on this stream →
   `Failure(Conflict, …)` **at once, without waiting**. `eventmemory` never
   blocks on another transaction, so it cannot deadlock on two transactions
   taking two streams in opposite orders and needs no detector for it. The
   contract promises the door and the class, not the waiting (`event/store.go`),
   and the caller's obligation is the same one §UC-022 already states: reload and
   decide again;
4. otherwise the transaction takes or extends its claim and stages the records.

That answers §UC-028's second append in one transaction — the second is admitted
at `Expected` equal to the version the first produced — and `ReadStream` inside a
transaction returns the committed envelopes followed by that transaction's staged
ones, which is the read-your-own-writes the contract requires.

**A position is assigned at commit, never at stage.** Under one store-wide
mutex, `Commit` assigns positions in staging order, publishes the envelopes to
both indexes, and releases the claims — one critical section, so commit order
**is** position order.

- Nothing can commit below the newest committed position, so the newest position
  **is** the watermark §UC-037 describes, and `MonotoneVisibility: Supported` is
  honest rather than asserted. The `monotone visibility` section's "a committed
  event is immediately at or below the returned cursor" holds because there is
  never a lower position in flight.
- `Rollback` discards the staged records, **advances the position counter by the
  number of records it staged**, and releases the claims. That is §UC-029's burn
  and §INV-009's falsification case — a later append takes a higher position and
  the gap stays — obtained without ever letting an unassigned position exist
  between two commits. Nothing is reissued because nothing was issued.
- `Commit` therefore **cannot report a conflict**. It returns an error only for a
  use error — a `*Tx` already committed or already rolled back — and that error
  is `eventmemory`'s own, never an `event` sentinel.
- An `Append` with no transaction bound is stage-admit-commit in one call, so it
  takes and releases its claim inside that call and two concurrent autocommit
  appends to one stream produce exactly one receipt and one `ErrConflict`.

**Cursor encoding**, because §INV-035 makes it a value a consumer persists:
`"<log fingerprint>:<newest position, decimal>"`. The fingerprint is minted once
in `NewLog` and is the same for every `Store` over that `*Log`, which is what
makes §UC-053's foreign cursor (`Failure(BadCursor, …)` → `ErrCursor`, wiring)
and §INV-035's across-store-values resumption both work. The kernel never reads
the bytes (§UC-037).

**A losing `Append` leaves the `*Tx` intact, and that is not a promise.** Nothing
was staged and no claim was released, so the caller could keep using it — which
is more than the contract grants (`event/store.go`), because `eventpg` cannot
grant it on a poisoned SQL block. `eventmemory` therefore ships with the freedom
and no case exercises it: the suite may not assert anything a store is only
*allowed* to do, or `eventpg` fails a section `eventmemory` invented.

*Conformance (S5), each with its control:* two appends in one transaction,
asserting the second is admitted and the two authorities compare `Same`; a `Load`
after an `Append` in one transaction, asserting the staged events are visible to
it and to nothing else; two concurrent transactions appending to one stream,
asserting exactly one set of events survives and that the loser's refusal is
`event.ErrConflict` **from `Append`**; a rollback between two appends, asserting
the gap and that the second append's position is higher; and **the aftermath
case**: after a conflict inside a bound transaction the caller rolls that
transaction back, begins another, reloads and appends there, and that second
attempt lands — with a reload inside the *same* transaction after a **successful**
append as its control, so a store that refuses everything once anything has been
bound fails the control rather than passing the case.

**Degradation.** A transaction holds its streams for its own lifetime, so a
caller that keeps one open across a network call starves every other writer of
those streams with immediate conflicts. **A transaction that is never committed
and never rolled back holds them for the life of the process**: `release` is the
only writer that drops a claim, there is no timeout and no reclamation, so an
abandoned `*Tx` — a panicked goroutine, a forgotten `defer`, an early `return` —
leaves one aggregate refusing every write with a conflict class that reads
retryable and will never clear (GAP-7). `doc.go` states it where a consumer
reads it. `eventmemory` is not a production store and `Persistence: Unsupported`
says so; a store that waits instead is equally conformant. `Close` is idempotent, returns nil every time, refuses nothing, and
neither commits nor rolls back staged work (§INV-032). It also touches no log
state at all: the log is the composition root's and is shared with every other
store over it, so a close is one store value's and a sibling keeps serving
(§UC-054, §UC-055).

#### What S3 had to decide because the contract above did not say it

Nine answers the shape needed and only the implementation could supply — six
written before the code and three the S3 review forced. None contradicts a row
above; each fills a hole that would otherwise have been decided by whoever hit it
first.

| Question | Decided |
|---|---|
| What is `Transaction`'s **third** answer here — *something of this store's is bound and it is not a transaction* — when the only thing this store binds is a `*Tx`? | A `*Tx` that is **finished**, and a nil one. Both answer the invalid authority and an error, so the kernel refuses with `ErrAmbientNotTransaction` **before any statement** rather than falling back to autocommit, which is §INV-041's third row and the escape it exists to close |
| Where does a `*Tx` of **another** log leave this store? (GAP-1) | Exactly where it found it. The binding is **keyed by the `*Log` it was begun on**, so a context carries one transaction per backing and `WithTransaction` for a second log cannot shadow the first — §UC-028's own control chains two `Within` contexts for two backings and requires both stores to proceed. A single unkeyed key made the second binding replace the first for every reader, and this store then took row one — *nothing of mine is bound* — and **autocommitted a write the caller had opened a transaction for**. A store may not answer row one while its own transaction is in the context. A nil `*Tx` is the one binding that names no log: every store that has no binding of its own finds it and takes row three, because a binding that could have been meant for anyone must fail closed for everyone |
| What does the store itself answer when a caller drives it past that check, without the kernel? | `Failure(Refused, …)` at **all three** doors — `Append`, `ReadStream` **and** `ReadAll` — whose cause is `eventmemory`'s own and is reachable through `CauseOf`. Not `NotWritten`: nothing failed and nothing was written, and `ErrBackend` would tell a transport the server broke over a wiring mistake. C2's seventh outcome is what makes this spellable at all. `ReadAll` returns no staged envelope either way, because a staged envelope has no position; consulting the transaction is about the **answer**, so that one context does not produce a refusal at one read door and a page at the other (GAP-4) |
| Does `Commit` or `Rollback` read the context's deadline? | **No.** A transaction that refused to finish because its request was cancelled would hold its claims for the life of the process and starve every other writer of those streams, and there is no window here for a cancellation to be uncertain about. The five operating methods do read it, and a bare cancellation travels as itself (§UC-057's first window) |
| Is `Rollback` of a finished `*Tx` an error, as `Commit`'s is? | **Yes**, and it is the same one — `database/sql`'s shape, which `defer tx.Rollback()` beside an explicit commit already ignores. Both errors stay **unexported**: each method has exactly one, so *non-nil* carries the whole of it and the package keeps its eight exported symbols |
| Where is the re-validation at commit, given that a conflict may not be reported from one? | The claim is the mechanism and the commit is where it is **checked**: every staged record must still land at the version it was admitted at, densely, on a stream nobody else advanced. It cannot fire while the claim holds, and it is what makes a future edit to the claim logic loud instead of a silently renumbered history |
| What position does a staged envelope read back at, inside its own transaction? | **Zero**, because a position is assigned at commit and one assigned earlier would have to be reassigned or reissued. `ReadAll` is position-ordered and therefore never returns a staged envelope, to its own transaction or anyone else's |
| May a `*Tx` be used from more than one goroutine? (GAP-2) | **Yes**, and the store is what makes it safe: the two doors that act on the bound transaction resolve it **inside the section that acts**, under the log's own mutex, so a transaction another goroutine commits or rolls back in between is refused with the same `Failure(Refused, errFinished)` an already-finished one gets. Resolving it before the lock was a check-then-act: `Commit` released the staged maps in the window and the next `stage` panicked on a nil map, which `event/store.go` does not recover and which takes the process. An `errgroup` appending to two aggregates in one transaction, and a watchdog rolling one back on a deadline, are both ordinary. `Transaction(ctx)` reads the same flag without the lock, because it acts on nothing and a transaction may finish the instant after any answer |
| What does `Transaction` answer on a **closed** store? (GAP-3) | The same as an open one. It writes and reads nothing, and what the context carries did not change when the store was closed, so reporting closure — or reporting *nothing bound* while the caller's own transaction is right there — would be a fourth answer to a question the contract says has three. The refusal comes from the `Load` or `Append` that follows. `event/store.go`'s sentence said *the second* and is corrected; C10 row 4 and row 5 disagreed with each other and now do not |

`New` also refuses a spec whose `StreamPage` or `MaxRead` at this log's
`MaxPayload` exceeds what one read may hold — the kernel's own rule, through the
kernel's own `event.ResidentPage`, one door earlier and named at the line that
chose the numbers. **No store the kernel would have admitted is refused**, and
that is a property of the defaults and not only of the arithmetic (GAP-5): the
page default is `min(256, event.ResidentPage(log.MaxPayload))`, so a log at the
kernel's own `MaxPayloadBytes` still builds a store with nothing operational set —
before, it was refused over a number the caller never touched. The refusal, when
a caller does set one, names the number it set and the number it may not pass.
`Check` answers `ctx.Err()`, then `event.ErrClosed` on a closed store, then nil;
it is not one of the eight, so nothing maps it, and `Begin` answers the same
sentinel for the same reason.

**The five defaults, and where each number comes from.** `universality.md`'s rule
is that a literal states its derivation where it is chosen; these are on the two
`const` blocks that hold them.

| Default | Derived from |
|---|---|
| `LogSpec.MaxPayload` `64 << 10` | A sixteenth of `event.MaxPayloadBytes`, and §D.13's recommended payload: a fact carrying an embedded document fits, and the room left over is what a deployment raises rather than the ceiling |
| `LogSpec.MaxKey` `512` | A quarter of `event.MaxKeyBytes`, and §D.13's recommended key: a family beside a composite rendered identity |
| `Spec.MaxBatch` `64` | One decision's facts, not a migration's. At the default payload it is 4 MiB the kernel holds at once, a sixteenth of `MaxResidentBytes`; the batch's real bytes are measured at `Append` and never estimated as a product |
| `Spec.StreamPage`, `Spec.MaxRead` | `min(256, event.ResidentPage(MaxPayload))`. 256 is §D.13's recommended page — 16 MiB at the default payload — and the `min` is the resident rule applied to the default rather than restated: at `MaxPayloadBytes` it yields the 64 envelopes one read may hold, so **every** log the kernel admits builds a store |

### `event/eventtest/` — the conformance suite

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
	Tail    func(t *testing.T, s event.Store) event.Cursor   // round 2, GAP-18

	Unparsable func(t *testing.T, s event.Store) event.Cursor // round 3, GAP-25
	Window     time.Duration                                  // round 2, GAP-18
}

func Run(t *testing.T, factory Factory)

func RoundTrip[S, ID, E any](t *testing.T, fact *event.Fact[S, ID, E], byRevision ...any)
func Keys[S, ID any](t *testing.T, a *event.Aggregate[S, ID], ids ...ID)
func Families(t *testing.T, declarations ...event.Declaration)
```

Six exported symbols. `*testing.T` in a non-test file of the root module is
settled by precedent: `cache/cachetest/suite.go` does it and `check-deps` is green,
because `testing` is standard library and `check_deps` filters on
`{{if not .Standard}}` (Q9, verified in [REC] §2.5).

The factory constructs; the suite never does. `Fail` returns **false** for an
outcome it cannot produce, so *cannot* and *did not* are told apart. Hook
requiredness is derivable from the capability report for every hook, and a claimed
capability whose hook is missing **fails** — it is never skipped.

**`New`'s own contract, corrected in round 1 of S5's review (GAP-2).** It is
called **one or more times per section** and the suite holds several of its
stores live at once: `store failure classification` calls it sixteen times,
`transactions` three, and `resumption` and `durability` each call it a second
time *after* the data the section is asserting about has been written. So a store
`New` builds must **not** destroy or reset what an earlier one wrote in the same
section — a factory that reads "once per section" as licence to truncate its
backing makes `resumption`, `durability` and `foreignCursor` report a correct
store as broken, and none of those failures names the factory. Whether two of its
stores share one backing is the factory's own choice and is **asked rather than
assumed**: `Backing().Equal` is what the sections branch on, and a section that
needs the kind this factory does not build is reported *not certified*. Every
section that calls `New` mid-run says in its own comment why it may.

**`New`'s second clause, added in round 2 (GAP-17).** Every store it builds
publishes the **same `Limits` and the same `Capabilities`**, because the suite
reads both once at the door and derives every count it writes from them —
`probe.store` compares each later store's `Limits()` **and its `Capabilities()`**
with the admitted one's and refuses the section when either differs, so the whole
of the obligation is checked rather than half of it (round 3, GAP-28). Both
halves have a control: `TestAFactoryWhoseLaterStoresPublishSomethingElseIsRefused`
drives a factory whose second store publishes a wider page, and one whose second
store claims fewer capabilities, and both are reported *failed* for `binding`
while the factory that builds one kind throughout is *passed*.

**The obligation on one store value, which is a different one and was asserted by
nothing** (round 3, GAP-26). `Capabilities`, `Limits` and `Backing` are constant
for a store's life (`event/store.go`), and the parties that read them read them at
different moments: the suite keeps all three from the door, the kernel keeps the
first two from `Bind` and re-reads the backing per operation. `binding` now reads
all three twice on **one** store value with a store operation between the two
readings and refuses when any differs — the backing through `Equal`, never `==`.
The plausible stores are the ones the clause names: one deriving `Transactions`
from whatever executor is bound, one whose `StreamPage` shrinks after a
reconfiguration, one that re-pointed at another database. All three passed every
section before the clause was asserted.

**`Unparsable`, an optional hook added in round 3 (GAP-25).** It answers a cursor
this store cannot parse. Every cursor the suite can mint is one of three — `""`,
one this store minted, or one **another store** minted — and none of them reaches
the store's own parser, because a foreign cursor parses cleanly and names
somebody else. Only the store knows what it cannot read, so a literal the suite
invented would be nonsense to one store and a legal position to the next. Without
it `resumption` is reported **not certified**, which is the third word rather than
silence: the factory `eventpg` will supply builds every store over one database,
so `foreignCursor` already reports *not certified* there and without this hook
the whole of that store's cursor validation would be uncertified while the run
read *passed*. A store that reads from the beginning of its log for a checkpoint a
projector truncated re-applies every event it has ever written, which is the one
thing §UC-053 exists to prevent.

**`Tail`, an optional hook, and `Window`, a duration — both added in round 2
(GAP-18).** `Tail` answers a cursor at the current end of this store's log.
It gates nothing: without it every section still runs and the suite finds the end
by reading to it, which is the whole log once per walking section and which never
arrives at all on a log another process is appending to. Six sections take a
tail; the refusal when the read does not finish names `Factory.Tail` and the
remedy rather than the store. `Window` is the per-section deadline, zero meaning
the twenty seconds derived below, so a store whose operations are a network away
sets its own rather than being reported *failed* for a wait the contract permits
it.

**`Begin`'s disposal obligation, added in the same round (GAP-5).** The suite
rolls back every transaction it began through `probe.begin`, registered on the
section's own `t.Cleanup` at the moment it is begun — so the transaction
`lifecycle` deliberately abandons across a `Close`, and every transaction a
refusing case leaves through `panic(abort{})`, is released when that section ends
rather than when the binary does. `eventmemory` garbage-collects an abandoned
transaction and shows nothing; a `eventpg` one holds a pooled connection and
every row lock it took, and the next section's `New` draws from that pool.

**`Fail`'s own contract, written down in S5 because the section that drives it
has to know what it armed.** It makes the store's **next** `Append`, `ReadStream`
or `ReadAll` answer a failure classified as the named outcome *instead of
performing the operation* — so `NotWritten` is a write that certainly did not
land, and the section asserts zero events on it. `eventmemory` answers `false`
for `Unconfirmed` and true for the other six, which is §UC-044's worked case: it
has no commit window, so a decorator of its own that reported one would be
lying about the store rather than injecting into it.

**`Tx`'s own contract, moved out of this plan and into the shipped package in
round 2 (GAP-19).** Four rules the sections assert and only this document
carried: `Commit` answers an error when the transaction has already been
committed or rolled back — `staged` commits twice on purpose — `Rollback`
answers nil wherever the suite calls it, and the suite rolls back every
transaction it began when the section that began it ends, committed ones
included, ignoring that answer. They are now the doc comment on `eventtest.Tx`,
because a store author implements against what the package says. The fifth rule,
which is `Store`'s rather than `Tx`'s, went to `event/store.go`: **all eight
methods are safe for concurrent use**, since one `Repo` is the handle every
request goroutine shares — the clause `concurrency` runs eight goroutines
against.

**`Tx.Commit`'s error contract, because the suite has to know what to do with
one.** A commit-time error is the **store's own**, of the store's own class, and
the suite never asserts an `event` sentinel on it — a conflict arrives from
`Append` and nowhere else (`event/store.go`). So the suite treats a non-nil
`Commit` in any case but the deliberate double-finish as a **failure of that
case**, reported with the store's error text, and it has exactly one case that
expects a non-nil one: committing a `*Tx` that was already finished.

**Three words, never two** — *passed*, *not certified*, *failed*, and after
round 1 a fourth that is not a word the suite ever reports on a run that
finished: `unreported`, the **zero value**, which `Run` turns into a `t.Error`
and `certified` does not count (GAP-7). `cachetest`'s
`t.Skip` at **seven** sites (`cache/cachetest/suite.go:214, 250, 1508, 1674,
1697, 1715, 1721`, counted) is the shape §UC-044 refuses and is deliberately not
copied: `grep -c "t.Skip" event/eventtest/*.go` over the **suite's own files** is
0. The one occurrence in the package is
`suite_test.go`'s `t.SkipNow()` inside a **factory hook**, which is how
`TestASectionThatNeverReturnedIsNotReportedPassed` drives a section out through
`runtime.Goexit` — the same door `t.Fatal` leaves by, and the only one of the two
a watching test can observe without the failure propagating to itself.

#### The section inventory, in code, and the test that it was honoured

Every section is a `t.Run` subtest, and **`go test -list` cannot see a subtest**.
So a suite shipped with twelve of the twenty sections written passes every
`-list` count clause, passes `go test -race ./event/...`, prints twelve *passed*
lines and looks complete. The four anti-vacuity rules do not close it: a missing
hook is not a missing section, "every section was skipped" catches only the
all-skipped case, and `defects.go`'s inventory proves the existence of the
sections **a defect names** — which excludes `concurrency`, `refusal classes` and
`lifecycle`, three of the sections the carried gaps are proved in.

So the sections are an inventory on `defects.go`'s model: one slice, built by an
unexported function so nothing is package-level and mutable (§INV-013), iterated
by `Run`. **Twenty names, verbatim**, so the code and this document can be
diffed:

```
binding            stream identity   expected version   dense versions
global order       conservation      stream paging      global paging
resumption         bounds            payload ownership  refusal classes
cancellation       lifecycle         concurrency        transactions
durability         shared backing    monotone visibility
store failure classification
```

**`expected version` asserts the version-zero case explicitly** (GAP-151): two
appends at `Expected: 0` against one fresh stream, the second refused
`ErrConflict`. The idiomatic misreading of an unsigned zero as "any version"
passes every other case in that section and fails this one, so the trap is
certified rather than assumed.

Fifteen run always; `transactions`, `durability`, `shared backing` and
`monotone visibility` are gated on their capability and `store failure
classification` on the `Fail` hook — and a gated section that does not run is
reported **not certified**, which is one of the three words, so all twenty are
reported on every run. `self-falsification` is **not** in the inventory and that
is deliberate: [SPEC] §D.9 makes it the one run whose `Factory` the suite writes
itself, and C8 moved it into `defects.go` plus
`TestTheSuiteStillDetectsEveryDefectItWasBuiltToDetect`. Running the suite's own
defect fixtures inside a store author's run would report another store's failures
against theirs.

*Test:* `TestEverySectionInTheInventoryWasReported` runs the suite against a
fixture store and asserts every inventory name was reported **exactly once** with
one of the three words. Its control is a run over the inventory with one section
removed, which must **fail** the same assertion — otherwise the test proves that
a list was iterated and not that the sections exist. **S5 correction:** it lives
in `package eventtest_test` with every other test, and reaches the unexported
runner through `event/eventtest/export_test.go`, the standard-library idiom. The
plan had put it in `package eventtest` for that access, which does not work: the
fixture stores are in `package eventtest_test`, where §INV-019's compile-time
proof needs them, and the two packages cannot see each other's identifiers even
though both compile into one test binary.

### Type inference — where it works and the two places it stops

| Written at the call site | Inferred | How |
|---|---|---|
| `event.Define[Account]("accounts.account", func(id AccountID) event.Key { … })` | `ID = AccountID` | a partial type-argument list with the explicit parameter first |
| `event.From(event.JSON[Opened]())` | `V = Opened` | the codec's own type argument |
| `event.Then(prev, event.JSON[Credited](), toCreditedV2)` | `A` from `prev`; `B` from the codec **and** the upcaster's result | two independent sources that must agree, so a mismatched upcaster is a compile error |
| `event.Declare(accounts, "accounts.credited", chain, fold)` | `S`, `ID` from the aggregate; `E` from the chain | the fold's `func(S, E) S` must match all three |
| `event.Bind(binding, accounts)` | `S`, `ID` | from `*Aggregate[S, ID]` |
| `credited.New(id, payload)`, `repo.Load/Append`, `accounts.Fold(...)` | nothing | methods on fully instantiated types |

**It stops twice, admitted rather than hidden.** (1) There is no fluent chain —
Go has no method-level type parameters, so a revision is added with
`Then(From(a), b, up)` and a long chain reads outward-in. (2) `RoundTrip` takes
`byRevision ...any` positionally, because the historical reader types are a
heterogeneous list and Go has no variadic type parameters; each element's dynamic
type is checked against the declared reader type for that position, failing with
the revision and the two type names.

---

## Sections

Statuses: `[ ]` not started · `[~]` partial (must carry `MISSING:`) · `[x]` done,
checkpoint executed · `[!]` blocked (must carry `BLOCKED BY:`).

Ordering rule: **no section leaves the tree red**; `go build ./...` passes after
every one. Each is independently completable, and each is tested against a *real*
implementation rather than a mock — which is why `eventmemory` (S3) precedes the
caller seam (S4).

**Every checkpoint is split, and every named test is counted before it is run.**
Two rules, and they exist because a checkpoint that cannot fail is worse than no
checkpoint:

- **A `-run` pattern that matches nothing is a pass.** Measured in this tree:
  `go test -count=1 -run '^(TestThisNameDoesNotExistAnywhere)$' ./errs/` prints
  `ok … [no tests to run]` and exits **0**. Nothing in this repository greps
  `.agents/artifacts/` (`scripts/docs_test.go:49` reads `docs/` only, verified),
  so a misspelt or later-renamed name is green forever. Every clause that names
  tests is therefore preceded by
  `test "$(go test -list '<the same pattern>' ./pkg | grep -c '^Test')" = <N>`,
  which is non-zero when a name is missing. `go test -list` prints one line per
  matching test and then its own `ok` line, so `grep -c '^Test'` is the count
  (measured). The patterns are written `'^(A|B|C)$'` and not `'A|B|C'`, because
  `-run` and `-list` match **unanchored**: `TestClose` would also match
  `TestCloseIsIdempotent` and inflate the count past the assertion, which is the
  same vacuity arriving through the other door. Both forms measured in this tree.
  **And the clause stops at the top level:** `go test -list` prints `Test…`
  functions and never a `t.Run` subtest, so it cannot count the twenty
  conformance sections — every one of which is a subtest, and seven of the ten
  carried gaps are proved in one. That gap is closed where it lives, by an
  inventory in `eventtest` and a top-level test over it
  (`### event/eventtest/`), not by a longer regex.
- **Phase 4 implements a section and phase 5 writes its tests**, so at the moment
  a section's implementation is finished its named tests do not exist. Each
  checkpoint below is therefore two blocks. A section is `[~]` with
  `MISSING: <section> tests` when its **phase-4** block passes, and `[x]` only
  when its **phase-5** block passes. `[x]` claimed on a phase-4 block is the lie
  the ECONV skill names.

---

### S1 — the vocabulary, the refusal partition and the store contract  `[x]`

**Both blocks are executed and green.** The phase-5 suite is ten tests and one
fuzz target in eight files — `event/refusal_test.go`, `walk_test.go`,
`outcome_test.go`, `rendering_test.go`, `store_test.go`, `backing_test.go`,
`concurrency_test.go`, `fuzz_test.go` — carrying the eight names this section
planned, plus `TestANilOrLyingCauseNeverPanicsTheKernel` (GAP-162's own close
criterion), `TestTheKernelsValuesAnswerTheSameFromManyGoroutines` (§INV-038's
S1 rows, under `-race`) and
`FuzzComposeRendersAKeyThatIsLegalAndReversible` (§INV-033's injectivity over an
unbounded domain rather than a table).

**Delivers** every inert value on both seams, the twenty-four sentinels and their
partition, the `Outcome` channel and its total map, and the `Store`/`Log`
interfaces. Nothing has behaviour yet except the values themselves. This is the
section the three blocking carried gaps land in.

**Files** `event/doc.go`, `identity.go`, `bounds.go`, `text.go`, `backing.go`,
`authority.go`, `outcome.go`, `errors.go`, `store.go`.

**Realises** the identity vocabulary, `Backing`, `Authority`, `Outcome`/`Failure`,
the sentinel set and map, `Support`/`Capabilities`/`Limits`,
`Record`/`AppendRequest`/`Envelope`, `Log`, `Store`.

**Carried gaps** C2 (the `Refused` outcome and `ErrRefused`), C3 (the context
cause is unreachable — widened by round 4's **GAP-135** to the whole cause, with
`CauseOf` as its named reader), C5 (`ErrCursor` → wiring; `ErrTooLarge` loses the
load path; **the wrapper's `Is`/`As` split**, without which the declared class
wrap is unreadable and an oversized payload renders 500, and without whose `Is`
half a store's cause renders an uncertain commit 503), C6 (the scoped inbound clause),
C9 (the `Codec` and mapper concurrency clause is written in S2; the ownership
comments on `Record`/`Envelope` are here), C10.3 and C10.5 (documented on the
contract).

**Round 5's five closures** (GAPS-147 to GAPS-151, all `[immediate]`): §INV-024's
partition holds through the two rows whose wrap is a cause (the mechanism was
replaced in round 6 — see below — and the property is the same); a cancellation
travels as the **bare** sentinel and a `Failure` renders no cause text, so a
store's message — which names the key and the version — reaches neither the
caller nor a log line; the walk's hop budget bounds **visits rather than depth**,
so a branching `errors.Join` chain cannot hang the request goroutine;
`Stream.String` refuses to render a family that failed the kernel's text rule;
and `AppendRequest` carries §D.14's three clauses, of which `Expected: 0` means
*never appended to* and never *any*.

**Round 6's five closures** (GAP-157 to GAP-161): every member of the **request
class** declares `crud.ErrBadRequest` on its own sentinel, so an omitted id
renders 400 and not the operator page §UC-051 chose the class to prevent
(GAP-157); the kernel performs **three** traversals over a foreign chain and no
stdlib `errors.Is` or `errors.As`, so `refuse` — the first statement on every
store error — can no longer hang on a cyclic `Unwrap` nor cost 555 ms on a
branching one, and a cause is promoted to a wrap only when the kernel could read
it to the end, which is what makes `Is` and `As` agree past the budget
(GAP-158); the partition is enforced on the **target** of the traversal rather
than by dropping the cause, so a decorator's or an upcaster's own error stays
matchable when it co-carries one of the twenty-four (GAP-159); `findAs` answers
"does this chain carry a `T`" the way `errs.AsFault` does, so a retryable
failure behind an `As`-only error renders 503 and not 500 (GAP-160); and the
`Outcome` test is named once per section rather than twice under one name
(GAP-161).

**Covers** UC-060 (the classification half), UC-034 (the map), UC-048 (`Refused`);
INV-001, INV-011 (the message table), INV-012, INV-016, INV-018 (the contract),
INV-021 (the clauses), INV-024, INV-025, INV-026, INV-027 (the seam carries no
transaction control — the shape half), INV-028, INV-029, INV-030,
INV-033 (`Compose`'s legality and injectivity, which is the half that needs no
store), INV-038 (the table, and the inert rows under `-race`), INV-045.

**Degradation** none — nothing in this section performs I/O or calls an
application callback.

**Checkpoint S1 — phase 4 (implementation)**

```
go build ./... && go vet ./event/ && go test -race -count=1 ./event/ && test -z "$(gofmt -l event)" && test "$(go list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' ./event/ | sort | paste -sd, -)" = "github.com/frostgrove/vv/crud,github.com/frostgrove/vv/errs,github.com/frostgrove/vv/event,github.com/frostgrove/vv/utils"
```

The last clause is this block's real evidence, and it proves the import graph and
nothing else. **It compares rather than prints** (GAP-144): `go list -deps` exits
0 for any graph that builds, so a printed list is a clause that cannot fail, and
an `event` that imported `port`, `jobs` or `tenancy` in S1 would be caught five
sections later by S6's own test. The four it must equal are —
`github.com/frostgrove/vv/utils`, `…/crud`, `…/errs`, `…/event` — and nothing
else. `utils` is inherited: `go list -deps ./crud` prints `utils` and `crud`,
measured at `72e7d22`. That is **three** first-party packages outside itself,
against `./jobs`'s five (`utils`, `crud`, `crud/query`, `errs`, `port`) and
`./tenancy`'s two — the subsystem `event` is modelled on already pays more for
less, which is D-121's measured argument rather than an assumed one.

**Checkpoint S1 — phase 5 (tests)**

```
test "$(go test -list '^(TestTheRefusalVocabularyIsAPartition|TestTheStoreSeamHasEightMethodsAndNoneMutatesOrQueries|TestTheSeamsValuesAreDefinedTypesWithTheDeclaredFields|TestABackingAndAnAuthorityAreComparedAndNeverIdentical|TestAContextCauseNeverTravelsThroughARefusal|TestADeclaredWrapIsReachableByErrorsAsAndACauseIsNot|TestAnOutcomeOutsideTheVocabularyNormalises|TestEveryRenderingNamesAClassAndNeverAValue|TestAJoinedCauseCannotOutlastTheWalksBudget|TestANilOrLyingCauseNeverPanicsTheKernel|TestTheKernelsValuesAnswerTheSameFromManyGoroutines|TestComposeRendersTheFrozenKey|FuzzComposeRendersAKeyThatIsLegalAndReversible)$' ./event/ | grep -cE '^(Test|Fuzz)')" = 13 && go test -race -count=1 -v -run '^(TestTheRefusalVocabularyIsAPartition|TestTheStoreSeamHasEightMethodsAndNoneMutatesOrQueries|TestTheSeamsValuesAreDefinedTypesWithTheDeclaredFields|TestABackingAndAnAuthorityAreComparedAndNeverIdentical|TestAContextCauseNeverTravelsThroughARefusal|TestADeclaredWrapIsReachableByErrorsAsAndACauseIsNot|TestAnOutcomeOutsideTheVocabularyNormalises|TestEveryRenderingNamesAClassAndNeverAValue|TestAJoinedCauseCannotOutlastTheWalksBudget|TestANilOrLyingCauseNeverPanicsTheKernel|TestTheKernelsValuesAnswerTheSameFromManyGoroutines|TestComposeRendersTheFrozenKey|FuzzComposeRendersAKeyThatIsLegalAndReversible)$' ./event/ && go test -race -count=1 ./event/ && go vet ./event/... && test -z "$(gofmt -l .)"
```

**The count clause reads `grep -cE '^(Test|Fuzz)'` and expects thirteen** because
`go test -list` prints a matching fuzz target beside the tests (measured), and
the three names phase 5 added and the two round 7 added are counted like the
eight it was given. The thirteen run in **1.0 s** under `-race`.

Those eight are the invariants S1's `Covers` claims that a test can reach without
a store: §INV-024's pairwise ground-truth table over all twenty-four sentinels,
§INV-016's and §INV-028's `Equal`/`Same`-never-`==` cases, C3's context-cause
table, C5's traversal table — every declared wrap found by `errors.As`, every
store cause *not* found by it **nor by `errors.Is`** (GAP-135), `CauseOf` finding
each of those causes, and `ErrRefused` and `ErrUpcast` as the two rows whose
cause is their wrap and so answer both — for every target except one of the
twenty-four, which `sentinel` alone answers (GAP-147) —
§GAP-118's normalisation, and §INV-025 over every `String()` in the section.
§INV-018, §INV-021, §INV-027, §INV-030, §INV-045 and §INV-011 are **contract
clauses here and are proved in S4, S5 and S6**, which the coverage matrix's
checkpoint column already says.

**What round 5 added to four of them, and the eighth test it added.** Each is a
finding that was reproduced against this code before it was fixed, so each is a
test that fails without its fix:

- `TestTheRefusalVocabularyIsAPartition` runs its 24 × 23 table **over refusals
  built from a store's cause** — `Failure(<each outcome>, <each of the 24>)`
  through both doors — and not only over the bare sentinels. That is the table
  §INV-024's *Falsified by* asks for, and it is the one that was green on a
  vocabulary that was not a partition (GAP-147). `Failure(Refused,
  crud.ErrForbidden)` and a `Failure(Refused, *errs.Fault)` are its controls: both
  must still arrive, or the fix has closed the mechanism C2 exists for.
- `TestAContextCauseNeverTravelsThroughARefusal` drives a store that returns
  `fmt.Errorf("read %s at v%d: %w", key, after, ctx.Err())` and asserts the
  returned error's `Error()` carries neither the key nor the version, with the
  bare sentinel's `errors.Is` as its control (GAP-148).
- `TestEveryRenderingNamesAClassAndNeverAValue` covers
  `Stream{Family: <newline>}.String()`, `Stream{Family: <MaxNameBytes+1>}.String()`
  and `Failure(NotWritten, errors.New("pq: password authentication failed …"))`
  `.Error()`, each asserting the value does not appear, with a legal family and a
  legal outcome as controls (GAP-148, GAP-150).
- `TestABackingAndAnAuthorityAreComparedAndNeverIdentical` includes an
  **uncomparable** identity for both constructors — a slice, and a struct
  containing one — with a comparable identity as its control (GAP-153).
- `TestAJoinedCauseCannotOutlastTheWalksBudget` builds a balanced
  `Unwrap() []error` tree deeper than the budget and asserts the walk **visits at
  most `causeHops` nodes**, counted rather than timed, with the left-deep
  `errors.Join` chain as its control (GAP-149). A timing assertion is what would
  make this test flaky; the visit count is the quantity the fix changed.

**What round 6 added to three of them.** Same rule: each was reproduced against
this code before it was fixed.

- `TestAJoinedCauseCannotOutlastTheWalksBudget` covers **every door the kernel
  reads a foreign chain through**, not `matches` alone — `refuseAppend`,
  `refuseRead`, `CauseOf`, `retryable` and a rendering through `errs.AsFault` —
  and adds a **cyclic** `Unwrap() error` chain, asserting each returns at all.
  The left-deep chain stays the control. Before the fix `refuseAppend` over the
  balanced tree cost 8.66 ms / 136 ms / 555 ms at depths 18 / 22 / 24 while the
  kernel's own `matches` cost 1.9 µs, and the cyclic chain never returned
  (GAP-158).
- `TestTheRefusalVocabularyIsAPartition` gains the **co-wrap** pair in both
  directions (GAP-159): `upcastRefusal(fmt.Errorf("%w: %w", errApp, ErrRevision))`
  and `Failure(Refused, fmt.Errorf("%w (quota %w)", errApp, ErrTooLarge))` each
  assert `errors.Is(err, errApp)` is **true** and the vocabulary sentinel is
  **false**, with `errApp` alone as the control — the loss the first mechanism
  caused was total and silent, so a control that only shows the sentinel gone
  proves half the property.
- `TestADeclaredWrapIsReachableByErrorsAsAndACauseIsNot` gains the request
  class's own wraps — `errors.Is(ErrKey, crud.ErrBadRequest)` and the same for
  `ErrEncode` and `ErrSample`, with a wiring-class sentinel as the control that
  answers false (GAP-157) — and a cause whose `*errs.Fault{KindRetryable}` is
  reachable **only through an `As` method**, asserting `retryable` finds it, with
  `fmt.Errorf("%w", fault)` as its control (GAP-160).

**Checkpoint S1 — phase 4, re-executed 2026-09-07 after round 6's closures**

```
$ go build ./... && go vet ./event/ && go test -race -count=1 ./event/ && test -z "$(gofmt -l event)" && test "$(go list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' ./event/ | sort | paste -sd, -)" = "github.com/frostgrove/vv/crud,github.com/frostgrove/vv/errs,github.com/frostgrove/vv/event,github.com/frostgrove/vv/utils" && echo "import graph: exactly crud, errs, event, utils"
?   	github.com/frostgrove/vv/event	[no test files]
import graph: exactly crud, errs, event, utils
EXIT=0

$ go list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' ./event/
github.com/frostgrove/vv/utils
github.com/frostgrove/vv/crud
github.com/frostgrove/vv/errs
github.com/frostgrove/vv/event

$ gofmt -l .          # silent, EXIT=0
$ go build ./...      # EXIT=0
$ go vet ./event/...  # EXIT=0
$ go test -race ./event/...
?   	github.com/frostgrove/vv/event	[no test files]
EXIT=0

$ make check          # check-deps, check-tiers, check-utils, check-triplets,
                      # check-todo, check-replaces, check-tidy,
                      # check-otel-schema, check-workspace — all ok
$ make unit           # every module, -race — EXIT=0, zero FAIL lines
```

`[no test files]` is the phase-4/phase-5 split working as written, not a hole:
S1's eight named tests are phase 5's deliverable and the section stays `[~]`
until its phase-5 block passes. The section is **839 lines** across nine files
after round 6, the longest `event/errors.go` at **354** against a 400 threshold.

**What was verified while the code was written, and then removed.** A throwaway
`event/zz_probe_test.go` drove the whole section — the twenty-four-sentinel
pairwise partition, the `Outcome` map at both doors including an out-of-range
outcome and a `%w`-wrapped `Failure`, both cancellation windows, `CauseOf`,
`Compose`'s legality and injectivity, the six nil routes of `NewBacking` and
`NewAuthority`, and `porthttp.Status` over five refusals (413 for an oversized
payload, 409 for a conflict, 403 for a policy refusal wrapping
`crud.ErrForbidden`, 503 for a retryable `ErrBackend`, **500 for an `ErrUncertain`
whose cause carries `crud.ErrUnavailable`** — GAP-135's own assertion). It was
made to fail twice on purpose — `Is` widened to reach the cause, and `Compose`'s
`/` escape removed — and both went red at the expected line before the code was
restored.

**Round 5 repeated that, once per closure, and every one of the five was made to
fail first.** A second throwaway probe held the assertions the four amended tests
and the new eighth are described as carrying; each fix was reverted in place, the
probe run, and the code restored:

```
Refused/Upcast promote any cause      → "a [outcome refused] refusal over cause
                                         event: the declaration is malformed also
                                         matches event: the declaration is malformed"
                                         (and 40 more rows)          GAP-147
cancellation returned verbatim        → "a cancelled read rendered
                                         \"read orders/A-17 at v9: context deadline
                                         exceeded\""                  GAP-148
failure.Error() concatenates a cause  → "event: the store reported [outcome not
                                         written]: pq: password authentication
                                         failed for user \"admin\""   GAP-148
budget passed by value per branch     → "a branching chain cost 524287 visits
                                         against a budget of 64"      GAP-149
Stream.String renders any family      → "a forged family rendered \"[stream
                                         orders\\n\\tFAKE LOG LINE: admin logged
                                         in]\"" and "an oversized family rendered
                                         138 bytes"                   GAP-150
```

**Round 6 did the same for its four code closures**, in one throwaway
`event/zz_probe_test.go` run against the unfixed code first and against the
fixed code after. Measured before:

```
request-class sentinels               → ErrKey / ErrEncode / ErrSample / bare
                                         ErrTooLarge all kind=internal
                                         status=500; only tooLarge()'s fault
                                         reached 413                  GAP-157
refuse / CauseOf over a cyclic chain  → "did not return within 2s", both
                                         refuseAppend(balanced depth-22 join
                                         tree) = 813 ms               GAP-158
co-wrapped application error          → upcastRefusal(errApp + ErrRevision):
                                         Is(errApp)=false, control
                                         Is(errApp)=true              GAP-159
an As-only *errs.Fault{KindRetryable} → kernel faultIn=false while
                                         errs.AsFault=true; a NotWritten over
                                         it rendered 500              GAP-160
```

and after: 400 for the three sentinels with 413 and 409 unchanged; the cyclic
chain returns and the depth-22 tree costs 25.6 µs; `Is(errApp)=true` with
`Is(ErrRevision)=false` and `Is(event.ErrConflict)=false` on a quota refusal
whose cause wraps it; `retryable` true and 503. The partition was re-run whole —
7 outcomes × 24 causes × 2 doors = **336 refusals**, each probed against all 24
sentinels, **336 matches in total**, so exactly one each and no cross-class edge —
with `Failure(Refused, crud.ErrForbidden)` → 403 and `Failure(Refused, *errs.Fault)`
→ `errs.AsFault` kind `forbidden` as the controls that C2's mechanism is intact.
Round 5's closures were re-asserted in the same probe and all five hold.

All three probes are **deleted**, because the tests of record are phase 5's and carry
the plan's own names; the assertions above are what those eight must contain.

**Checkpoint S1 — phase 5, executed 2026-09-07**

```
$ go test -list '^(…the thirteen names…)$' ./event/ | grep -cE '^(Test|Fuzz)'
13
$ go test -race -count=1 ./event/         ok  github.com/frostgrove/vv/event  1.032s
$ go vet ./event/...                      EXIT=0
$ gofmt -l .                              silent
$ go build ./...                          EXIT=0
$ make check                              nine arms, all ok
$ make unit                               EXIT=0, zero FAIL lines
$ go test -run '^$' -fuzz FuzzComposeRendersAKeyThatIsLegalAndReversible -fuzztime 45s ./event/
  7 032 528 executions, 84 corpus entries, PASS
```

**What phase 5 changed in the code, and why it is not scope creep.** GAP-162 was
`[high][immediate]` and **open**: `findAs` answered `(nil, true)` on two routes,
and its three callers dereference at once, so an unpopulated `*errs.Fault` a
store wrapped panicked the kernel on the read door's fail-safe path with the
caller's transaction open. Reproduced first — four panics, each
`invalid memory address or nil pointer dereference`, from `retryable`,
`refuseAppend`, `refuseRead` and `CauseOf` — then closed by one guard in
`findAs` (a located `T` that is nil by any route is not a find), with the walk's
comment stating what the kernel now does with a matcher that answers for a value
it never set. `TestANilOrLyingCauseNeverPanicsTheKernel` is the close criterion
and the count clause moved from 8 to 11.

**What phase 5 added beyond the eight.** GAP-163 asked for a check that the
partition gate's list **is** the declared set rather than a hand-maintained copy;
it lives in `TestTheRefusalVocabularyIsAPartition` as a `go/ast` walk of
`event/errors.go` that compares the exported `Err*` declarations, the identifiers
inside `vocabulary()` and this test's own table as three multisets, so an
omission cannot make the 24 × 23 table pass over a shrunken vocabulary.
`TestTheKernelsValuesAnswerTheSameFromManyGoroutines` drives every inert value
and every refusal-building route from eight goroutines under `-race`, which is
§INV-038's S1 rows and §INV-013's runtime half.
`FuzzComposeRendersAKeyThatIsLegalAndReversible` proves the two properties a
table cannot: every composed key passes the kernel's own text rule, and
`decompose(Compose(a, b, c))` is `[a b c]` byte for byte — injectivity by
reversibility rather than by a pairwise sample.

**Every one of the eleven was made to fail on purpose**, one broken behaviour at
a time, and the twenty-one mutations and the message each produced are in the
section report. Three of them are worth keeping here because they are the
defects the section's own audit rounds were spent on: the hop budget spent per
path costs **262 143 visits against 64** and `TestAJoinedCauseCannotOutlastTheWalksBudget`
names the number; `refusal.Is` reaching the cause reopens GAP-147 and the
pairwise table names the crossing pair; and the `findAs` guard removed panics
`TestANilOrLyingCauseNeverPanicsTheKernel` rather than failing it.

**GAP-167's second criterion, re-run:** after phase 5 the only unexported
function under `event/` with no caller is `Support.stated`, which is S4's
(`Bind` and `Read` read it for §INV-043). `tooLarge`, `upcastRefusal`,
`refuseTransaction`, `refuseAppend` and `refuseRead` — the five the finding
named — are all exercised, so the phase-4 block's `[no test files]` clause is
closed by behaviour rather than by compilation.

**Round 7's seven closures — the test review's `[immediate]` findings, and no
code changed.** Every one is a property the code already implemented and no
assertion reached; each was proved by re-applying the mutation the review's
campaign had recorded as surviving, and each turns the suite red.

- **The frozen key rendering** (GAP-T1). `TestComposeRendersTheFrozenKey` is a
  golden table of twelve renderings written as literals — `acme/A-17`,
  `acme%2Fevil/A-17`, `acme/evil%2FA-17`, `рога/17`, `a%FFb`, `a%C2%85b` — and
  not built from `composeSeparator` or `composeDigits`, which is what made the
  fuzz target's `decompose` self-consistent under any change to the rendering.
  A second subtest pins the one pair that renders equal, `Compose()` and
  `Compose("")`, and that both are refused as empty. M69 (`'/'` → `':'`), M68
  (lower-case hex) and M87 (escape every byte) each go red.
- **The custom-`Is` half of the bounded traversal** (GAP-T2). `answersByIs`
  beside `asOnlyFault`: a driver error whose `Is` answers the retryable class and
  whose `Unwrap` is nil reaches `crud.ErrUnavailable` through a `NotWritten`
  refusal, with a cause that answers for another class as the control; and a
  cancellation reached only through `Is` still travels as the bare sentinel while
  a classified one does not — §UC-057's two halves. M103 goes red on both.
- **The two untested panic guards** (GAP-T3). `panickingMatcher` panics from
  both `Is` and `As`, and `errs.AsFault` over a policy refusal built on it must
  answer false rather than panic — M27 red. `rows` is an uncomparable error type,
  so `node == target` panics inside `sameError` when a caller matches their own
  uncomparable error against a refusal whose chain carries one; the assertion is
  that the walk **continues past it** and still finds `answersForRows` behind it,
  with that matcher alone as the control — M29 red, because `walk`'s own recover
  aborts the whole traversal where `sameError`'s recovers one node.
- **§INV-013's runtime half** (GAP-T4). `TestTheKernelsValuesAnswerTheSameFromManyGoroutines`
  no longer computes its expectation in the test goroutine: eight readers are
  held at a barrier, each computes its own first answer and compares its own 200
  repeats to it, and the eight are compared pairwise afterwards. M107 — a lazily
  populated unsynchronised package-level cache in `inVocabulary`, the shape a
  real optimisation takes — now goes red under `-race`; before, only the
  write-on-every-call form did.
- **The positive half of the message table** (GAP-T5). Every refusal is asserted
  to render its own sentinel's message, the twenty-four sentinels to render
  twenty-four distinct messages, the three capability answers to render three,
  a stated backing to differ from one no store stated, and the four rendering
  kinds — backing, authority, capability answer, classification — to share no
  phrase. M76, M78, M79, M80, M57 and M31 each go red.
- **The seam's value contract** (GAP-T6).
  `TestTheSeamsValuesAreDefinedTypesWithTheDeclaredFields` asserts the declared
  field set and field type of `Stream`, `Capabilities`, `Limits`, `Record`,
  `AppendRequest` and `Envelope`, and that `Key`, `Cursor`, `Version` and
  `Position` are defined types rather than aliases and count upward. M47, M48,
  M49, M54, M83, M84, M85 and M86 each go red — the two aliases and the two
  signed types would otherwise have compiled forever.
- **`refuse(nil)`** (GAP-T9). Both doors are asserted to refuse nothing when they
  were handed nothing, which is the guard on the happy path of every successful
  append and read. M30 red.

---

### S2 — the declaration seam: codec, chain, aggregate, fact, change, fold  `[x]`

**Both blocks are executed and green.** The phase-5 suite is **twelve** tests and
**two** fuzz targets in seven files — `event/declaration_test.go`,
`seal_test.go`, `fold_test.go`, `roundtrip_test.go`, `upcast_test.go`,
`crossings_test.go`, and the two fuzz targets in S1's `fuzz_test.go`, which is
where this package's fuzzing lives.

**Delivers** everything an application declares and everything it can do with **no
store at all** — declare, mint a change, fold, round-trip. The kernel runs with
zero extensions registered, which is `microkernel.md`'s own requirement and
§UC-041's use case.

**Files** `event/codec.go`, `encodable.go`, `chain.go`, `aggregate.go`, `fact.go`,
`change.go`, `seal.go`; `event/testdata/crossings/` (build-failure fixtures, three
packages: `control`, `change`, `identity`). `encodable.go` is round 2's split of
`codec.go` and holds the walk — *what a payload may be* — with no exported symbol
of its own.

**Realises** `Codec[V]`, `JSON`, `Chain`/`From`/`Then`, `Aggregate`/`Define`/
`TryDefine`/`Family`/**`Key`**/`Fold`, `Declaration`, `Fact`/`Declare`/
`TryDeclare`/`New`/`Name`/`Revisions`/`RoundTrip`, `Change`.

**Carried gaps** C1 (the `Codec` clause, the `RoundTrip` proxy, the zero-value
second payload and `ErrSample`), C9 (the codec and mapper clauses where an
implementer reads them, and the panic policy for all three codec methods),
C10.1 (`seal()` and its **five** enumerated readers — `Bind` takes the sixth row
in S4, and the seal test now parses the enumeration out of `event/seal.go` and
compares it to the AST call sites, so a row with no reader fails as loudly as a
reader with no row), C10.2 (the guarded `Ledger` fold in the example).

**Decides** GAP-41: the kernel does not refuse a non-struct payload; the codec
answers. §UC-004's trigger list loses that row.

**Covers** UC-001, UC-002 (the compile half), UC-003, UC-004, UC-005, UC-012 (the
chain), UC-014, UC-015, UC-016, UC-021 (the `Fold` half), UC-024 (`New`), UC-026,
UC-041, UC-042 (`Fact.RoundTrip`), UC-050, UC-051 (the `New`/`Fold` doors),
UC-052, UC-056, UC-063, UC-064 (the `Fold` half), UC-065, UC-067 (the fold's
panic is not recovered), UC-068; INV-004, INV-005, INV-010, INV-017, INV-020 (the
compile half), INV-021 (hand-offs 1, 2, 8), INV-023, INV-033, INV-042, INV-044
(the `Fold` half).

**Degradation** an upcaster that panics is recovered into `ErrUpcast` wrapping
nothing; a fold that panics is **not** recovered and unwinds to the caller; a
codec that cannot encode its own reader type panics at declaration.

**What S2 changed in the contract, and why each was a defect rather than a
preference.** Each is written into the contract section it belongs to:

- **`Aggregate.Key(id ID) (Key, error)` is added, and `Aggregate.Family` seals.**
  C10.1 names `eventtest.Keys` and `eventtest.Families` as two of the six sealing
  readers, and an unexported `seal()` **cannot be called from another package** —
  `d.seal()` in `package eventtest` does not compile. So the two helpers seal
  through the two exported methods they were always going to call, and `Keys` had
  no exported way to obtain a key at all. See `#### event/aggregate.go` and C10.1.
- **`Fact.RoundTrip`'s four clauses** — the second decode is the revision's own
  `selfDecode`, the aliasing verdict is `ErrPayload`, every codec output the
  algorithm keeps is cloned, and the whole comparison stays in the revision's own
  type. See `#### event/fact.go`. The third is the one the mutation campaign
  found: a codec that reuses its **encode** buffer — which §INV-021 hand-off 1
  permits in terms — defeats the whole proxy without it. The fourth came out of
  the S2 implementation review (GAP-172) and changed `link.selfDecode` to return
  the decoded value.

**What round 1 of the S2 implementation review changed, closed here.** Each is
written into the contract section it belongs to and each was verified by
mutation — the arm was removed, the named test went red, the arm was restored:

- **GAP-170 `[critical]` — `event.JSON` admitted payload types that encode to
  `{}`.** `Encode` now marshals `&value`, and the walk carries addressability,
  refuses a struct with fields and no field `encoding/json` writes, and refuses
  two fields of one struct rendering one JSON name. See the corrected walk under
  `#### event/codec.go`. `TestADeclaration` gains three subtests: the refused and
  accepted tables grow by six and five rows each with a control beside every
  refusal; *what the shipped codec writes is frozen* is a seven-row golden table
  proving `&value` changed no wire format for an integer, a nil slice, a map,
  `json.RawMessage`, a value-receiver marshaller and a slice element, and changed
  exactly the broken one; *what the shipped codec accepts, it records with its
  contents* drives a pointer-receiver payload through `Declare` → `New` → `Fold`
  and fails with `[""]` on the old `Encode`.
- **GAP-171 `[high]` — the walk asked only whether a type writes itself.** It now
  asks for the matching unmarshaller, on the type and on a map key.
- **GAP-172 `[high]` — the non-aliasing proxy was vacuous for every revision but
  the current one.** `TestACodecThatDecodesIntoAReusedBufferIsCaught` gains *a
  retained revision behind a copying upcaster is under test too*, which drives
  the suite's own `scratchCodec` as revision 1 of a two-revision chain and
  answers `<nil>` on the old spelling; the same chain over `JSON` is its control.
- **GAP-173 `[medium]` — a change whose mapper failed carried a zero stream**, so
  `Fold` reported a request-class `ErrKey` as a wiring-class `ErrWrongStream`.
  `TestFoldRefusesAnotherInstance` gains *a change minted for an identity that
  renders no key answers as a key refusal*, with the existing blank-identity case
  as its control, and the crossing subtest now asserts the refusal names the wire
  type rather than being the section's one bare sentinel.
- **GAP-174 `[low]` — the seal enumeration named `Bind`, which does not exist.**
  The row leaves until S4 adds it, and `TestTheSealRefusesALateFact` now parses
  the enumeration out of `event/seal.go`'s own comment, so the artefact a reader
  of the library sees is under the same test as the code.
- **GAP-175, GAP-176 and GAP-177 were graded `[deferred]`** and are carried in
  `## Debt` with the argument for each.
- **The phase-5 name list moves by one in each direction.**
  `TestComposeRendersALegalKeyAndNeverCollides` is **dropped**: S1 shipped
  `TestComposeRendersTheFrozenKey` (a twelve-row golden table, including the
  separator, escape, NUL, invalid-UTF-8 and multi-byte parts this test named) and
  `FuzzComposeRendersAKeyThatIsLegalAndReversible` (legality and injectivity by
  reversibility over an unbounded domain), so a third Compose test would assert a
  strict subset. `TestAnUpcasterMayRefuseAndAFoldMayNot` is **added**, because
  S2's own **Degradation** paragraph states the upcaster/fold panic asymmetry
  (§INV-017, §UC-067) and nothing in the given list reached it. Count stays nine
  through phase 4; phase 5 takes it to twelve, and the three it adds are listed
  with the mutation each one kills below.
- **§UC-050's control reads "the helper length-prefixes"**, which the plan's own
  `Compose` (escaping, `#### event/identity.go`) superseded. Under escaping the
  two legal spellings of a **single-part** identity are byte-identical unless the
  part carries `/`, `%`, a control byte or invalid UTF-8, so the test pins the
  pair on an identity containing a separator. §INV-005's freeze is unaffected and
  the trap is the same one. S6's [SPEC] edit list carries the wording.

**What round 2 of the S2 implementation review changed, closed here.** The four
`[immediate]` findings are repaired in the code and written into the contract
section each belongs to; each was verified by mutation — the arm was removed, the
named case went red, the arm was restored. Round 2's own summary is that round 1
repaired *the close criterion's wording and the fixture beside it* rather than the
rule, so each repair here is written against the rule and the fixture that proves
it is the ordinary shape rather than the exotic one:

- **GAP-178 `[critical]` — the walk could not see promoted fields**, so a struct
  embedding two structs that each carry an `ID` encoded as `{}`, and one whose
  embedded name is shadowed from a shallower depth lost that field, both with a
  `nil` at every door. The walk now computes the names a struct renders the way
  `encoding/json` does and refuses a name claimed twice by any route
  (`#### event/codec.go`, refusal 5), and `Fact.RoundTrip` compares what came
  back with the sample in the revision's own type (`#### event/fact.go`, clause
  6). `TestADeclaration` gains three refused rows with three accepting controls
  and two diagnosis rows; `TestACodecThatDecodesIntoAReusedBufferIsCaught` gains
  *a codec that records less than it was handed is caught*.
- **GAP-179 `[critical]` — a map key of string or integer kind never reached the
  route check** GAP-171 installed one arm below, because `objectKey` answered
  `nil` for those kinds first. `encoding/json` asks `MarshalText` and
  `UnmarshalText` **before** the kinds, so `type Currency string` with a display
  marshaller wrote `cur:usd` into the log and read it back as a legal currency
  code, and `type Kind int` with one wrote a key no `Load` can ever parse. The
  question is now asked first and in both directions
  (`#### event/codec.go`, refusal 3). Three refused rows, three accepting
  controls, two diagnosis rows.
- **GAP-180 `[high]` — the non-aliasing proxy passed the ordinary reusable-buffer
  codec.** The verdict is now taken by decoding the same bytes twice from two
  separate buffers and comparing the answers for a slice or map they share
  (`#### event/fact.go`, clause 5). `roundtrip_test.go` gains a length-prefixed
  `prefixCodec` that reuses one buffer and never clears it, with an
  `allocatingCodec` of the same wire format as its control; the case answers
  `<nil>` on the old spelling. Two controls sit beside the two new refusals — an
  allocating codec of the same wire format, and *what an encoding drops by design
  is not what it lost*, which drives a `time.Now()` payload and a populated
  unexported field through the fidelity comparison and would be red under
  `reflect.DeepEqual`.
- **GAP-181 `[medium]` — every decision took the aggregate's mutex to write a
  boolean that was already true.** `sealed` is an `atomic.Bool` with a lock-free
  fast path (`#### event/aggregate.go`). `TestTheSealRefusesALateFact` gains *a
  fact declared as the first reader runs is accepted or sealed and never both*,
  which runs two observers against one declaration 200 times and turns red under
  `-race` if `sealed` is a plain `bool`; three benchmarks sit beside it and the
  distinct-aggregate control is one of them, so the claim that the mutex is not a
  bottleneck cannot be made again without it.
- **GAP-182, GAP-183 and GAP-184 were graded `[deferred]`** and are carried in
  `## Debt` with the argument for each. GAP-184's first half — two comments naming
  an `eventtest` package no tree provides — is closed here anyway, in the spelling
  GAP-174 used for `Bind`: the two now name the conformance suite and say it has
  not arrived.
- **`event/codec.go` split.** The walk moved to `event/encodable.go`; nothing else
  changed and no exported symbol moved.

**What round 3 of the implementation review and round 1 of the test review
changed, closed here.** Round 3's own closing paragraph named the pattern: each
earlier repair was written against *the route the finding named* rather than the
rule the route was an instance of. The three `[critical]` findings it left open
are the same rule reached three more ways, and they are closed together, with the
walk's contract above rewritten from five refusals to eight. Every arm below was
verified by mutation — the arm was removed, the named row went red, the arm was
restored:

- **GAP-185 `[critical]` — a marshalling pair promoted from an embedded type was
  trusted as the struct's own**, so `struct{ Money; SKU string }` and
  `struct{ time.Time; Note string }` wrote themselves as the embedded value and
  dropped every field beside it, with `nil` at every door.
  `ownMethods` now settles a **struct** position only after `promotedMarshaller`
  has asked whether an anonymous field carries the route the struct writes by
  (`#### event/codec.go`, refusal 6). Measured before the repair and after:
  `CanEncode=<nil>, Encode=500, Decode={Money:{Cents:500} SKU:}` became a refusal
  naming the embedded type and the field it hides. The tagged spelling is refused
  too, and correctly — a `json:"money"` tag does not undo method promotion, which
  is why GAP-185's own suggested control was wrong and the accepting control is a
  **named** field.
- **GAP-186 `[critical]` — a read route through a method on the value receiver
  counted as a read route.** `readsAs` now answers true only for a method
  reachable through the pointer and **not** through the value, `objectKey`'s read
  probe asks the same question, and the case has a message of its own rather than
  *"declares no `UnmarshalJSON`"* (`#### event/codec.go`, refusal 7).
- **GAP-187 `[critical]` — an embedded pointer to an unexported struct type
  encoded cleanly and could never be decoded.** `members.collect` carries the
  blocking field down the promotion and refuses where a **name is rendered**
  through it (`#### event/codec.go`, refusal 8), which keeps the embedded value
  spelling and a type embedding a pointer to itself legal — both round-trip. The
  suite's `pointed` fixture moves from the accepted table to the refused one and
  is replaced there by `struct{ *Stream }`, an embedded pointer to an **exported**
  struct.
- **GAP-T20 `[critical]` (test review) — `event.JSON`'s decode-failure path was
  asserted by nothing**, so `jsonCodec.Decode` could swallow every
  `json.Unmarshal` error with the package, the seed corpus and a 3.9 M-execution
  fuzz campaign green. `TestFoldRefusesAnotherInstance` gains *recorded bytes the
  shipped codec cannot read are refused by the fold*, four rows through `Fold`
  plus the direct `Decode` assertion and its well-formed control; and
  `FuzzAStoredPayloadIsFoldedOrRefused` gains a positive arm in its setup — four
  payloads the declaration **must** refuse — so *folded or refused* is no longer
  satisfiable by *always folded*.
- **GAP-T21 `[critical]` — the declaration tables had no ordinary Go embedding.**
  They go from 26 refused and 23 accepted rows to **35 and 31**, and the
  diagnosis table from 11 rows to **17**: the three shapes above, the tagged
  spelling, the value-receiver reader as a reader type and as a field, the
  value-receiver map key, and an accepting control beside every one of them.
- **GAP-T22 `[high]` — the second arm of C1's non-aliasing proxy was deletable**,
  with both of the clones that live only inside it, because every case was
  decided by the pointer comparison. `roundtrip_test.go` gains `sealedCodec`,
  whose decoded value reaches its reused scratch buffer through an **unexported**
  field — the class `sharesMemory` skips by design, and what a zero-copy reader
  built on a private slice or a `string` is — with `sealingCodec`, the same wire
  format and the same reused encode buffer decoding into memory of its own, as
  its control. It also gains *a codec that consumes the buffer it was handed is
  handed one of its own*, over a reader that unescapes in place, which is the
  property the two payload clones exist for and which nothing stated before.
- **GAP-T23 `[high]` — `Family()` was pinned by one literal against the one
  example family**, so an implementation ignoring its receiver survived. The
  subtest is now a two-row table over two declarations, each asserting `Family()`
  and `Declaration.Family()` against the string that declaration was constructed
  from.
- **GAP-T24 `[high]` — the crossings test accepted any build failure as proof.**
  Each negative fixture now carries the substring its compiler error must contain,
  so a fixture that stops crossing while the build still fails is red. (Adding an
  unrelated undefined symbol *beside* an intact crossing stays green, and
  correctly: the compiler reports both errors and the fixture still proves what
  it claims.)
- **GAP-T25, GAP-T26, GAP-T27 and GAP-T29 `[medium]`/`[low]`, all `[immediate]`,
  are closed too.** `RoundTrip`'s five distinct refusals get a diagnosis table of
  their own, which is what stops a retained revision reporting the current
  reader type; a two-revision chain whose **current** revision carries
  `decimalCodec` pins the codec `Fact.New` writes with; `Fold`'s cause-1 subtests
  keep the state they were given and assert it came back untouched, one of them
  over a reference-kind state; a list holding both a crossing and a carried
  refusal is folded in both orders; and `Aggregate.Key` is asserted to answer the
  **empty** key beside its refusal.
- **GAP-T28, GAP-T30, GAP-T31, GAP-T32 and GAP-T33 were graded `[deferred]`** and
  are carried in `## Debt` with the argument for each.

**Checkpoint S2 — phase 4 (implementation), re-executed 2026-09-07 after round 2**

```
$ go build ./... && go vet ./event/ && test -z "$(gofmt -l event)" && go test -race -count=1 ./event/
ok  	github.com/frostgrove/vv/event	1.204s
EXIT=0

$ go list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' ./event/ | sort | paste -sd, -
github.com/frostgrove/vv/crud,github.com/frostgrove/vv/errs,github.com/frostgrove/vv/event,github.com/frostgrove/vv/utils
```

The import graph is unchanged from S1 — no new first-party or third-party
dependency, and `encoding/json`, `encoding`, `reflect`, `strings`, `sync` and
`sync/atomic` are the only standard-library imports S2 adds. The section is
**1021 lines** across seven files, the longest `event/encodable.go` at 290 —
`codec.go` reached 393 with GAP-178's and GAP-179's arms and was split, so the
largest file is back where a section's worth of further work fits under the
threshold. Comment density is 207/1021 = 20 %, and every comment is contract text
an implementer needs: the codec's three obligations and the panic policy for
every extension point, the question the walk asks and why encodability is not it,
why a map key's two text methods are asked about before its kind, what a name
claimed twice costs, the chain's revision-is-position rule and why the self
readers pass `any`, the mapper's injectivity and concurrency clause, `Fold`'s
consumption contract and its four causes, why a carried refusal outranks the
stream comparison, the `Change`'s retention rule, `RoundTrip`'s two properties
and what the second one cannot see, and the seal's enumeration and why it is
monotone. **No exported symbol was added, removed or changed by either round: the
surface is the same 22.**

The five longest functions are `Fact.roundTrip` at 37 lines, `sharesMemory` and
`sameValue` at 35 each, and `members.collect` and `jsonWalk.visit` at 33 each,
then `Aggregate.Fold` at 24. Maximum nesting depth is **4**, on `sharesMemory`'s
`switch → case Map → for → if`, and 3 or less everywhere else.
`sync/atomic` appears once, on the seal, and the replay path still reaches no
clock, no randomness, no I/O and no telemetry:

```
$ grep -nE '"(time|math/rand|crypto/rand|os|net|log|context)"' event/{codec,encodable,chain,aggregate,fact,change,seal}.go
EXIT=1, no hits
$ grep -rniE 'eventmemory|eventpg|if name ==|registry|init\(\)' event/{codec,encodable,chain,aggregate,fact,change,seal}.go
EXIT=1, no hits
```

**What phase 5 added, and the mutation each new case kills.** The suite is
**twelve** tests and **two** fuzz targets. The three new names are each a
property the given nine reached no assertion for, and none of them renames or
replaces a case that was there:

- **`TestACodecPanicBecomesThatMethodsOwnRefusal`.** The section's **Degradation**
  paragraph states the panic policy for all three codec methods; only
  `CanEncode`'s was asserted. `encodeWith`'s and `decodeWith`'s `recover` could
  each be deleted with the suite green, and a panicking codec then took the
  process down at a decision and at a fold. Two subtests plus the answering codec
  as their control.
- **`TestAFoldIsPureOverTheStateItIsGiven`.** §INV-004's second checkable proxy —
  one (state, changes) pair folded from **two independently constructed** states
  producing two equal results — with the falsifier §INV-004 names beside it: a
  fold that counts how often it has run must fail the same proxy, and §UC-068's
  in-place advance must still pass it, so the two questions stay apart.
- **`TestOneDeclarationIsDecidedAndFoldedFromManyGoroutines`.** C9's clause is
  that the codec, the mapper and the fold are called from every request goroutine
  at once. Eight goroutines mint, fold, render a key and round-trip on one
  declaration for fifty rounds each, and each asserts its own answer.
- **`FuzzAStoredPayloadIsFoldedOrRefused`** drives the reader chain at arbitrary
  revisions over arbitrary bytes — which is what a store's rows are after an
  older deploy — and pins three properties: the refusal is `ErrRevision` or
  `ErrPayload` and never a declaration- or request-class one, a refusal leaves the
  state it was given, and the same bytes fold to the same state twice.
  **`FuzzADecidedFactIsFrozenAgainstItsCallersBuffer`** mints a change through the
  aliasing codec over arbitrary bytes, overwrites the caller's own buffer and
  asserts the recorded fact is unchanged and that the frozen array carries no
  spare capacity. Both seed corpora run on every `make unit`; both were also run
  for 60 s each (12.0 M and 12.3 M execs, no failures, nothing written to
  `testdata/fuzz`).

The rest of phase 5 is subtests of the nine, and every one of them was written
because a mutation survived:

```
encodeWith / decodeWith recover nothing  → "an encoder that panics …" / "a decoder that
                                            panics …": the binary panics
Then never asks the codec                → "a refusing codec at revision 2 answered <nil>"
the refusal names revision 1 whichever   → "was refused with … which names no revision"
it was
a pointer does not make what it holds    → "refused a pair reached through a struct a map
addressable                                 holds behind a pointer"
a slice element is as addressable as     → "refused a pair held in a slice a map holds"
the slice
an array element is addressable          → "accepted a pair held in an array a map holds"
wherever the array stands
the visited set is keyed by the type     → "accepted a type legal where it stands and
rather than the position                    illegal one field over"
the walk forgets the complex kinds       → "accepted a complex field"
the write route asks for text before     → "refused a type that writes itself as JSON and
JSON                                        as text"
a tag's name is the whole tag            → "accepted a tag that names the field beside it
                                            and carries an option"
a tag naming no name renders no name     → the control declaration panics
the walk descends into a field JSON is   → "accepted a struct whose only field
told to skip                                encoding/json is told to skip"
the claim walk counts a field JSON is    → "refused two fields encoding/json is told to
told to skip                                skip"
the claim walk does not remember where   → the binary is killed: the walk does not
it has been                                 terminate on a type embedding a pointer to
                                            itself
an embedded struct with a name of its    → "refused an embedded struct with a JSON name of
own is promoted anyway                      its own"
an embedded pointer promotes nothing     → "refused a struct promoted through an embedded
                                            pointer"
the walk's bounds are raised, or          → "the walk is bounded at 1048576 deep …" /
deleted outright                            "a type graph of 1100 distinct types was walked
                                            to the end"
the claim walk counts no names           → "a struct rendering 1100 JSON names was
                                            collected to the end"
a chain carries only the revision        → "a chain of three reports 2 retained revisions"
before it
the current revision is the chain's      → "revision 1 … reads event.creditedV3 and the
first position                              sample is a event.creditedV1"
a rendered key is capped at the payload  → "an identity rendering a key one byte over the
ceiling                                     kernel cap minted a change carrying <nil>"
the frozen array keeps its spare         → "the frozen payload has 3 bytes of capacity
capacity                                    behind its 13"
two answers holding one map are not      → "a codec that hands out the map it decodes into
sharing memory                              answered <nil>"
an empty slice is compared by address    → "a payload whose slice is empty was reported as
like any other                              a codec reusing its memory"
a float is compared without asking       → "a payload carrying a NaN was accused of losing
about NaN                                   data"
an Equal method is called without        → the comparison panics on a payload whose Equal
reading its signature                       answers a different question
a change no fact decided is reported     → "which reads as a crossing between two streams"
as a crossing
a fold's answer depends on how often     → "one list folded from two equal states produced
Fold has run                                {Balance:290 …} and {Balance:250 …}"
a decision is frozen into one buffer     → WARNING: DATA RACE in the concurrency test
the package holds
```

**Thirty-one mutations applied in phase 5, thirty-one killed.** Two needed the
fixture rewritten before they died, and both are worth stating because the first
fixture proved less than it looked like it did: the map-reuse codec that
**clears** its scratch map is caught by `RoundTrip`'s disturbance arm even with
the pointer comparison deleted, so the fixture became the ordinary shape — filled
and never cleared, which the zero value cannot disturb; and the pointer-position
case needs a pair reached **through** a struct behind a pointer, because a
`*posted` held directly in a map is settled by `ownMethods` before the walk's
pointer arm is reached at all.

**Two test-side repairs the campaign forced.** The bounds subtest first derived
its shapes from `codecGraphNodes`, so raising the constant moved the test with the
code — S1's GAP-T15 in miniature; it now asserts the three constants against the
numbers its shapes were built from and uses literals for the shapes. And the
diagnosis table called `.Error()` on its refusal without checking for `nil`, so a
mutation that accepted a shape crashed instead of naming it.

**Checkpoint S2 — phase 5 (tests)**

```
test "$(go test -list '^(TestADeclaration|TestEveryMalformedDeclarationPanicsAndTryDefineReturnsIt|TestACodecPanicBecomesThatMethodsOwnRefusal|TestTheSealRefusesALateFact|TestOneDeclarationIsDecidedAndFoldedFromManyGoroutines|TestFoldRefusesAnotherInstance|TestAFoldIsPureOverTheStateItIsGiven|TestAReferenceKindStateFoldsWithoutAliasing|TestAChangeRetainsNoApplicationValue|TestACodecThatDecodesIntoAReusedBufferIsCaught|TestAnUpcasterMayRefuseAndAFoldMayNot|TestTheCrossingsThatMustNotCompile)$' ./event/ | grep -c '^Test')" = 12 && test "$(go test -list '^(FuzzAStoredPayloadIsFoldedOrRefused|FuzzADecidedFactIsFrozenAgainstItsCallersBuffer)$' ./event/ | grep -c '^Fuzz')" = 2 && go test -race -count=1 -v -run '^(TestADeclaration|TestEveryMalformedDeclarationPanicsAndTryDefineReturnsIt|TestACodecPanicBecomesThatMethodsOwnRefusal|TestTheSealRefusesALateFact|TestOneDeclarationIsDecidedAndFoldedFromManyGoroutines|TestFoldRefusesAnotherInstance|TestAFoldIsPureOverTheStateItIsGiven|TestAReferenceKindStateFoldsWithoutAliasing|TestAChangeRetainsNoApplicationValue|TestACodecThatDecodesIntoAReusedBufferIsCaught|TestAnUpcasterMayRefuseAndAFoldMayNot|TestTheCrossingsThatMustNotCompile)$' ./event/ && go test -race -count=1 ./event/
```

**Checkpoint S2 — phase 5, executed 2026-09-07**

```
$ go test -list '^(…the twelve names…)$' ./event/ | grep -c '^Test'      12
$ go test -list '^(…the two fuzz names…)$' ./event/ | grep -c '^Fuzz'     2
$ go test -race -count=1 -v -run '^(…the twelve names…)$' ./event/
--- PASS: TestTheCrossingsThatMustNotCompile (0.13s)
--- PASS: TestADeclaration (0.04s)
--- PASS: TestEveryMalformedDeclarationPanicsAndTryDefineReturnsIt (0.00s)
--- PASS: TestACodecPanicBecomesThatMethodsOwnRefusal (0.00s)
--- PASS: TestFoldRefusesAnotherInstance (0.00s)
--- PASS: TestAReferenceKindStateFoldsWithoutAliasing (0.00s)
--- PASS: TestAChangeRetainsNoApplicationValue (0.00s)
--- PASS: TestAFoldIsPureOverTheStateItIsGiven (0.00s)
--- PASS: TestACodecThatDecodesIntoAReusedBufferIsCaught (0.00s)
--- PASS: TestTheSealRefusesALateFact (0.03s)
--- PASS: TestOneDeclarationIsDecidedAndFoldedFromManyGoroutines (0.01s)
--- PASS: TestAnUpcasterMayRefuseAndAFoldMayNot (0.00s)
ok  	github.com/frostgrove/vv/event	1.211s          56 subtests
$ go test -race -count=1 ./event/       ok …/event 1.243s, then 1.232s
$ go vet ./event/...                    EXIT=0
$ gofmt -l .                            silent
$ go build ./...                        EXIT=0
$ make check                            nine arms, all ok
$ make unit                             EXIT=0, zero FAIL lines
$ go test -run XXX -fuzz FuzzAStoredPayloadIsFoldedOrRefused -fuzztime 60s ./event/
  12000207 execs, 529 new interesting, PASS
$ go test -run XXX -fuzz FuzzADecidedFactIsFrozenAgainstItsCallersBuffer -fuzztime 60s ./event/
  12348255 execs, PASS
```

**Checkpoint S2 — phase 5, re-executed 2026-09-07 after the test review's round 1**

```
$ go test -list '^(…the twelve names…)$' ./event/ | grep -c '^Test'      12
$ go test -list '^(…the two fuzz names…)$' ./event/ | grep -c '^Fuzz'     2
$ go test -race -count=1 -v -run '^(…the twelve names…)$' ./event/
--- PASS: TestTheCrossingsThatMustNotCompile (0.12s)
--- PASS: TestADeclaration (0.04s)
--- PASS: TestEveryMalformedDeclarationPanicsAndTryDefineReturnsIt (0.00s)
--- PASS: TestACodecPanicBecomesThatMethodsOwnRefusal (0.00s)
--- PASS: TestFoldRefusesAnotherInstance (0.00s)
--- PASS: TestAReferenceKindStateFoldsWithoutAliasing (0.00s)
--- PASS: TestAChangeRetainsNoApplicationValue (0.00s)
--- PASS: TestAFoldIsPureOverTheStateItIsGiven (0.00s)
--- PASS: TestACodecThatDecodesIntoAReusedBufferIsCaught (0.00s)
--- PASS: TestTheSealRefusesALateFact (0.03s)
--- PASS: TestOneDeclarationIsDecidedAndFoldedFromManyGoroutines (0.01s)
--- PASS: TestAnUpcasterMayRefuseAndAFoldMayNot (0.00s)
ok  	github.com/frostgrove/vv/event	1.215s          62 subtests
$ go test -race -count=2 ./event/...    ok …/event 1.447s
$ go vet ./event/...                    EXIT=0
$ gofmt -l .                            silent
$ make check                            nine arms, all ok
$ make unit                             EXIT=0, zero FAIL lines
$ go test -run XXX -fuzz FuzzAStoredPayloadIsFoldedOrRefused -fuzztime 30s ./event/
  5 789 245 execs, 26 new interesting, PASS
$ go test -run XXX -fuzz FuzzADecidedFactIsFrozenAgainstItsCallersBuffer -fuzztime 20s ./event/
  3 984 166 execs, PASS
```

Nothing was written to `event/testdata/fuzz`. The section is **twelve** tests, **two** fuzz
targets and **62** subtests; the seven source files are **1 110** lines, the longest
`event/encodable.go` at **379** (was 290) with refusals 6, 7 and 8, still under the 400-line
threshold but no longer with a section's worth of room — the next arm the walk gains is the one
that splits it. The exported surface is the same 22.

**Checkpoint S2 — phase 5's name list as it stood after phase 4's round 2, superseded by the twelve above**

```
$ go test -list '^(…the nine names…)$' ./event/ | grep -c '^Test'
9
$ go test -race -count=1 ./event/                               ok  …/event  1.204s (twice)
$ go vet ./event/...                                            EXIT=0
$ gofmt -l .                                                    silent
$ go build ./...                                                EXIT=0
$ make check                                                    nine arms, all ok
$ make unit                                                     EXIT=0, zero FAIL lines
```

**The section checkpoint, run verbatim as handed over, after phase 5:**

```
$ go build ./... && go test -race -count=1 ./event/ && go test -race -count=1 -v -run 'TestADeclaration|TestTheSealRefusesALateFact|TestFoldRefusesAnotherInstance|TestACodecThatDecodesIntoAReusedBufferIsCaught|TestTheCrossingsThatMustNotCompile' ./event/ && test -z "$(gofmt -l event)"
ok  	github.com/frostgrove/vv/event	1.241s
--- PASS: TestTheCrossingsThatMustNotCompile (0.12s)
--- PASS: TestADeclaration (0.04s)                          13 subtests
--- PASS: TestFoldRefusesAnotherInstance (0.00s)            10 subtests
--- PASS: TestACodecThatDecodesIntoAReusedBufferIsCaught (0.00s)  9 subtests
--- PASS: TestTheSealRefusesALateFact (0.03s)                3 subtests
ok  	github.com/frostgrove/vv/event	1.207s
EXIT=0
```

**Round 2's own mutation pass — nine mutations, nine killed.** Each was applied to
the library, the named test run, and the file restored:

```
the claim walk stops at the fields      → "accepted two embedded structs rendering one
declared on the struct                     JSON name", and the diagnosis row with it
objectKey asks the kinds first          → "accepted a string-kind map key that writes
                                           itself and declares no reader"
the read-without-a-writer key arm       → "accepted a map key that reads itself and is
removed                                    written as its kind"
the shared-backing arm removed          → "a codec that hands out a slice of the buffer
                                           it decodes into answered <nil>"
the fidelity comparison removed         → "a codec that wrote three bytes of a six-byte
                                           payload answered <nil>"
the fidelity comparison is DeepEqual    → "a payload carrying a monotonic reading and a
                                           local zone … was accused of losing data"
the Equal method is not asked           → "a codec that wrote the note and forgot the
                                           time answered <nil>"
unexported fields are compared          → "a payload carrying a populated unexported
                                           field was accused of losing data"
sealed is a plain bool again            → WARNING: DATA RACE in "a fact declared as the
                                           first reader runs …", and the test FAILs
```

The benchmarks beside that last one, 200 000 iterations each on a 20-thread
machine, are what the seal's fast path is for and what its control is:

```
                                             before      after
BenchmarkAChangeIsMintedSerially-20          328.5 ns/op  320.1 ns/op
BenchmarkChangesAreMintedOnOneAggregate-20   165.6 ns/op   42.4 ns/op   ← one declaration
BenchmarkChangesAreMintedOnDistinctAggregates 67.1 ns/op   43.7 ns/op   ← 64 declarations
```

**Round 1's own mutation pass — ten mutations, ten killed.** Each was applied to
the library, the named test run, and the file restored:

```
Encode marshals value, not &value       → "the recorded fact folded to [\"\"]: a marshaller
                                           on the pointer receiver that encoding/json was
                                           never able to reach writes an empty object"
the writes-without-a-reader arm removed → cents is refused through the arm beside it,
                                           with a message naming the wrong repair
the unreachable-marshaller arm removed  → "accepted a marshalling pair on the pointer
                                           receiver, held where JSON cannot address it"
the empty-object refusal removed        → "accepted a struct with fields and none
                                           encoding/json writes"
the colliding-name refusal removed      → "accepted two fields rendering one JSON name"
objectKey stops asking for UnmarshalText→ "accepted a map key that writes itself and
                                           declares no reader"
the walk ignores addressability         → the map-value pair is accepted
the aliasing verdict taken after the    → "a revision-1 codec that reuses its decode
upcasters                                  buffer answered <nil> behind an upcaster"
Fold compares streams first, bare       → both the crossing's message assertion and the
sentinel                                   blank-identity case go red
a sixth enumerated reader that does not → "event/seal.go tells a reader of the library
exist                                      that [… Bind …] seal the declaration and […] do"
```

The three arms of `ownMethods` share `ErrCodecType` and each is subsumed by the
next as a *verdict*, so two of the ten are killed only by the diagnosis subtest.
That is the finding stated rather than hidden: a table asserting the sentinel
alone would have passed with the wrong repair printed.

**Thirty-seven mutations, thirty-six killed on the first pass.** Each was applied
to the library, the named test run, and the code restored. The ones worth keeping
because they are the defects the section exists to prevent:

```
Fold does not clone the frozen bytes    → "the second fold published \"Xne\", so a
                                           mutation of a state rewrote a decided fact"
Fact.New does not freeze the encoding   → "the recorded fact reads \"Xne\" after the
                                           caller mutated its own payload"
Fold compares streams before the key    → "a blank identity answered ... this change was
                                           decided for another stream"
a seventh reader seals without a row    → "sealed from [... Fact.Name ...] and the
                                           enumeration names [...]"
RoundTrip does not decode a second time → "a codec whose decoded value aliases a buffer
                                           it rewrites answered <nil>"
RoundTrip keeps no copy of any of the   → the same message, once per clause; the
three encodings it holds                   fixed-width fixture codec is what makes all
                                           three observable
the codec walk accepts an interface     → "the shipped codec accepted an interface field"
the walk asks "does it marshal itself"  → "accepted a field typed as an interface that
before "is it an interface"                marshals itself"; a value of it encodes and
                                           never reads back, so the order is load-bearing
the codec walk has no visited set       → a recursive type is refused at depth 1024
an upcaster's panic is not recovered    → the test binary panics
a fold's panic IS recovered             → "an application's own bug arrives as a
                                           framework refusal a retry loop will hammer"
```

**The one survivor and what closed it.** `Fact.New`'s kernel payload ceiling —
`len(encoded) > MaxPayloadBytes` — could be deleted with every test still green,
because §UC-024's caller-side cap had no assertion. `TestFoldRefusesAnotherInstance`
gained *a payload over the kernel ceiling is refused where no store is known*, with
a payload **at** the ceiling as its control; both the deletion and an off-by-one
(`>=`) now turn it red.

**What is implemented and not exercised by S2, named rather than left to be
found.** `applierOf`'s revision bound (`ErrRevision`) is reachable from `Load`
alone, since a `Change` always carries the current revision — S2 drives it
directly through the fact's own applier so the branch is not dead, and S4's
`Load` is what exercises it in anger. The codec walk's three limits (depth 1024,
distinct types 1024, edges 4096) are asserted only through the recursive-type
case; a graph that reaches the node or edge bound needs generated source, and
they are `jobs`'s own numbers.

---

### S3 — `event/eventmemory`: a complete, transaction-capable store  `[x]`

**Both blocks are executed and green.** The suite the implementation carried is
**fourteen** tests in three files — `event/eventmemory/store_test.go`,
`transaction_test.go`, `read_test.go` — carrying the eight names this section
planned, plus
`TestAStreamIsReadInPagesTheStorePublished`: the two published page bounds, a
page's own shape (first version `after + 1`, dense, this stream), the tiling of
`ReadAll` from cursor to cursor, and the four hand-built cursors. The kernel
verifies that shape in S4 and the suite certifies it in S5; the store that
produces it has to be held to it here, or S4 would be verifying a store nothing
had checked. They are `package eventmemory_test`, so every value the tests reach
is a value a store's own consumer can reach.

**Five more came from the S3 review**, one per finding it closed, each with the
control that stops it passing vacuously:

| Test | Pins | Its control |
|---|---|---|
| `TestTwoLogsCarryTheirOwnTransactionsInOneContext` | GAP-1: two `WithTransaction` calls for two logs both stay findable; each store names its own transaction and each append lands in it | The same log alone, and the two authorities asserted **not** `Same` |
| `TestOneTransactionUsedFromManyGoroutinesRefusesRatherThanCrashes` | GAP-2: 200 rounds of append ‖ read ‖ commit-or-rollback over one `*Tx` under `-race`; every non-nil answer is byte-identical to the refusal an already-finished `*Tx` gets | The refusal from a transaction finished **before** the call, computed first and compared against |
| `TestTransactionAnswersWhatTheContextCarriesAfterTheStoreIsClosed` | GAP-3: a closed store names the same transaction it named while open, and answers the invalid authority with nothing bound | The open store's answer, and the `Append` that follows refusing `Closed` |
| `TestATransactionThisStoreCannotUseIsRefusedAtEveryDoor` | GAP-4: a finished `*Tx` and a nil one are `Refused` at `Append`, `ReadStream` **and** `ReadAll` | A live transaction served at all three |
| `TestAStoreIsBuiltOverEveryLogTheKernelWouldAdmit` | GAP-5 and GAP-6: a log at any payload bound the kernel admits, up to `MaxPayloadBytes`, builds a store with nothing operational set | The page at exactly `event.ResidentPage` admitted, and that page plus one refused with `ErrWrongStore` |

**Phase 5 added twelve more** — ten tests and two fuzz targets, in seven new
files (`capability_test.go`, `admission_test.go`, `ownership_test.go`,
`cancellation_test.go`, `resumption_test.go`, `concurrency_test.go`,
`construction_test.go`), taking the package to **24 tests and 2 fuzz targets**.
They close the half of this section's `Covers` line that the fourteen above had
left to a blanket `go test ./event/...`: what the store *claims*, what it does
with a caller's bytes and a caller's cancellation, what a cursor is worth after a
restart, and what many goroutines leave behind.

| Test | Pins | Its control |
|---|---|---|
| `TestTheStoreDeclaresWhatItCanDoAndSaysWhatItCannot` | §UC-044, §UC-037: none of the four capabilities is `Unstated`; each claim is asserted through the behaviour that earns it, and `Persistence: Unsupported` is the one it declines; `Capabilities`, `Limits` and `Backing` answer the same after a write and after `Close` | The values are asserted non-empty before they are compared, so a store answering zeros everywhere fails the control rather than passing the constancy case |
| `TestAnAppendIsAdmittedOnlyAtTheVersionItWasDecidedAt` | §INV-002, §INV-003, §UC-022's admission half: four wrong versions including `MaxUint64` are refused and leave the stream untouched; a refused batch of three leaves none of its records; an admitted one takes dense versions, one recorded instant and contiguous positions; after a conflict the caller decides again and the refused list is nowhere in the history | The same batch at the observed version is admitted, so a store that refuses everything fails |
| `TestAClaimCoversOnlyTheStreamsATransactionWroteTo` | The claim's scope and its release: an autocommit append to a claimed stream is refused, a second transaction on a stream the first never touched is admitted, and both a commit and a rollback free what they held | The append to another stream, admitted while the claim is live |
| `TestVersionsAreDenseAndPositionsAscendWhateverTheClockSays` | §INV-009: dense versions per stream, strictly ascending positions across three streams and a rollback, one stream's order preserved inside the log, under a clock that runs **backwards** | The instants are asserted to descend and the first to be the injected clock's own, so a store ignoring the clock fails the control instead of passing the ordering case |
| `TestNothingACallerHandsToAnAppendIsRetainedOrRewritten` | §INV-021 hand-offs 3 and 6, inbound: the store writes into none of the caller's arrays, overwriting them afterwards changes no history, the `[]Record` is the caller's to rewrite and reuse, and a staged append keeps the same distance | The first subtest reads the two payloads back, so the three that follow are over a store that recorded something |
| `TestACancellationTravelsAsItselfFromEveryDoorTheStoreOperates` | §UC-057's first window: `Append`, `ReadStream`, `ReadAll`, `Check` and `Begin` answer `context.Canceled`/`DeadlineExceeded` **bare**, matched by `errors.Is` and compared against all seven of the kernel's renderings so that none of them is what travelled; nothing was written; `Commit` reads no deadline | The same five doors on a live context, and the append that lands there |
| `TestACursorIsTheBackingsAndResumesThroughAnyStoreValueOverIt` | §UC-053, §INV-035: a cursor persisted as text resumes in a second store value over one log; two values mint the **same** cursor at one position; a checkpoint at the end neither rewinds nor skips what is written after it; burned positions skip nothing; the empty cursor is the start of a log and of an empty one | The foreign cursor refused beside them, and the one-page-behind resume that must return the following page |
| `FuzzACursorEitherResumesInsideTheLogOrIsRefused` | The one value a consumer persists, decoded from bytes nobody minted: either `Failure(BadCursor, …)` with no page and no cursor, or a page that is a contiguous run of the log at most `MaxRead` long whose own cursor reads the run after it. Fed both the raw input and a real cursor with the input appended | The seed corpus holds a cursor that parses, so the resuming arm is exercised on every run |
| `FuzzAPayloadIsHandedBackByteForByteAndBelongsToWhoeverReadsIt` | §INV-021 hand-offs 4 and 7 over arbitrary bytes: the caller overwrites its array after the append and the reader writes into the page it was handed; the history reads what was appended both times | The first read asserts the bytes came back before either party writes |
| `TestManyWritersLeaveOneDenseHistoryAndAMonotoneLog` | §INV-009, §INV-003, §UC-022 and §UC-037 under `-race`: six writers, three streams, twenty rounds each, half of them transactional, reloading and deciding again on every conflict, beside a reader tiling `ReadAll` throughout. Versions dense, positions strictly ascending, the two reads conserving one set, each decision committed exactly once, and no reader ever handed a position below one it had already been handed | The tiling reader's count is asserted non-zero, so its monotone claim is never made over an empty log |
| `TestEveryNumberAStoreAndItsLogTakeIsBoundedAtBothEnds` | §UC-006's constructor half: a spec naming no log, and every one of the five numbers below zero and above the kernel's ceiling, are `ErrWrongStore`; zero is the published default; what the caller set is what the store publishes | The same numbers at exactly the ceiling, admitted and published |
| `TestAStoreIsBuiltPerRequestOverALogThatOutlivesIt` | §UC-055, §INV-013, §INV-014: two hundred stores constructed, written through and closed over one log leave a dense history the next one reads with no step that found it, and no goroutine any constructor started is still running | The goroutine count is taken before the loop and polled after it, so the assertion is against this test's own baseline |

**The test review's round 1 closed eight more findings** — GAP-T1 to GAP-T8 of
`EVENTSOURCE_P1_S3_TEST_GAPS.md`, every `[immediate]` one it graded. What they
had in common is that the suite could not see a *field* or an *identity* the
store handles rather than a behaviour it performs: three of the nine mutations
they name would have shipped a store whose histories are undecodable, interleaved
across aggregates or crossed between streams, at 98.2 % statement coverage. The
package is now **28 tests and 2 fuzz targets** across eleven test files, and the
kernel gains its first test of `event/bounds.go`.

| Test | Pins | Its control |
|---|---|---|
| `TestAnEnvelopeCarriesTheTypeAndRevisionOfTheRecordItWasWrittenFrom` | GAP-T1: five distinct type/revision pairs, asserted per offset over a committed read, a staged read inside a transaction and a `ReadAll` page — the two fields S2's chain uses to find the fact and the decoder | The five kinds are asserted distinct before anything is read, so nothing below is satisfied by a store answering one constant |
| `TestTwoFamiliesSharingOneKeyAreTwoStreams` | GAP-T2, §INV-033, and the corruption §INV-039 is written against: two families at one key are two histories — versions from 1 each, their own `ReadStream` pages, a claim on one that does not refuse an append to the other, and a `ReadAll` holding both under two stream identities | The account's own append at the shared key is admitted first, so the order's admission at version 0 is about the family and not about the key being free |
| `TestATransactionThatStagesToTwoStreamsNeverCrossesThem` | GAP-T3, §UC-028's two-stream unit of work: each read inside the transaction answers only its own stream, in order, at dense versions continuing the committed ones, and the commit publishes both in each stream's own order | A third stream the transaction never touched, read inside it, must be empty — which is what fails when `stagedFor` stops filtering |
| `TestAPageOfStagedRecordsIsTheCallersToo` | GAP-T5, §INV-021's fourth hand-off on the staged half: a page of one committed record beside two staged ones is written into byte by byte and appended to, and the transaction re-reads what it staged and commits it | The page is asserted to hold the committed record beside the staged ones before anything is written into it |
| `TestTheResidentPageIsTheCeilingCountedInEnvelopes` (`event/bounds_test.go`) | GAP-T7, §INV-022: `ResidentPage` at seven bounds — the two non-positive arms, an exact divisor, a truncating divisor, the kernel's own payload ceiling and a bound above the whole ceiling — against counts divided out of 64 MiB by hand | `MaxResidentBytes` itself is asserted to be the 64 MiB those counts were derived from, so a changed ceiling is a red test rather than a silently re-based one |

Three existing tests grew the assertion they were short of, a fourth stopped
computing its expected page with the function under test (`TestAStoreIsBuiltOverEveryLogTheKernelWouldAdmit`, GAP-T7), and two shared
helpers stopped being constants: `streamOf` takes a family (GAP-T2 — six
families are now in use and no `const family` remains), `records` derives each
record's type and revision from its place in the batch and its payload (GAP-T1),
`TestASecondAppendInOneTransactionIsAdmitted` begins a **second** transaction on
one store and asserts the two authorities are not `Same` while both are live
(GAP-T6, §INV-028's second half), `TestAStreamIsReadInPagesTheStorePublished`
runs over `StreamPage: 2, MaxRead: 3` and asserts each read is capped by its own
published number (GAP-T4), and
`TestVersionsAreDenseAndPositionsAscendWhateverTheClockSays` asserts a store
built with **no** `Clock` records an instant between two the test sampled around
the append (GAP-T8).

**Delivers** the second implementation, so that S4's caller seam is exercised
against a real store rather than a double. Its conformance proof lands in S5;
this section's evidence is its own package tests.

**Files** `event/eventmemory/doc.go`, `log.go`, `store.go`, `append.go`,
`read.go`, `transaction.go`, `cursor.go`; and, from the review, `event/bounds.go`
(`ResidentPage`) and `event/store.go` (the closed-store sentence on
`Transaction`).

**Realises** `LogSpec`/`Log`/`NewLog`, `Spec`/`Store`/`New`, the eight contract
methods, `Check`, `Tx`/`Begin`/`WithTransaction`/`Commit`/`Rollback`, and
`event.ResidentPage`.

**Carried gaps** C7 (a full-slice hand-out per envelope and a fresh `[]Envelope`
per call), C9 (`Spec.Clock` is retained and called concurrently — stated and
obeyed).

**Decides** where a conflict is reported (from `Append`, against committed +
staged, so §UC-022's caller branch fires on this store exactly as it does on a
SQL one), whether a read inside a transaction sees that transaction's staged
appends (it does), when a position is assigned (at commit, under one store-wide
mutex, so commit order is position order), what "burned" means under that answer
(`Rollback` advances the counter by what it staged), and therefore that
`MonotoneVisibility: Supported` is honest. The design is written out under
`### event/eventmemory/`, and the nine answers the contract had left unstated —
what this store's *not a transaction* is, where a transaction of another log
leaves it, what it answers when a caller drives it past the kernel's own check,
whether finishing a transaction reads a deadline, whether a second `Rollback` is
an error, where the commit-time re-validation lives, what position a staged
envelope reads back at, whether a `*Tx` may be used from two goroutines, and what
`Transaction` answers on a closed store — are decided in the table beneath it.
The last three are the review's, and two of them changed the kernel: `event`
gains `ResidentPage`, and `Store.Transaction`'s closed-store sentence is
corrected.

**Covers** UC-006 (the constructor half), UC-008, UC-022 (admission), UC-028
(the store's `Tx`), UC-029 (rollback), UC-037, UC-040, UC-044, UC-047, UC-053
(the cursor), UC-054, UC-055, UC-057 (the two windows); INV-002, INV-003,
INV-009, INV-013 (nothing started, nothing global), INV-014, INV-021 (hand-offs 4
and 7, committed and staged alike), INV-022 (the resident ceiling counted in
envelopes), INV-028 (two live transactions on one store are not `Same`), INV-032,
INV-033 (the store keys its history by family and key both), INV-035, INV-041
(the store's half).

**Degradation** `Persistence: Unsupported` — everything is lost on process exit,
which the capability states rather than implies. `Close` releases what the store
opened and nothing else; it commits nothing and rolls nothing back. An abandoned
transaction's claims are never reclaimed, which `doc.go` states.

**Budget** one allocation and one ~200 B copy per envelope returned (§D.13): a
100 000-event load copies ~20 MB across 391 pages and holds one page at a time.
That cost falls on the store used in unit tests, not on the production store.

**Checkpoint S3 — phase 4 (implementation)**

```
go build ./... && go vet ./event/... && test -z "$(gofmt -l event)" && go test -race -count=1 ./event/...
```

**Checkpoint S3 — phase 5 (tests)**

```
test "$(go test -list '^(TestTwoStoreValuesOverOneLog|TestCheckAnswersWhileTheStoreIsOpenAndAfterItIsClosed|TestARolledBackAppendBurnsItsPositions|TestASecondAppendInOneTransactionIsAdmitted|TestAReadInsideATransactionSeesItsOwnStagedAppends|TestTwoTransactionsOnOneStreamLeaveOneWinnerAndAConflictFromAppend|TestCloseIsIdempotentAndDecidesNothing|TestAPageIsTheCallersIncludingItsCapacity)$' ./event/eventmemory/ | grep -c '^Test')" = 8 && go test -race -count=1 -v -run '^(TestTwoStoreValuesOverOneLog|TestCheckAnswersWhileTheStoreIsOpenAndAfterItIsClosed|TestARolledBackAppendBurnsItsPositions|TestASecondAppendInOneTransactionIsAdmitted|TestAReadInsideATransactionSeesItsOwnStagedAppends|TestTwoTransactionsOnOneStreamLeaveOneWinnerAndAConflictFromAppend|TestCloseIsIdempotentAndDecidesNothing|TestAPageIsTheCallersIncludingItsCapacity)$' ./event/eventmemory/ && go test -race -count=1 ./event/...

test "$(go test -list '^(TestTheStoreDeclaresWhatItCanDoAndSaysWhatItCannot|TestAnAppendIsAdmittedOnlyAtTheVersionItWasDecidedAt|TestAClaimCoversOnlyTheStreamsATransactionWroteTo|TestVersionsAreDenseAndPositionsAscendWhateverTheClockSays|TestNothingACallerHandsToAnAppendIsRetainedOrRewritten|TestACancellationTravelsAsItselfFromEveryDoorTheStoreOperates|TestACursorIsTheBackingsAndResumesThroughAnyStoreValueOverIt|TestManyWritersLeaveOneDenseHistoryAndAMonotoneLog|TestEveryNumberAStoreAndItsLogTakeIsBoundedAtBothEnds|TestAStoreIsBuiltPerRequestOverALogThatOutlivesIt|FuzzACursorEitherResumesInsideTheLogOrIsRefused|FuzzAPayloadIsHandedBackByteForByteAndBelongsToWhoeverReadsIt)$' ./event/eventmemory/ | grep -cE '^(Test|Fuzz)')" = 12 && go test -race -count=1 -v -run '^(TestTheStoreDeclaresWhatItCanDoAndSaysWhatItCannot|TestAnAppendIsAdmittedOnlyAtTheVersionItWasDecidedAt|TestAClaimCoversOnlyTheStreamsATransactionWroteTo|TestVersionsAreDenseAndPositionsAscendWhateverTheClockSays|TestNothingACallerHandsToAnAppendIsRetainedOrRewritten|TestACancellationTravelsAsItselfFromEveryDoorTheStoreOperates|TestACursorIsTheBackingsAndResumesThroughAnyStoreValueOverIt|TestManyWritersLeaveOneDenseHistoryAndAMonotoneLog|TestEveryNumberAStoreAndItsLogTakeIsBoundedAtBothEnds|TestAStoreIsBuiltPerRequestOverALogThatOutlivesIt|FuzzACursorEitherResumesInsideTheLogOrIsRefused|FuzzAPayloadIsHandedBackByteForByteAndBelongsToWhoeverReadsIt)$' ./event/eventmemory/ && go test -race -count=3 ./event/... && go test -race -count=1 -shuffle=on ./event/...

test "$(go test -list '^(TestAnEnvelopeCarriesTheTypeAndRevisionOfTheRecordItWasWrittenFrom|TestTwoFamiliesSharingOneKeyAreTwoStreams|TestATransactionThatStagesToTwoStreamsNeverCrossesThem|TestAPageOfStagedRecordsIsTheCallersToo)$' ./event/eventmemory/ | grep -c '^Test')" = 4 && go test -race -count=1 -v -run '^(TestAnEnvelopeCarriesTheTypeAndRevisionOfTheRecordItWasWrittenFrom|TestTwoFamiliesSharingOneKeyAreTwoStreams|TestATransactionThatStagesToTwoStreamsNeverCrossesThem|TestAPageOfStagedRecordsIsTheCallersToo)$' ./event/eventmemory/ && test "$(go test -list '^TestTheResidentPageIsTheCeilingCountedInEnvelopes$' ./event/ | grep -c '^Test')" = 1 && go test -race -count=2 ./event/... && go test -race -count=1 -shuffle=on ./event/...
```

**The first two blocks, run — re-run after the review's six closures** (the
third block is the test review's, and its run is recorded under it):

```
$ go build ./... && go vet ./event/... && test -z "$(gofmt -l event)" && go test -race -count=1 ./event/...
ok  	github.com/frostgrove/vv/event	1.248s
ok  	github.com/frostgrove/vv/event/eventmemory	1.013s

$ gofmt -l .            # silent, whole tree
$ go vet ./event/...    # clean

$ test "$(go test -list '^(...eight names...)$' ./event/eventmemory/ | grep -c '^Test')" = 8 && echo COUNT OK
COUNT OK

$ go test -list '.*' ./event/eventmemory/ | grep -c '^Test'
14

$ go test -race -count=1 -v -run '^(...the same eight...)$' ./event/eventmemory/
--- PASS: TestAPageIsTheCallersIncludingItsCapacity (0.00s)
--- PASS: TestTwoStoreValuesOverOneLog (0.00s)
--- PASS: TestCheckAnswersWhileTheStoreIsOpenAndAfterItIsClosed (0.00s)
--- PASS: TestCloseIsIdempotentAndDecidesNothing (0.00s)
--- PASS: TestASecondAppendInOneTransactionIsAdmitted (0.00s)
--- PASS: TestAReadInsideATransactionSeesItsOwnStagedAppends (0.00s)
--- PASS: TestTwoTransactionsOnOneStreamLeaveOneWinnerAndAConflictFromAppend (0.00s)
--- PASS: TestARolledBackAppendBurnsItsPositions (0.00s)
PASS
ok  	github.com/frostgrove/vv/event/eventmemory	1.007s

$ go build ./... && go test -race -count=1 ./event/... &&
  go test -race -count=1 -v -run 'TestTwoStoreValuesOverOneLog|TestARolledBackAppendBurnsItsPositions|TestClose|TestAPageIsTheCallersIncludingItsCapacity' ./event/eventmemory/ &&
  test -z "$(gofmt -l event)"
ok  	github.com/frostgrove/vv/event	1.248s
ok  	github.com/frostgrove/vv/event/eventmemory	1.013s
--- PASS: TestAPageIsTheCallersIncludingItsCapacity (0.00s)
--- PASS: TestTwoStoreValuesOverOneLog (0.00s)
--- PASS: TestCloseIsIdempotentAndDecidesNothing (0.00s)
--- PASS: TestARolledBackAppendBurnsItsPositions (0.00s)
PASS
ok  	github.com/frostgrove/vv/event/eventmemory	1.009s
EXIT=0

$ go test -race -count=3 ./event/... && go test -race -count=1 -shuffle=on ./event/...
ok  	github.com/frostgrove/vv/event	1.665s      # three runs, and again shuffled:
ok  	github.com/frostgrove/vv/event/eventmemory	1.031s   the 200-round transaction race
ok  	github.com/frostgrove/vv/event	1.243s               is stable, not lucky once
ok  	github.com/frostgrove/vv/event/eventmemory	1.015s

$ make unit          # every module, no FAIL
$ make check
check-deps: ok   check-tiers: ok   check-utils: ok   check-triplets: ok
check-todo: ok   check-replaces: ok   check-tidy: ok   check-otel-schema: ok
check-workspace: ok
```

**Six more mutations from the review's closures, six killed** — each applied to
the fix, the new test run, and the code restored:

```
the binding is one unkeyed context key   → "the first store answered that nothing of its
again, and ambient walks past a foreign    own was bound while its own transaction was",
*Tx                                        and "the first log kept [ours] after the
                                           transaction it was appended in was rolled back"
Append resolves the ambient transaction  → panic: assignment to entry in nil map,
before it takes the lock                   eventmemory.(*Tx).stage → (*Store).Append
Transaction answers the invalid          → "a closed store named a different transaction
authority on a closed store                than it named while open"
ReadAll does not consult the ambient     → "reading the log through a context carrying a
transaction                                transaction that is nil was admitted"
the page default is a flat 256           → "a store over {…StreamPage:0 MaxRead:0} was
                                           refused: StreamPage 256 at MaxPayload 524288 is
                                           more than the 128 envelopes one read may hold"
the store doubles the kernel's own       → "a store over {…StreamPage:65} answered <nil>,
ResidentPage                               where a page holding more than one read may is
                                           not a store the kernel would admit"
```

**Ten mutations, ten killed.** Each was applied to the store, the named test
run, and the code restored — the store's own semantics are all absence-shaped, so
a test that never watched one fail is a test that watched a map behave:

```
handOut does not clone the payload      → "after a reader wrote into the page it was
                                           handed, the stream reads [zzzzzzzzzzz …]"
Rollback does not advance the counter   → "the append after the rollback took position 2
                                           against the earlier 1, so the two positions
                                           the rollback staged were reissued"
Append drops the claim check            → "a second transaction appending to a stream
                                           another one holds was admitted"
Append admits against the committed      → "appending [credited 20] … at version 1 was
version alone rather than committed        refused: [outcome conflict]"
plus staged
ReadStream ignores the bound tx's staged → "a read inside the transaction returned
records                                    [committed], so an operation that appends and
                                           reloads cannot see its own writes"
Check never reports closure             → "a closed store answers <nil> to a readiness
                                           question"
Backing() is derived from the store      → "two stores over one log answer different
value rather than the log                  backings"
Append and the two reads drop the        → "appending to a closed store was admitted",
closed check                               and the committed-work control fails beside it
readCursor does not compare the          → "resuming another log's read from this log's
fingerprint                                cursor was admitted"
ReadStream ignores StreamPage           → "the page after version 0 holds 5 envelopes
                                           against a published page of 2"
```

**Twenty-seven more mutations from the phase-5 suite, twenty-seven killed.**
Each was applied to the store, the one test that owns the property run, and the
file restored byte-identically afterwards (`cmp` against a copy taken before the
campaign, for every file under `event/` and `event/eventmemory/`):

| Mutation | The test that caught it, and what it said |
|---|---|
| `Capabilities` claims `Persistence: Supported` | `TestTheStoreDeclaresWhatItCanDoAndSaysWhatItCannot` — *"the store answers [support supported] for persistence, and a history held in a map outlives nothing"* |
| `Capabilities` answers `Unstated` for transactions | the same — *"the store leaves transactions unstated, and what a store does not state is refused at both doors rather than tried"* |
| `Limits` answers the zero value once the store is closed | the same — *"a closed store publishes {…all zeros} where it published {MaxPayload:65536 …} while open"* |
| `Append` admits anything at or below the observed version | `TestAnAppendIsAdmittedOnlyAtTheVersionItWasDecidedAt` — *"an append at version 0 against a stream at version 1 was admitted"* |
| the clock is read once per record rather than once per append | the same — *"the three records of one batch were recorded at 12:00:02, 12:00:03 and 12:00:04, so the batch was assembled over three instants rather than admitted at one"* |
| `release` stops deleting the claims it took | `TestAClaimCoversOnlyTheStreamsATransactionWroteTo` — *"appending [after the commit] … was refused: [outcome conflict]"* |
| a claim covers every stream rather than the ones it wrote to | the same — *"appending [from outside] … was refused: [outcome conflict]"*, on the stream the transaction never touched |
| versions are counted from zero | `TestVersionsAreDenseAndPositionsAscendWhateverTheClockSays` — *"holds versions [0 1], and a fold that counts from one reads a different history than the store holds"* |
| the store records the wall clock rather than its own | the same — *"the first event was recorded at 2026-09-07 13:21:31.535… rather than at the instant the store's own clock answered"* |
| `Append` keeps the caller's own payload array | `TestNothingACallerHandsToAnAppendIsRetainedOrRewritten` — *"after the caller wrote into the arrays it had appended, the stream reads [zzzzzzzzzzz zzzzzzzzzzz]"*, and the staged case beside it |
| a cancelled append is reported as an uncertain one | `TestACancellationTravelsAsItselfFromEveryDoorTheStoreOperates` — *"answered event: the store reported [outcome unconfirmed], which a caller matching a cancellation cannot recognise as one"* |
| `ReadAll` reads no deadline | the same — *"reading the log under a context the caller cancelled answered <nil>"* |
| `Commit` refuses to finish under a cancelled context | the same — *"committing under the cancelled context answered context canceled, and a transaction that refuses to finish holds its claims and starves every other writer of its streams"* |
| the cursor a read returns names the first event of its page | `TestACursorIsTheBackingsAndResumesThroughAnyStoreValueOverIt` — *"resuming from the persisted cursor in a second store value read [two three four]"* |
| a read that returns nothing answers the start of the log | the same — *"reading from a cursor at the end of the log answered [one two] and moved the cursor to …:2"* |
| the cursor a read returns is one position past its last event | `FuzzACursorEitherResumesInsideTheLogOrIsRefused` — *"resuming from the cursor …:3 answered [four five] where the log continues with \"three\", so the resume point skipped an event"* |
| a cursor this store did not mint is refused as a policy refusal | the same — *"the cursor \"7\" was refused with [outcome refused], which is not the one answer a cursor this store did not mint has"* |
| a refused cursor comes back with a cursor beside the refusal | the same — *"the refused cursor \"7\" came back with 0 envelopes and the cursor \"7\", so a caller that ignores one refusal checkpoints past events it never read"* |
| a read hands out the log's own payload array | `FuzzAPayloadIsHandedBackByteForByteAndBelongsToWhoeverReadsIt` — *"after the reader wrote into the page it was handed, the same event reads …"* on four seeds |
| the admitted version is read outside the section that publishes | `TestManyWritersLeaveOneDenseHistoryAndAMonotoneLog` — *"committing an append the store had admitted was refused: a claimed stream moved while it was claimed"*, which is `errStaleClaim` becoming reachable, exactly as GAP-16 says it would if the claim stopped holding |
| the same window, opened only for an autocommit append | the same — *"holds versions [1 … 11 9 13 14 15 14 …] after concurrent writers, so a version was skipped or reused"* |
| a position is taken when a record is staged rather than when it is published | the same — *"a reader that had already been handed position 12 was then handed 11, so an event became visible below one it had checkpointed past"*, which is the `MonotoneVisibility` claim failing rather than a version |
| `publish` adds to the stream index and not to the log's own | the same — *"the reader tiling the log while the writers ran saw nothing, so what it asserted about positions was asserted over an empty log"*, the vacuity control firing first |
| a number below zero is taken as a number | `TestEveryNumberAStoreAndItsLogTakeIsBoundedAtBothEnds` — *"a log over a payload bound below zero answered <nil>"* |
| the page a store defaults to is halved | the same — *"a log and a store with nothing set publish {… StreamPage:128 MaxRead:128} where the defaults are {… StreamPage:256 MaxRead:256}"* |
| the constructor starts a goroutine | `TestAStoreIsBuiltPerRequestOverALogThatOutlivesIt` — *"202 goroutines are running where 2 were before 200 stores were constructed"* |
| `Close` discards the history the log holds | the same — *"appending [request 1] … at version 1 was refused: [outcome conflict]"* |

The two fuzz targets also ran real campaigns rather than their seed corpus alone:
`-fuzztime 45s` each, **15.9 M** executions for the cursor and **12.9 M** for the
payload, no failure and no new corpus entry the seeds had not already reached for
the cursor.

**Nine mutations from the test review's closures, nine killed.** These are the
nine the review applied and watched survive; each was re-applied here, the suite
run, and the file restored — `diff` reports every implementation file
byte-identical afterwards, and no `testdata/` was written.

| Mutation | The test that caught it, and what it said |
|---|---|
| `Type: ""` and `Revision: 0` in the envelope an append writes | `TestAnEnvelopeCarriesTheTypeAndRevisionOfTheRecordItWasWrittenFrom` — *"the stream reads as [@0 @0 @0] where [accounts.credited@1 accounts.debited@2 accounts.frozen@7] was written, so every fold of it looks up the wrong fact and the wrong decoder for a history that is intact"* |
| every record recorded under the first one's type at its revision plus seven | the same — *"the stream reads as [accounts.credited-mutated@8 …] where [accounts.credited@1 accounts.debited@2 accounts.frozen@7] was written"*, and the staged read and the log beside it |
| the family dropped from the log's three `event.Stream`-keyed maps, nine call sites | `TestTwoFamiliesSharingOneKeyAreTwoStreams` — *"creating an order at the key an account already uses was refused with [outcome conflict], so a key names one stream whatever family it was written under and two aggregates share one history and one version counter"* |
| `stagedFor` returns every staged envelope unfiltered | `TestATransactionThatStagesToTwoStreamsNeverCrossesThem` — *"the account read inside the transaction returned [credited 10 credited 20 credited 30 credited twice], so an aggregate reloaded inside the unit of work that wrote it folds another stream's events"* |
| `ReadStream` pages by `MaxRead` rather than `StreamPage` | `TestAStreamIsReadInPagesTheStorePublished` — *"a stream page holds 3 envelopes where the store publishes a stream page of 2 and a log page of 3, so a read of one stream is cut by the other number"* |
| a staged envelope handed out without the payload clone | `TestAPageOfStagedRecordsIsTheCallersToo` — *"after a reader wrote into the page it was handed inside a transaction, that transaction reads [committed zzzzzz zzzzzzzzzz] — so the record it is about to commit is the reader's to rewrite"* |
| `Transaction` identifies the authority by the store value rather than the transaction | `TestASecondAppendInOneTransactionIsAdmitted` — *"two transactions live on one store at once answered authorities that compare the same, so two subsystems that each opened their own prove they wrote together and the operation commits in two units"* |
| `ResidentPage` answers twice the count | `TestTheResidentPageIsTheCeilingCountedInEnvelopes` — *"the kernel's own payload ceiling admits a page of 128 envelopes where 64 of them is what 64 MiB holds"*, with both arms of `TestAStoreIsBuiltOverEveryLogTheKernelWouldAdmit` red beside it |
| the default clock is the zero instant | `TestVersionsAreDenseAndPositionsAscendWhateverTheClockSays` — *"a store constructed with no clock recorded an event at 0001-01-01 00:00:00 +0000 UTC where the append ran between … and …"* |

**Both checkpoint blocks re-run after the eight closures**, plus the counts the
new tests add:

```
$ gofmt -l .                                    # silent, whole tree
$ go vet ./event/...                            # clean
$ test "$(go test -list '^(…the section's eight…)$'  | grep -c '^Test')" = 8      BLOCK1 COUNT OK
$ test "$(go test -list '^(…the phase-5 twelve…)$'   | grep -cE '^(Test|Fuzz)')" = 12  BLOCK2 COUNT OK
$ test "$(go test -list '^(…the review's four…)$'    | grep -c '^Test')" = 4      BLOCK3 COUNT OK
$ test "$(go test -list '^TestTheResidentPageIsTheCeilingCountedInEnvelopes$' ./event/ | grep -c '^Test')" = 1   KERNEL COUNT OK

$ go test -list '.*' ./event/eventmemory/ | grep -cE '^(Test|Fuzz)'
30                                              # 28 tests and 2 fuzz targets
$ go test -v ./event/eventmemory/ | grep -c '^    --- PASS'
104

$ go test -race -count=2 ./event/...
ok  	github.com/frostgrove/vv/event	1.449s
ok  	github.com/frostgrove/vv/event/eventmemory	1.045s

$ go test -race -count=1 -shuffle=on ./event/...     ok, twice
$ go test -coverprofile -covermode=atomic ./event/eventmemory/
98.2% of statements                             # the same four uncovered, all deferred

$ go test -fuzz FuzzACursorEitherResumesInsideTheLogOrIsRefused -fuzztime 30s
10 579 583 execs, no new interesting, PASS
$ go test -fuzz FuzzAPayloadIsHandedBackByteForByteAndBelongsToWhoeverReadsIt -fuzztime 30s
164 897 execs, no new interesting, PASS

$ make unit          # every module, no FAIL
$ make check
check-deps: ok   check-tiers: ok   check-utils: ok   check-triplets: ok
check-todo: ok   check-replaces: ok   check-tidy: ok   check-otel-schema: ok
check-workspace: ok
```

**What is implemented and not exercised here, named rather than left to be
found.** The mapped sentinel a caller reads — `ErrConflict`, `ErrClosed`,
`ErrCursor`, `ErrRefused` — is the kernel's, and there is no exported reading of
a store's classification before S4, so these tests assert the outcome the store
**selected** by comparing against the kernel's own rendering of it
(`event.Failure(outcome, nil).Error()`). S4's `Repo` and S5's suite are what
assert the sentinel. `errStaleClaim` is unreachable while the claim mechanism
holds, which is the point of it. And the two windows of §UC-057 are half here: a
bare cancellation from the five operating methods travels as itself, and the
uncertain half has no producer in a store with no commit window — which is
exactly what §UC-044 says this store cannot certify.

---

### S4 — the caller seam: binding, repository, token, receipt, reader  `[x]`

**Phase 4 is executed and green.** The implementation is five files —
`event/token.go`, `binding.go`, `marker.go`, `repo.go`, `reader.go` — plus one
edit each to `event/errors.go` (`tooMany`, beside `tooLarge`, because
`tooLarge`'s message counts bytes and a batch counts changes) and to
`event/seal.go` (C10.1's sixth row).

**Fourteen tests ship with it**, in `event/repo_test.go`, `binding_test.go`,
`transaction_test.go`, `reader_test.go`, `outcomes_test.go`, `status_test.go`
and the fixture in `recordingstore_test.go`. Six are this section's checkpoint;
the other eight are carried from the phase-5 block or written because the branch
they cover had no other reader, and each keeps the planned name where the plan
has one:

| Test | Pins | Its control |
|---|---|---|
| `TestAFreshStreamLoadsAsZero` | §UC-009: no events is the zero state at version 0 and no refusal | the same load after two facts, folded at version 2 |
| `TestAppendRefusesInItsStatedOrder` | §D.5's six steps: each row breaks the step it names **and every step after it**, and asserts the earlier sentinel, zero `Append` calls, and that the token that came back is the one that went in | the same append with everything right, reaching the store once |
| `TestAForgedTokenIsRefusedBeforeAnyStatement` | §INV-015's first two defences: `At[account]{}` is `ErrKey` with and without changes and makes **zero** calls of any of the eight; a hand-built token with a legal key is `ErrWrongStore` | a token `Load` minted appends |
| `TestAnEmptyAppendChecksTheKeyAndNothingElse` | §INV-031 and GAP-53: zero store calls, the token back unchanged, and all six accessors answered — stream, `First` 0, `Last` the token's version, `Count` 0, the invalid authority | the one-change append beside it, whose receipt is not empty |
| `TestAnOverLongPageIsRefused` | C4's length half: a page one envelope over the store's own `StreamPage` is `ErrBackend`, with the zero state and the zero token | the same stream loading in pages of 2 |
| `TestAMisPagedStreamIsRefusedBeforeItIsFolded` | C4's shape half, three defects: reversed versions, a page that does not begin at `after + 1`, an envelope of another stream | the same stream loading clean |
| `TestALoadThatFailsMidStreamReturnsNothing` | §INV-006: a type the declaration does not know on the **second** of three events answers the zero state, not the accumulator | the clean load of the same stream at balance 7 |
| `TestBothDoorsCheckTheStore` | §INV-022 and §INV-043: seventeen dishonest stores, plus a nil store and a nil pointer inside one, refused at `Bind` **and** at `Read`; `ReadOnly` does not assert back to a `Store` | the honest store admitted at both doors, and an at-the-ceiling control per bound — a store publishing exactly `MaxPayloadBytes`, `MaxBatchCount`, `MaxKeyBytes` or `MaxPageCount` is bound and read, so a per-row drift of one in `admitLimits` is caught where a blanket `>` → `>=` already was |
| `TestABindWithNothingToBindIsRefused` | the widened check 1: a nil `*Aggregate` is `ErrDeclaration` and a nil `*Binding` is `ErrWrongStore`, neither a nil dereference | — |
| `TestOneFamilyNamesOneAggregate` | §UC-059: a second declaration of one family through one `Binding` is `ErrFamily` | the same declaration bound twice succeeds, and so does the second one through a second `Binding` |
| `TestWithinComposesForTwoBackings` | §UC-028 and GAP-73: two `Within` contexts chained for two backings, both stores proceeding, each authority its own | the mismatch **is** detected — a second transaction bound into the marked context is `ErrTransactionMismatch` at both `Load` and `Append`, without which "both proceed" passes against a kernel that never compares |
| `TestAConsumerReadsThroughPagesTheStorePublished` | §UC-036 and §INV-038: a walk tiles the log in the store's own pages, the first page survives the reads after it, the cursor is non-empty | the two page defects — over the bound, and positions that do not ascend — refused with `ErrBackend` |
| `TestAnOutcomeOutsideTheVocabularyTakesTheFailSafeDefault` | §INV-045 and §UC-060: seven store answers mapped at the append door, an unclassified one at **both** read doors — `Load` and `Reader.Next` — as `ErrBackend` rather than `ErrUncertain`, a retryable unclassified cause through `Next` reaching the caller as `crud.ErrUnavailable`, and a policy refusal whose cause is reachable and whose class does not travel | the append with nothing injected, and the page `Next` reads with nothing injected |
| `TestARequestClassRefusalRendersAClientStatusAndAHistoryClassOneDoesNot` | C5: `ErrKey`, `ErrEncode` and `ErrSample` render 400, `ErrTooLarge` 413, and `ErrPayload` (history) and `ErrCursor` (wiring) 500 | the two 500s are the controls, and every request-class member is driven rather than one |

**Verified by mutation.** Seventeen mutations were applied one at a time, the
suite run, and each file restored and `diff`ed byte-identical afterwards:
the page-length check removed (only `TestAnOverLongPageIsRefused` red — the
over-long page is otherwise dense, so the order check does not cover for it); the
version-order check neutered and the foreign-stream check removed (both arms of
`TestAMisPagedStreamIsRefusedBeforeItIsFolded`); the empty-append short circuit
moved above the key check; the bounds moved before the carried refusal; the
marker resolved innermost-first whatever its backing; `admit` skipping the
capabilities; `Read` skipping the door; `replay` answering the accumulator it
had; an empty receipt forgetting the version; a store's transaction refusal
falling back to autocommit; the key checked against the kernel ceiling rather
than the store's bound; `ReadOnly` handing the store back; a refusal answering
`At[S]{}`; and the reader's two page checks. Every one turned exactly the test
that names it red.

**Phase 5 wrote the ten the phase-4 block left**, plus one fuzz target, in
`event/replay_test.go` (new), `repo_test.go`, `transaction_test.go` and
`binding_test.go`. They cover the four functions phase 4 left below 100 % —
`Within` 62.5 %, `Authority` 75 %, `records` 83.3 %, `apply` 87.5 % — and the
branches nothing else reads:

| Test | Pins | Its control |
|---|---|---|
| `TestAStreamWithHistoryFoldsToItsCurrentState` | §UC-010, §UC-012, §UC-062 and §INV-008: four facts fold to the state at version 4 in the store's own pages; a revision-1 envelope is read by revision 1's codec and carried by the declared upcaster; a reload is the authority after the caller writes into the state it was handed; two decisions in one operation are only correct with the `Fold` line | the same stream at a page of 1 folds identically and takes 5 reads rather than 3; the equivalent revision-2-only stream folds to the same value; and the same operation **without** the `Fold` line writes twice and returns no error anywhere, which is the only thing that makes the line load-bearing |
| `TestOneLoadAndOneAppendMakeExactlyTheStoreCallsTheContractNames` | §INV-007, §INV-027 and C10.3: a per-method count over all eight, so a load is `Backing` 1 / `Transaction` 1 / `ReadStream` 2 and an append is `Backing` 1 / `Transaction` 1 / `Append` 1 — `Limits` and `Capabilities` stay at zero after `Bind`, and a conflict costs exactly one `Append` and **zero** `ReadStream` | two appends and a load counted together, so "exactly one" is a measurement and not a counter stuck at one |
| `TestOneAppendCarriesTwoIdenticalChanges` | §UC-058, §UC-035 and §UC-021: two identical changes are two events at consecutive versions in one `Append` call, fold to twice the amount, and the same fact in a second append makes a third | a one-change append answers a receipt of 1, and the token the first of two decisions returned conflicts once the stream has moved past it |
| `TestAnAppendWhoseActualBytesExceedTheResidentCeilingIsRefused` | `records`' two byte bounds: one payload over the store's `MaxPayload` and a batch of `MaxBatchCount` payloads holding 1 KiB more than `MaxResidentBytes`, both `ErrTooLarge` with zero store calls and the token unchanged | a payload of exactly the bound is written, and the same batch one payload shorter reaches the store — the arithmetic is asserted before the rows run, so a batch that is not actually over the ceiling fails the test rather than passing it |
| `TestAStoreWhoseProductExceedsTheResidentCeilingIsRefused` | §INV-022's product half at both doors: `StreamPage` and `MaxRead` one envelope over `ResidentPage(MaxPayload)` are `ErrWrongStore` at `Bind` **and** at `Read` | the at-the-product store is admitted at both doors, and the counts are chosen well inside `MaxPageCount` (1025 against 4096) so the refusal is the product's rather than the page ceiling's |
| `TestWithinAnswersTheStoresTransactionQuestion` | §UC-032, §UC-033, §UC-046 and C10.4: `ErrNoTransaction` having asked the store once; `ErrNoTransactionBinding` **without** asking it at all; `ErrAmbientNotTransaction` carrying the store's own answer as its cause; every refusal returning the context it was given; a transaction joined with no `Within` at all, and a non-transaction executor refused at both doors before any statement; and on a **closed** store `Within` succeeding while the `Load` and `Append` after it are `ErrClosed` | the marked context, through which a load and an append both proceed |
| `TestRepoAuthorityIsTheComparisonTwoSubsystemsUse` | §UC-030, §INV-028 and C10.5: the three answers a store gives about one context; two appends in one transaction and `Repo.Authority` answering one value; a closed store answering exactly what an open one would; and GAP-2's divergence — on §UC-049's crossed context `Authority` reports the transaction the store finds while `Load` refuses | two live transactions answer authorities that are **not** `Same`, so a store answering a constant fails; and the `Load` after the closed-store row is `ErrClosed`, so that row cannot pass against a store that never closed |
| `TestEveryHistoryClassRefusalIsRaisedByTheThingItNames` | §UC-013..§UC-017 and `apply`'s identifier rule: eight stored records, each refused by the party its sentinel names, each with the zero state and the zero token; a type name that is over-long, control-carrying or not UTF-8 is `ErrUnknownType` and **does not travel**, raw or escaped; a stored payload over the cap is `ErrPayload` and not `ErrTooLarge` (C5); a refusing upcaster hands back the application's own error | the stored fact this declaration reads folds at version 1, and a record whose payload is **exactly** `MaxPayload` folds — the read side of the bound the append side already controls, so `>` → `>=` at `apply` cannot make a legally written stream permanently unreadable |
| `TestAFoldPanicUnwindsOutOfLoad` | §UC-067 and §INV-017: a fold that writes into a nil map panics out of `Load` and is recovered into no sentinel of the vocabulary | the guarded fold over the same stream loads, and the **upcaster's** panic through the same door is recovered into `ErrUpcast` wrapping nothing — so the two callbacks are asserted to be treated differently rather than assumed to be |
| `TestAPanickingCodecBecomesTheRefusalItsErrorWouldHaveBeen` | the panic rule at all three codec methods: a panicking `Decode` out of `Load` is `ErrPayload` with no cause travelling, a panicking `Encode` is `ErrEncode` carried on the change and surfaced by `Append` with zero store calls, a panicking `CanEncode` is `ErrCodecType` out of `Declare` | each row is driven twice — the panicking codec and the same codec **returning** that error — so a kernel that recovered nothing and one that recovered into the wrong sentinel both fail; the shipped codec is the third control |
| `FuzzALoadFoldsAStoredStreamOrRefusesItWhole` | §UC-015 and §INV-006 universally quantified over a store's own bytes: for any wire type name, revision and payload behind one legal envelope, `Load` either folds — twice, to the same value — or refuses with one of `ErrUnknownType`, `ErrRevision`, `ErrPayload`, `ErrUpcast` and hands back the zero state and the zero token | 13 seeds run on every `make unit`; a 45 s campaign at 5.2 M executions found nothing |

**Verified by mutation, fifteen runs and sixteen edits** — the last is a pair,
because `Load` zeroes what the fold loop returns and both lines have to go for the
property to be falsifiable. Each was applied on its own, with the file restored
and its sha256 compared afterwards: the current codec used for an older
revision; the page loop stopping after the first page; a retry around the store's
`Append`; two equal changes collapsed into one record; each of `records`' two byte
bounds removed; the page-times-payload product not checked at a door; `Within`
not reading the capability, and admitting a context with no transaction;
`Authority` answering the marker's question instead of the store's; a stored type
name rendered before it is checked; the recorded payload cap removed; a fold's
panic recovered into `ErrUpcast`; the recover in `decodeWith` removed; and a load
that hands back what it had folded. Each turned exactly the test that names it
red, and only that test.

**Two comment repairs went with them**, both raised by the S4 implementation
review and both about a public contract sentence that the phase-5 tests now
assert the opposite of. GAP-1: `Repo.Append`'s GoDoc claimed *every* refusal
means nothing reached the backing, which is false for every outcome the store
answers once the batch has been issued and is the exact inference §UC-034 exists
to forbid — it now scopes that reason to the six pre-store steps and names what
an outcome says instead. GAP-2:
`Repo.Authority` says in its own words that it reports what is bound and never
whether an append may be written through it, which is the divergence
`TestRepoAuthorityIsTheComparisonTwoSubsystemsUse` pins on §UC-049's context.
No behaviour changed.

```
$ test "$(go test -list '^(…the twenty-six names…)$' ./event/ | grep -c '^Test')" = 26
26
$ go test -race -count=1 ./event/...                 # twice, no flake
ok  	github.com/frostgrove/vv/event	1.270s
ok  	github.com/frostgrove/vv/event/eventmemory	1.026s
$ go test -run '^$' -fuzz FuzzALoadFoldsAStoredStreamOrRefusesItWhole -fuzztime 45s ./event/
elapsed: 45s, execs: 5213898, new interesting: 459   PASS
$ go vet ./event/...   # clean      $ gofmt -l .   # silent
$ make unit            # every module, no FAIL
$ make check
check-deps: ok   check-tiers: ok   check-utils: ok   check-triplets: ok
check-todo: ok   check-replaces: ok   check-tidy: ok   check-otel-schema: ok
check-workspace: ok
```

```
$ go build ./...                                    # clean
$ go vet ./event/...                                # clean
$ gofmt -l .                                        # silent, whole tree
$ go test -race -count=1 ./event/...
ok  	github.com/frostgrove/vv/event	1.250s
ok  	github.com/frostgrove/vv/event/eventmemory	1.024s

$ go test -race -count=1 -v -run 'TestAFreshStreamLoadsAsZero|TestAppendRefusesInItsStatedOrder|TestAForgedTokenIsRefusedBeforeAnyStatement|TestWithinComposesForTwoBackings|TestAnOverLongPageIsRefused|TestBothDoorsCheckTheStore' ./event/
=== RUN   TestBothDoorsCheckTheStore
--- PASS: TestBothDoorsCheckTheStore (0.00s)
=== RUN   TestAFreshStreamLoadsAsZero
--- PASS: TestAFreshStreamLoadsAsZero (0.00s)
=== RUN   TestAppendRefusesInItsStatedOrder
--- PASS: TestAppendRefusesInItsStatedOrder (0.00s)
=== RUN   TestAForgedTokenIsRefusedBeforeAnyStatement
--- PASS: TestAForgedTokenIsRefusedBeforeAnyStatement (0.00s)
=== RUN   TestAnOverLongPageIsRefused
--- PASS: TestAnOverLongPageIsRefused (0.00s)
=== RUN   TestWithinComposesForTwoBackings
--- PASS: TestWithinComposesForTwoBackings (0.00s)
PASS
ok  	github.com/frostgrove/vv/event	1.015s

$ go test -coverprofile ./event/                    # the five new files
binding.go 100 % but Bind's own two nil rows, marker.go 100 %, reader.go 100 %,
token.go 100 %, repo.go 100 % but the branches the tests phase owns above

$ make unit                                          # every module, no FAIL
$ make check
check-deps: ok   check-tiers: ok   check-utils: ok   check-triplets: ok
check-todo: ok   check-replaces: ok   check-tidy: ok   check-otel-schema: ok
check-workspace: ok
```

**Round 1 of the test review closed six of its eight findings** — the one
`[high][immediate]`, the one `[high][deferred]`, both `[medium]` boundary rows
and both `[low]` ones. Two tests are new and four existing ones grew a control;
the mutation that proves each is beside it:

| Finding | What closed it | The mutation that turns it red |
|---|---|---|
| GAP-1 `Reader.Next`'s fail-safe default is asserted by nothing | `recordingStore` gains `failWhole`, the `ReadAll` twin of `failRead`, and `TestAnOutcomeOutsideTheVocabularyTakesTheFailSafeDefault` drives the second read door: an unclassified store error through `Next` is `ErrBackend` and not `ErrUncertain`, a retryable one carries `crud.ErrUnavailable`, and the page `Next` reads with nothing injected is the control | `refuseRead` → `refuseAppend` at `reader.go:51`; and `backendRefusal` never promoting a retryable cause |
| GAP-2 §UC-055's `Bind` cost has no test | `TestABindInterrogatesNoCodecAndMutatesNoDeclaration` | a fact-table walk inserted into `Bind`; and `Bind` writing one entry into the fold table |
| GAP-3 the recorded-payload cap has no at-the-bound control | a control record of exactly `MaxPayload` bytes in `TestEveryHistoryClassRefusalIsRaisedByTheThingItNames`, with its own length asserted first | `>` → `>=` at `repo.go:265` |
| GAP-4 four of the five kernel ceilings are tested from one side only | an at-the-ceiling control per bound in `TestBothDoorsCheckTheStore`, the page rows paired with a payload bound that still admits the page | each of the five `admitLimits` ceilings lowered by one, in turn — each turns exactly its own row red |
| GAP-7 three tests sit outside the count clause | the phase-5 `-list` clause names twenty-six | — |
| GAP-8 `streamOf`'s mapper-refusal branch is unreachable | `TestTheRenderedKeyIsCheckedBeforeTheStoresBound` | `streamOf` swallowing `locate`'s refusal; and `checkKey` reading `MaxKeyBytes` rather than the store's bound |

GAP-5 (the seam is proven against the fixture and no real store) and GAP-6 (a
`*Repo` shared by many goroutines) are S5's by the finding's own assignment and
are carried in `## Debt`.

**Delivers** load / decide / append / read, the transaction seam, every bound,
every door check and every refusal a caller can reach.

**Files** `event/binding.go`, `repo.go`, `marker.go`, `token.go`, `reader.go`;
`event/status_test.go` — the one test file in this phase that imports
`port/porthttp`, which is a **test** import of `event` and therefore invisible to
`go list -deps` without `-test` (C5); and `event/replay_test.go`, phase 5's own
file, which holds what a load does with a store's bytes.

**Realises** `Open`/`Binding`/`Bind`, `Repo` and its four methods, `At`, `Commit`,
`ReadOnly`/`Read`/`Reader`.

**Carried gaps** C4 (the kernel verifies a page longer than the store's own
bound, and its shape: first version `after + 1`, each subsequent one higher, this
stream), C5 (the rendering half — a request-class refusal reaches a client status
and a history-class one does not), C10.3 (`Append`'s and `Load`'s fixed orders,
where the empty-append short circuit sits, and that `Limits()` is retained so
"zero store calls" is literal), C10.4 and C10.5 (`Within` and `Authority` on a
closed store).

**Decides** GAP-53: an empty `Commit`'s accessors, all six — and that §UC-030's
cross-subsystem comparison uses `Repo.Authority(ctx)` rather than the receipt, so
a no-op decision does not produce a false negative. **Also decides** that `Load`
stops on a short page and returns the zero state and the zero token with any
non-nil error, and that a codec's panic is recovered into the sentinel its error
would have produced while a fold's, a mapper's and a store's are not.

**Covers** UC-002 (the `ErrWrongStream` half), UC-006 (`Open`/`Bind`), UC-007,
UC-009, UC-010, UC-011, UC-012 (the load), UC-013, UC-014, UC-015, UC-016,
UC-017, UC-018, UC-019, UC-020, UC-021, UC-024 (the surfacing), UC-025, UC-027,
UC-028, UC-030, UC-031, UC-032, UC-033, UC-034, UC-035 (the absence), UC-036,
UC-046, UC-049, UC-051 (the `Load`/`Append` doors), UC-055 (`Bind` is O(1)),
UC-057 (the surfacing), UC-058, UC-059, UC-060 (the surfacing), UC-061, UC-062,
UC-064 (the `Append` half), UC-066, UC-067 (out of `Load`); INV-006,
INV-007 (the recording store), INV-008, INV-015, INV-018 (the verification),
INV-019 (the caller half), INV-020 (the runtime half), INV-022,
INV-027 (the recording store), INV-031, INV-039, INV-040, INV-041, INV-043,
INV-044 (the `Append` half).

**Degradation** every store failure arrives as one of the map's rows; an
unclassified error is `ErrUncertain` from `Append` and `ErrBackend` from a read;
a store that never stated its bounds is refused at both doors before any work.

**Budget** `Append` pays one key scan of at most `MaxKey` bytes, one struct
comparison per change, and one `Transaction(ctx)` lookup — a context read, a type
assertion and a struct fill, never a statement. `Load` pays **zero** kernel clones
(§D.13).

**Checkpoint S4 — phase 4 (implementation)**

```
go build ./... && go vet ./event/... && test -z "$(gofmt -l event)" && go test -race -count=1 ./event/...
```

**Checkpoint S4 — phase 5 (tests)**

```
test "$(go test -list '^(TestAFreshStreamLoadsAsZero|TestAStreamWithHistoryFoldsToItsCurrentState|TestAppendRefusesInItsStatedOrder|TestAForgedTokenIsRefusedBeforeAnyStatement|TestTheRenderedKeyIsCheckedBeforeTheStoresBound|TestAnEmptyAppendChecksTheKeyAndNothingElse|TestOneLoadAndOneAppendMakeExactlyTheStoreCallsTheContractNames|TestWithinComposesForTwoBackings|TestWithinAnswersTheStoresTransactionQuestion|TestRepoAuthorityIsTheComparisonTwoSubsystemsUse|TestAMisPagedStreamIsRefusedBeforeItIsFolded|TestAnOverLongPageIsRefused|TestALoadThatFailsMidStreamReturnsNothing|TestEveryHistoryClassRefusalIsRaisedByTheThingItNames|TestBothDoorsCheckTheStore|TestABindWithNothingToBindIsRefused|TestABindInterrogatesNoCodecAndMutatesNoDeclaration|TestOneFamilyNamesOneAggregate|TestAStoreWhoseProductExceedsTheResidentCeilingIsRefused|TestAnAppendWhoseActualBytesExceedTheResidentCeilingIsRefused|TestAnOutcomeOutsideTheVocabularyTakesTheFailSafeDefault|TestOneAppendCarriesTwoIdenticalChanges|TestAConsumerReadsThroughPagesTheStorePublished|TestAFoldPanicUnwindsOutOfLoad|TestAPanickingCodecBecomesTheRefusalItsErrorWouldHaveBeen|TestARequestClassRefusalRendersAClientStatusAndAHistoryClassOneDoesNot)$' ./event/ | grep -c '^Test')" = 26 && go test -race -count=1 -v -run '^(TestAFreshStreamLoadsAsZero|TestAStreamWithHistoryFoldsToItsCurrentState|TestAppendRefusesInItsStatedOrder|TestAForgedTokenIsRefusedBeforeAnyStatement|TestTheRenderedKeyIsCheckedBeforeTheStoresBound|TestAnEmptyAppendChecksTheKeyAndNothingElse|TestOneLoadAndOneAppendMakeExactlyTheStoreCallsTheContractNames|TestWithinComposesForTwoBackings|TestWithinAnswersTheStoresTransactionQuestion|TestRepoAuthorityIsTheComparisonTwoSubsystemsUse|TestAMisPagedStreamIsRefusedBeforeItIsFolded|TestAnOverLongPageIsRefused|TestALoadThatFailsMidStreamReturnsNothing|TestEveryHistoryClassRefusalIsRaisedByTheThingItNames|TestBothDoorsCheckTheStore|TestABindWithNothingToBindIsRefused|TestABindInterrogatesNoCodecAndMutatesNoDeclaration|TestOneFamilyNamesOneAggregate|TestAStoreWhoseProductExceedsTheResidentCeilingIsRefused|TestAnAppendWhoseActualBytesExceedTheResidentCeilingIsRefused|TestAnOutcomeOutsideTheVocabularyTakesTheFailSafeDefault|TestOneAppendCarriesTwoIdenticalChanges|TestAConsumerReadsThroughPagesTheStorePublished|TestAFoldPanicUnwindsOutOfLoad|TestAPanickingCodecBecomesTheRefusalItsErrorWouldHaveBeen|TestARequestClassRefusalRendersAClientStatusAndAHistoryClassOneDoesNot)$' ./event/ && go test -race -count=1 ./event/...
```

---

### S5 — `event/eventtest`: the conformance suite and its self-falsification  `[x]`

**Delivers** the exported suite, its twenty sections, its three fixture stores and
its eighteen self-falsification defects, plus the three runnable proxies. This is the
section that makes every earlier claim evidence rather than prose, and it is the
artefact phase 2's `eventpg` runs **verbatim**.

**Files** `event/eventtest/doc.go`, `suite.go`, `probe.go` (round 2's split of
`suite.go`: the value a section holds and everything it does to a store),
`inventory.go` (the twenty
section names and the runner that iterates them), `declaration.go` (the aggregate
the suite declares for itself, its two codecs and its folds), `stores.go` (the
decorators a section builds around the store under test), `sections_write.go`,
`sections_read.go`, `sections_resumption.go` (round 3's split: `resumption` and
the three helpers only it uses, because the round-3 clause took
`sections_read.go` to 421 lines), `sections_lifecycle.go`, `sections_ownership.go`,
`sections_transactions.go`, `defects.go`, `proxies.go`, `report.go`;
`event/eventtest/export_test.go` (`package eventtest`, the seam the suite's own
tests reach the inventories and the verdicts by);
`event/eventtest/inventory_test.go` (the inventory assertion and its
shortened-inventory control);
`event/eventtest/fixtures_test.go`, `wrappers_test.go` (round 3: the five store
shapes the constancy clauses are falsified with, each internally consistent and
each breaking nothing a section reads), `suite_test.go`, `defects_test.go`,
`proxies_test.go`, `fuzz_test.go` (round 3: the one property of the three proxies
that is universally quantified over caller data) — the fixture stores and the
self-checks, in the suite's **own test package**, a third package, which is
§INV-019's compile-time proof; `event/eventmemory/conformance_test.go`.

**Five file-level changes to the list above, made while writing it and recorded
here rather than left as a difference.** (1) `declaration.go` and `stores.go` are
new: the suite's own aggregate and the seven decorators its sections build are
neither an inventory nor a section, and putting them in `suite.go` would have made
one 700-line file out of three small ones. (2) `inventory_test.go` is
`package eventtest_test` rather than `package eventtest`, and the internal seam is
`export_test.go` instead. The plan had the inventory assertion reach the
unexported runner by living in `package eventtest`, and the three fixture stores
live in `package eventtest_test` — two packages that cannot see each other, so as
written the test could not construct the store it runs the suite against. The
standard-library `export_test.go` idiom resolves it in the direction that keeps
§INV-019 intact: every fixture stays in the third package, and the unexported
runner, the section inventory and the defect inventory are re-exported to it by a
**test** file, which is on nothing's surface. (3) **`event/store.go` was edited
during S5 and is not on the list** (GAP-13): the `Store.Transaction` contract
sentence changed from *"A closed store answers the second"* to *"A closed store
answers exactly what an open one would and never reports closure here, because
this method writes and reads nothing and what the context carries did not change
when the store was closed"*. It is a comment and nothing else, it is the clause
`lifecycleSection.closedWithin` asserts, and it aligns the sentence with what
C10 row 5 had already decided — but it is a **port contract sentence** phase 2
implements against, so it is a recorded plan change rather than a silent one.
**Round 2 (GAP-23) withdraws what that note claimed next about `event/repo.go`**:
the whole of `event/` except `store.go` is **untracked**, so `git diff` reports
nothing for `repo.go` whatever its content and the "no diff" it cited could not
have failed. What holds `repo.go`'s content is S4's own suite, unchanged and
green — `repo_test.go`, `replay_test.go` and `transaction_test.go` run against it
on every `go test ./event/` — and no S5 change to it is recorded because none was
made; the mtime is the S4-era write of a file S5 never opened. The claim cannot
be made from `git` at all until there is a commit to diff against.
(4) **`event/store.go` was edited a second time, in round 2** (GAP-19): `Store`'s
doc gains *"All eight are safe for concurrent use. One store value is bound once
and the `Repo` over it is one handle every request goroutine shares, so a store
that needs a lock takes its own."* That is the obligation `concurrency` runs
eight goroutines against and the one thing the suite asserted that the contract
did not state. A comment and nothing else.
(5) **`suite.go` was split in round 2**: at 473 lines it was over
`architecture.md`'s 400, so the probe and everything it does to a store moved to
`event/eventtest/probe.go` (259 lines), leaving the extension point, the runner
and the admission in `suite.go` (224). No behaviour and no symbol changed.

**Realises** `Factory`, `Tx`, `Run`, `RoundTrip`, `Keys`, `Families`.

**Carried gaps** C1 (`RoundTrip`'s proxy is reported), C2 (the refusing-decorator
case), C3 (`cancellation` asserts both halves in both windows), C4 (the `bounds`
case, the illegal-product store, the over-long-page defect, and the two mis-paging
defects with the at-the-bound control), C7 (the one-byte `append` case), C8 (one
inventory in `defects.go`, eighteen defects after round 3, a test computes the coverage),
C9 (the policed half), C10.2, C10.4.

**Round 3 added three clauses the suite had none for, each with its own defect row
and its own control.** (1) **The recorded instant** (GAP-24): §D.6 puts it in the
store's column and nothing in the suite read `Envelope.RecordedAt` at all, so a
store whose select list drops the column reported the same twenty verdicts as a
correct one — the instant orders nothing (§INV-009), so no other section can see
it. `dense versions` now asserts that every envelope this run appended carries a
non-zero one and that the read after answers the same one, which is what separates
a stored instant from one minted when the row is read. (2) **The constancy of
`Capabilities`, `Limits` and `Backing` within one store value** (GAP-26), in
`binding`. (3) **A cursor this store cannot parse** (GAP-25), in `resumption`,
through the new `Factory.Unparsable` and reported *not certified* without it.

**Also delivers the four `transactions` cases GAP-112 and GAP-126 forced**, each
with its control: two appends in one transaction; a `Load` after an `Append` in
one; two concurrent transactions on one stream; and the **aftermath** case — a
conflict inside a bound transaction, then the recovery the contract states (roll
back, begin again, reload, append), with a reload inside the same transaction
after a *successful* append as its control. And `Tx.Commit`'s error contract,
which is that the suite never asserts an `event` sentinel on one.

**No case may exercise a freedom the contract only permits.** After any refusal
but `ErrClosed` and `ErrRefused`, the suite issues nothing further on that bound
transaction, because `eventmemory` survives it and `eventpg` cannot
(`event/store.go`). That rule is what stops the suite from certifying a promise
one store invented.

**Covers** UC-006 (`binding`), UC-011 (`stream paging`), UC-018
(`cancellation`), UC-019 (`expected version`), UC-022, UC-025 (`bounds`),
UC-027, UC-028, UC-029, UC-034 (the unknown outcome, driven through `Fail`),
UC-036 (`global paging`), UC-037, UC-038, UC-039, UC-040 (the suite is what a
store's own package runs), UC-042 (`eventtest.RoundTrip`), UC-043, UC-044,
UC-045, UC-047, UC-048, UC-051 (`stream identity`), UC-053 (both halves after
round 3: a foreign cursor in `resumption`, and one this store cannot parse
through `Factory.Unparsable`), UC-057, UC-059
(`binding`), UC-060, UC-061 (`payload ownership`), UC-066; INV-001 (the
append-only walk), INV-002, INV-003, INV-009 (including its third clause, the
store-assigned instant, in `dense versions` — round 3), INV-019 (the compile-time half),
INV-021 (the store-side hand-offs 3, 4, 6, 7), INV-026 (`refusal classes`),
INV-029, INV-030, INV-032, INV-033, INV-034, INV-035, INV-036, INV-038, INV-039,
INV-040, INV-041, INV-042 (`payload ownership`'s inbound half), INV-043, INV-044,
INV-045.

**Anti-vacuity, five rules and they are the section's point.** A claimed
capability whose hook is missing **fails**. A run in which every claimed section
was skipped **fails**. An `Unstated` capability is refused at both doors. Every
section carries a control that a refuse-everything store fails. And — the rule
the other four do not reach, because `go test -list` cannot see a subtest —
**every one of the twenty inventory sections is reported exactly once with one of
the three words**, asserted by `TestEverySectionInTheInventoryWasReported` with a
one-section-shorter run as its control.

**Degradation** a store that cannot produce an outcome answers `false` from
`Fail` and the section is reported **not certified** — never passed.

**Phase 4 and phase 5 are executed and green.** Fourteen files of suite and six of
test, plus `event/eventmemory/conformance_test.go`. The twenty sections are
written, all fifteen defects are detected, and the two stores that exist run the
whole of it: `eventmemory` reports eighteen *passed* and the two *not certified*
rows §UC-044 predicts of it by name — `durability`, because it declares
`Persistence: Unsupported`, and `store failure classification`, because it has no
commit window and its `Fail` hook therefore answers **false** for `Unconfirmed`.

**Twenty tests and one fuzz target ship with it** — ten as written, four that
round 1 of the implementation review added, four more from round 2 and four from
round 3, each with a control that was run; three of them are the checkpoint's:

| Test | Pins | Its control |
|---|---|---|
| `TestTheMemoryStoreSatisfiesTheContract` and `TestTheMemoryStoreSatisfiesTheContractAtNarrowerLimits` (`event/eventmemory`, the second round 2's) | the whole contract against the store that exists, and §UC-040's shape — a store's own package runs the suite through the exported API and nothing else; the second publishes `MaxKey` 40, `StreamPage` 4, `MaxBatch` 2 and `MaxRead` 3, all legal and a hundredth of the defaults | the `-v` section list: every section named, none skipped, two of them *not certified* with the reason — and the second test's twenty verdicts are the first's, verdict for verdict |
| `TestTheSuiteStillDetectsEveryDefectItWasBuiltToDetect` | C8: every defect in `defects.go` fails the section named for it; no two defects share a name; every section a defect names exists | the same store **without** the defect is asserted to **pass** that same section, so a failure is the defect's and not the fixture's |
| `TestEverySectionInTheInventoryWasReported` | every one of the twenty is reported exactly once with one of the three words — the rule `go test -list` cannot reach, because a section is a subtest | a run over an inventory one section shorter must **fail** the same assertion, so it proves the sections exist rather than that a list was iterated |
| `TestATrivialStoreNeedsNoInternalAccess` | §INV-019's compile-time half: a complete store built in a third package out of the exported vocabulary alone, run through `Run` | the demonstration below |
| `TestARunThatCertifiedNothingFails` | anti-vacuity rule 2: three gated sections against a store that claims none of them certify nothing, and that is what `Run` fails on | the whole inventory over the same store certifies fifteen, so the count is a measurement and not a zero |
| `TestEverySectionFailsAgainstAStoreThatRefusesEverything` | anti-vacuity rule 4, **computed rather than remembered**: all twenty sections are reported *failed* against a store that is honest about its bounds and its capabilities and refuses every operation | a section that passes there is a section carrying no control, and the run names it |
| `TestAnApplicationRunsTheThreeProxiesOverItsOwnDeclaration` and `TestTheProxiesCompareSomethingThatCanDiffer` | `RoundTrip`, `Keys` and `Families` over a declaration of an application's own | the collision `Keys` looks for is asserted to **be** there under a concatenating mapper, and `Compose` is asserted to render it differently — without which the proxy compares two things that can never differ |
| `TestAClaimedCapabilityWithAMissingHookIsRefused` | anti-vacuity rules 1 and 3 at the door: a claimed capability with no hook, and an `Unstated` one, are both refused before a section runs | the store that claims both and supplies both hooks is admitted |
| `TestATransactionCapableStoreSatisfiesTheContract` | the staging fixture — the base the eleven decorator defects wrap, and the `transactions` control — passes nineteen of twenty | `durability` is its one *not certified*, so it is not passing by claiming nothing |
| `TestEachProxyReportsTheThingItExistsToFind` (GAP-8) | the branch each of the three proxies exists for, driven: a colliding key pair, an identity that renders no key, one identity, no aggregate, a fact whose sample does not survive its codec, no fact, two declarations naming one family, a nil declaration, no declarations | the three cases beside it that must report **nothing** — the same fact with a sample that does survive, three identities `Compose` separates, two declarations of two families |
| `TestASectionThatNeverReturnedIsNotReportedPassed` (GAP-7) | the zero value of a verdict is not *passed*: a factory hook that leaves the subtest through `runtime.Goexit` leaves the row the runner initialised, and that row must not be the one word that means the store is correct | putting `passed` back at `iota` turns it red, and `Run` reports the row rather than silently counting it toward the certifications an empty run is refused for |
| `TestEveryStoreCallASectionMakesCarriesADeadline` (GAP-6) | the four doors that take a context, watched through a decorator over the staging fixture: 548 store calls in a whole run and none of them without a deadline | one section put back on `context.Background()` reports 2 of 548, so the count is a measurement |
| `TestASectionWalksItsOwnTailOfALogSomebodyElseFilled` (GAP-3) | a store whose log already holds 6 000 events this run did not write — more than the 1 024 pages at the fixture's `MaxRead` of five that the walk used to give up after — still reaches a real verdict in every section, and certifies the same number as the same store with an empty log | the pre-change walk (from `""`, bounded at 1 024 pages) reports `global order`, `conservation`, `global paging` and `resumption` **failed**, naming the suite's own budget as the store's defect |
| `TestEverySectionIsCertifiedAtTheNarrowestLimitsAStoreMayPublish` (GAP-17) | every count a section writes is derived from what the store publishes: the whole suite over the staging fixture at `MaxBatch` 1, `StreamPage` 1, `MaxRead` 1 and `MaxKey` 40 reports **no section failed** | the same store at the fixture's ordinary numbers certifies the same count, so the run is comparing two measurements; and the pre-change literals put it back — `widest := widestNarrowing` reports `stream paging` failed, a fixed 16-byte identity reports `stream identity`, `resumption`, `lifecycle` and `transactions` failed, which is GAP-17's own table reproduced |
| `TestASectionReachesAVerdictOverALogSomebodyElseIsStillWritingTo` (GAP-18) | a store whose log grows by one foreign event on **every** `ReadAll`: no section is *failed* and the run certifies what the quiet store certifies, because what a section walks is bounded by what it wrote and the factory answers where the log ended | with `walkLog` unbounded again, `global order`, `conservation`, `global paging` and `resumption` report *"reading the log answered context deadline exceeded"*; with `Factory.Tail` removed, the same four report the refusal that names the hook |
| `TestAStoreThatMintsTheRecordedInstantWhenAnEventIsReadIsNotCertified` (GAP-24) | the subtler of the two stores that lose the instant: one with no column at all, filling the field as it scans the row, so one event answers a different instant to every reader. The store that answers the **zero** time is the defect inventory's sixteenth row | the same store recording its own instants is asserted **passed** for `dense versions`; and removing `probe.instants` reports both the defect and this test's store *passed*, which is the whole clause in one mutation |
| `TestAStoreWhoseCapabilitiesOrBackingChangeUnderOneValueIsNotCertified` (GAP-26) | the two halves of the constancy clause with no defect row: a store claiming transactions on one call and not on the next, and one naming a fresh backing on every call. Both are internally consistent and pass every other section | the same store answering one value throughout is *passed*; neutralising `probe.unchanged`'s second reading reports all three — these two and the seventeenth defect — *passed* |
| `TestAFactoryWhoseLaterStoresPublishSomethingElseIsRefused` (GAP-28) | the **factory's** obligation rather than a store's, and the half `probe.store` did not check: a second store publishing a wider page, and one claiming fewer capabilities. Neither store is internally wrong, so only a comparison between two of one factory's values can see it | the same factory building one kind throughout is *passed*; removing the `Capabilities()` comparison reports the second *passed* |
| `TestACursorAStoreCannotParseIsRefusedRatherThanReadFromTheBeginning` (GAP-25) | the store's own cursor parser, which no cursor the suite can mint reaches: a store that starts its walk at the beginning of the log for a checkpoint it could not parse is *failed*, and a factory answering no such cursor is **not certified** — the third word rather than silence | the same store refusing what it cannot parse is *passed*; removing `probe.unparsableCursor` reports the defect *passed* and the missing-hook run *passed* rather than *not certified* |
| `FuzzKeysReportsACollisionExactlyWhenTwoIdentitiesRenderOneKey` (round 3) | the one property of the three proxies that is universally quantified over caller data, as an **equivalence**: `Keys` answers an error exactly when the two identities do not render two distinct legal keys. A proxy silent on a collision certifies an application that has one history for two instances; one that reports a collision that is not there refuses a correct mapper | both arms were driven by mutation — deleting the duplicate-key branch turns the seeds red on the first, widening it to "any second identity" turns them red on the second. 45 s of `-fuzz`, 1.78 M execs, no failure |
| `TestAStoreThatKeepsWhatItWroteIsCertifiedForDurability` (GAP-20) | the one section no in-tree store could demonstrate: a fixture claiming `Persistence: Supported` whose second value over one backing reads what the first wrote is asserted **passed**, and the fifteenth defect — the same store answering a second value with nothing in it — **failed** | widening the balance check to admit zero reports the defect *passed*, so the section's own assertion is what the pass rests on; coverage of `durabilitySection` moves 35.0 % → **85.0 %** |

**Two clauses of the `Covers` list above are asserted in sections whose D.9 row
does not name them**, because the section that owns the data is the one that can
see them: §UC-038's *per-stream order is a subsequence of global order* is in
`global order` beside the ascension check, over a walk that is asserted to have
reached at least two streams; and §UC-053's *a foreign cursor is refused* is in
`resumption`, where a cursor minted over one backing is presented to a second
store and must answer `ErrCursor` — reported *not certified* on a factory whose
second store shares the first's backing, since there is then no elsewhere to
present it to.

**The three fixtures are two types and four values**, and the difference from
[SPEC]'s wording is deliberate: the trivial store and the admit-everything store
are one type with one rule dropped, exactly as §UC-045 describes them, and the
leaky-rollback store is a second, transaction-capable type — but its **correct**
instantiation is a fourth value, because the ten decorator defects need a base
that is not broken and the `transactions` section needs a control that is not the
defect. A fifth fixture, the refuse-everything store, exists for anti-vacuity
rule 4 and is not a defect: it fails every section on purpose.

**Round 2 adds four more values of those same two types, and no third type.**
The prefilled store (round 1) and three from this round: the **narrow** one, which
is the staging store publishing `MaxBatch` 1, `StreamPage` 1, `MaxRead` 1 and
`MaxKey` 40; the **persistent** one, whose every `New` is another value over one
log and which is the only store in the tree claiming `Persistence: Supported` —
with its defective instantiation answering a second value that holds nothing; and
the **busy** one, whose log grows by an event of somebody else's on every
`ReadAll` and whose factory answers where the log ended. Each exists because a
property of a *store a consumer may write* was otherwise demonstrated by nothing
here: legal numbers other than the fixture's, a backing that outlives a value,
and a log this run does not own.

**Two [SPEC] §D.9 clauses are not expressible and are replaced rather than
dropped.** (1) `global paging`'s *the same walk at `MaxRead` 1* cannot be built by
the suite: narrowing a page means returning fewer envelopes than the store did,
and the cursor beside them is the **store's**, minted for the whole page — a suite
that truncated the page and kept the cursor would be building §UC-045's third
defect rather than a control. The section asserts the property it can: the walk
tiles the run's own events in position order, and a walk **split across two
readers at a persisted cursor** returns the identical sequence, which is the same
claim about page shape without minting a cursor nobody minted. (2) `resumption`'s
*a writer committing out of position order* needs a transaction to hold one open,
so on a store with none it is reported through the third word — the section runs
and the run says which half was not certified.

**Verified by mutation, and each was restored and its sha256 compared
afterwards.** `Envelope.RecordedAt` unexported: `event/eventtest_test` fails to
compile at `fixtures_test.go:268` — §INV-019's compile-time proof demonstrated
rather than claimed, and `eventmemory` fails beside it, which is the same proof
one package out. The kernel's page-length check removed from
`Repo.checkPage`: `TestTheSuiteStillDetectsEveryDefectItWasBuiltToDetect` turns
red with *the suite no longer detects a store that publishes a stream page
shorter than the page it returns: its bounds section was reported "passed"* —
which is the whole chain in one line, from a kernel branch to the defect
inventory to the section that names it. Beyond those two, the defect inventory
**is** a fifteen-mutation suite that runs on every `make unit`, and
`TestEverySectionFailsAgainstAStoreThatRefusesEverything` is a twentieth of one:
between them every section is asserted to fail against something.

**Round 1 of the implementation review closed nine findings, and each of the six
that changed behaviour was verified by putting the defect back and watching the
new assertion turn red** — every mutation restored afterwards and the tree
rebuilt clean.

| Finding | What changed | The mutation, and what it reported |
|---|---|---|
| GAP-1 `[high]` | `payload ownership`'s C7 case rewritten as `probe.spilled`; thirteenth defect `packedPages` | remove the `spilled` call: *"the suite no longer detects a store that hands out sub-slices of one fresh page buffer without cutting their capacity: its payload ownership section was reported `passed`"* |
| GAP-2 `[high]` | `Factory.New`'s and `Factory.Begin`'s doc rewritten to what the code does; `foreignCursor` and `durability` say why they build a store mid-section | doc-only; the three sections it misled are named above |
| GAP-3 `[high]` | the two bare `1024`s gone: `drain` ends on a short page and refuses an over-long one, `walkLog` ends on an empty page and refuses a page beside its own cursor, and every walking section mints its start cursor with `probe.tail` before it writes; the run identity widened 4 bytes → 16 | put the bounded from-`""` walk back: four sections *failed* on the 6 000-event fixture with *"a walk over this store did not reach the end of the log in 1024 pages"* |
| GAP-4 `[medium]` | `injectedAtRead` reads through the value the append door writes through, so the `wrapping` iteration exercises `wrapping.ReadAll` | make `wrapping.ReadAll` drop the classification: *"[outcome bad cursor] injected at the read door through a decorator that wraps the store's own error answered event: the store failed where the kernel maps that classification to event: this cursor was not minted over this backing"* |
| GAP-5 `[medium]` | `probe.begin` registers the rollback on the section's `t.Cleanup`; the staging fixture reports any transaction still unresolved when a section ends | remove that one line: *"1 transaction(s) this section began were still unresolved when it ended"*, from `lifecycle` |
| GAP-6 `[medium]` | every section runs under `probe.context()`, a 20-second window; `contended` reordered so the loser issues nothing until the winner has committed, and its comment now states what the code does | one section back on `context.Background()`: *"2 of 548 store calls a run made carried no deadline"* |
| GAP-7 `[medium]` | `word`'s zero value is `unreported`, which `Run` reports and `certified` does not count | `passed word = iota` restored: *"a section whose factory left the run through the goroutine that section was dispatched on was reported passed"* |
| GAP-8 `[medium]` | the three proxies answer an error and the exported wrapper fatals on it; `roundTrips`, `keysRender` and `familiesDiffer` are 100 % covered | driven directly through all nine reporting branches, with three silent controls beside them |
| GAP-9 `[medium]` | `door` and `through` are a function and a decorator-builder on `failureCase`; `refusal classes` asserts `err.Error() == policy.is.Error()` rather than a `"quota"` substring | the substring assertion failed **open** and the equality one does not |

**Round 2 closed seven more, and the two the reviewer graded `[critical]` and
`[high]` were both demonstrated before and after.**

| Finding | What changed | The mutation, and what it reported |
|---|---|---|
| GAP-17 `[critical]` | every count the suite writes is derived from `Limits()`, which is read once at the door and carried on the probe: `stream paging` narrows to `1, 2, min(StreamPage, 4)` and writes one more event than the widest; `payload ownership` writes `max(StreamPage, MaxRead) + 1`; `spread` and `dense versions` split their decisions at `MaxBatch` through `probe.batched`; the run identity is as wide as `MaxKey` leaves room for after the suite's reserve, and a `MaxKey` too small for even the narrowest is **fatal at the door** with the suite's own requirement in the message; `probe.store` refuses a later store publishing other numbers | `widest := widestNarrowing` → `stream paging` *failed* on the narrow store; a fixed 16-byte identity → `stream identity`, `resumption`, `lifecycle`, `transactions` *failed* with *"the identity does not render a legal stream key"*, which is GAP-17's own measured table |
| GAP-18 `[high]` | `Factory.Tail` answers the end of the log and `Factory.Window` sets the section's deadline; every log walk is bounded by what the run itself wrote (`walking{from, held, want}`), so a walk ends at its own last event rather than at an empty page that a second writer never leaves; the fallback that reads to the end names `Factory.Tail` in its refusal; `monotone visibility` takes a tail instead of draining the log through a reader | `walkLog` unbounded → four sections *"reading the log answered context deadline exceeded"* over the growing-log fixture; `Factory.Tail` removed from the same fixture → the same four with the refusal that names the hook |
| GAP-19 `[medium]` | `eventtest.Tx` carries the four rules the sections assert; `event/store.go` states that all eight `Store` methods are safe for concurrent use | doc-only, and both sentences describe assertions that already run (`staged`'s double commit, `probe.begin`'s cleanup, `concurrency`'s eight goroutines) |
| GAP-20 `[medium]` | `persistentFactory` is the first in-tree store to claim `Persistence: Supported`, and the fifteenth defect is the same store answering a second value with nothing in it | widening the balance check to admit zero reports that defect *passed*; `durabilitySection` coverage 35.0 % → **85.0 %** |
| GAP-21 `[medium]` | the `[]Envelope` case reads a **proper prefix** — the page a store publishes, of a stream one longer — clones the page after it, appends an envelope to the first, and compares the page the store still serves; the fourteenth defect `retainedPages` is a store that cuts every page out of one array it keeps | with the assertion removed the corruption is caught three assertions later by the kernel's own page check (*"answered a page that does not begin at the version it was read from"*), so the defect stays detected and stops being detected **at the hand-off** — recorded rather than overstated |
| GAP-22 `[low]` | the parameter breach is closed instead of recounted: `wroteWhatItSaid` takes a `fixture` value (7 → 2) and `from` a `walking` value (5 → 3) | `go/ast` over the package: **0** functions of more than four parameters, where the table had said one and the tree had two |
| GAP-23 `[low]` | S5's note says what `git` can and cannot establish for an untracked file, and what does hold `repo.go` | none — a record, not a behaviour |

**Round 3 closed five more, four of them adversarial findings against clauses the
suite had no case for at all. Each was demonstrated by putting the defect back
and watching the new assertion turn red, and each mutation was restored and its
sha256 compared afterwards.**

| Finding | What changed | The mutation, and what it reported |
|---|---|---|
| GAP-24 `[high]` | `probe.instants`, in `dense versions`: every envelope this run appended carries a non-zero `RecordedAt` and the read after answers the same one. The **sixteenth** defect is a decorator that blanks it; `TestAStoreThatMintsTheRecordedInstantWhenAnEventIsReadIsNotCertified` is the store that mints it at read time | `this.instants(...)` removed: *"the suite no longer detects a store that answers every envelope with no recorded instant at all: its dense versions section was reported passed"*, and beside it *"a store that mints the recorded instant at read time was reported passed for dense versions"* |
| GAP-25 `[medium]` | `Factory.Unparsable`, the hook only the store can answer, and `probe.unparsableCursor` in `resumption` — *not certified* when it is absent. The **eighteenth** defect is a store whose parser ignores its own error and starts at the beginning of the log; it is store-shaped rather than a decorator, because a decorator that forwards cannot tell a cursor it cannot parse from a foreign one without knowing the store's cursor format | `this.unparsableCursor(...)` removed: the defect was reported *passed*, and the factory answering no such cursor was reported *passed* rather than *not certified* |
| GAP-26 `[medium]` | `probe.unchanged`, in `binding`: all three published answers read twice on one store value with a store operation between, the backing through `Equal`. The **seventeenth** defect widens the page it publishes on every call; the capabilities and backing halves are `TestAStoreWhoseCapabilitiesOrBackingChangeUnderOneValueIsNotCertified` | `unchanged` made to compare the first reading with itself: the defect and both stores beside it *passed*, three assertions in one mutation |
| GAP-28 `[low]` | `probe.store` compares `Capabilities()` as it compares `Limits()`, and the plan's § Contracts sentence says which halves are checked | the comparison removed: *"a factory whose second store claims fewer capabilities than the store it was admitted on was reported passed for binding"* — which is also the first control either half of that comparison has had |
| GAP-29 `[low]` | the § Architecture metrics numbers recounted rather than remembered | none — a record |

Round 3's `[deferred]` findings — GAP-27, GAP-30, GAP-31, GAP-32 — are in
`## Debt` with their own arguments. **GAP-33's first half is closed** rather than
carried, because the fixture it names is one this round extended:
`stagingTx.done` is an `atomic.Bool` and its transition is a `CompareAndSwap`
inside the lock that publishes, so the two unsynchronised reads are gone. Its
second half (`persistentFactory`'s captured `shared`) stays in `## Debt`.

**The residue named rather than closed.** `go tool cover -func` puts
`proxies.go`'s three exported wrappers at 66.7 %: the uncovered statement in each
is its `t.Fatal`, which no test that stays green can reach. The three functions
those wrappers exist to call are at 100 %. `durabilitySection` is at 85.0 %: what
is left is the *not certified* arm, which a store claiming persistence does not
take.

**Checkpoint S5 — phase 4 (implementation)**

```
go build ./... && go vet ./event/... && test -z "$(gofmt -l event)" && go test -race -count=1 ./event/...
```

**Checkpoint S5 — phase 5 (tests)**

```
test "$(go test -list '^(TestTheMemoryStoreSatisfiesTheContract)$' ./event/eventmemory/ | grep -c '^Test')" = 1 && go test -race -count=1 -v -run '^(TestTheMemoryStoreSatisfiesTheContract)$' ./event/eventmemory/ && test "$(go test -list '^(TestTheSuiteStillDetectsEveryDefectItWasBuiltToDetect|TestEverySectionInTheInventoryWasReported|TestATrivialStoreNeedsNoInternalAccess|TestARunThatCertifiedNothingFails)$' ./event/eventtest/ | grep -c '^Test')" = 4 && go test -race -count=1 -v -run '^(TestTheSuiteStillDetectsEveryDefectItWasBuiltToDetect|TestEverySectionInTheInventoryWasReported|TestATrivialStoreNeedsNoInternalAccess|TestARunThatCertifiedNothingFails)$' ./event/eventtest/ && go test -race -count=1 ./event/...
```

The `-v` output of the first `-run` is the section list, and it is the evidence:
every section named, each *passed* or *not certified*, none skipped silently.
`TestATrivialStoreNeedsNoInternalAccess`'s report must state that the
unexport-one-field demonstration was performed and reversed; a compile-only test
that was never made to fail proves that a package compiles.

```
$ go build ./... && go vet ./event/... && test -z "$(gofmt -l .)" && go test -race -count=1 ./event/...
ok  	github.com/frostgrove/vv/event	1.279s
ok  	github.com/frostgrove/vv/event/eventmemory	1.052s
ok  	github.com/frostgrove/vv/event/eventtest	1.448s
```

```
$ test "$(go test -list '^(TestTheMemoryStoreSatisfiesTheContract)$' ./event/eventmemory/ | grep -c '^Test')" = 1
$ test "$(go test -list '^(…the four names…)$' ./event/eventtest/ | grep -c '^Test')" = 4
$ go test -race -count=1 -v -run 'TestTheMemoryStoreSatisfiesTheContract' ./event/eventmemory/
    suite.go:84: eventtest: binding: passed
    suite.go:84: eventtest: stream identity: passed
    suite.go:84: eventtest: expected version: passed
    suite.go:84: eventtest: dense versions: passed
    suite.go:84: eventtest: global order: passed
    suite.go:84: eventtest: conservation: passed
    suite.go:84: eventtest: stream paging: passed
    suite.go:84: eventtest: global paging: passed
    suite.go:84: eventtest: resumption: passed
    suite.go:84: eventtest: bounds: passed
    suite.go:84: eventtest: payload ownership: passed
    suite.go:84: eventtest: refusal classes: passed
    suite.go:84: eventtest: cancellation: passed
    suite.go:84: eventtest: lifecycle: passed
    suite.go:84: eventtest: concurrency: passed
    suite.go:84: eventtest: transactions: passed
    suite.go:84: eventtest: durability: not certified — this store does not claim persistence, so nothing of it survives a restart
    suite.go:84: eventtest: shared backing: passed
    suite.go:84: eventtest: monotone visibility: passed
    suite.go:84: eventtest: store failure classification: not certified — [outcome unconfirmed] cannot be produced by this store
--- PASS: TestTheMemoryStoreSatisfiesTheContract (0.01s)
    …the same twenty lines, verdict for verdict…
--- PASS: TestTheMemoryStoreSatisfiesTheContractAtNarrowerLimits (0.01s)
ok  	github.com/frostgrove/vv/event/eventmemory	1.032s
```

The second test is round 2's (GAP-17): the same store at `LogSpec{MaxKey: 40}`
and `Spec{StreamPage: 4, MaxBatch: 2, MaxRead: 3}`, which is a hundredth of the
defaults on three axes and legal on all of them. Twenty verdicts, identical to
the first, including the two *not certified* rows §UC-044 predicts.

```
$ go test -race -count=1 -v -run 'TestTheSuiteStillDetectsEveryDefectItWasBuiltToDetect|TestATrivialStoreNeedsNoInternalAccess' ./event/eventtest/
--- PASS: TestTheSuiteStillDetectsEveryDefectItWasBuiltToDetect (0.01s)
--- PASS: TestATrivialStoreNeedsNoInternalAccess (0.01s)
ok  	github.com/frostgrove/vv/event/eventtest	1.032s

$ go test -race -count=1 -v ./event/eventtest/       # every top-level test, after round 3
--- PASS: TestTheSuiteStillDetectsEveryDefectItWasBuiltToDetect (0.01s)
--- PASS: TestEverySectionInTheInventoryWasReported (0.02s)
--- PASS: TestAnApplicationRunsTheThreeProxiesOverItsOwnDeclaration (0.00s)
--- PASS: TestTheProxiesCompareSomethingThatCanDiffer (0.00s)
--- PASS: TestEachProxyReportsTheThingItExistsToFind (0.00s)
--- PASS: TestATrivialStoreNeedsNoInternalAccess (0.01s)
--- PASS: TestATransactionCapableStoreSatisfiesTheContract (0.01s)
--- PASS: TestARunThatCertifiedNothingFails (0.01s)
--- PASS: TestASectionThatNeverReturnedIsNotReportedPassed (0.00s)
--- PASS: TestAClaimedCapabilityWithAMissingHookIsRefused (0.00s)
--- PASS: TestEveryStoreCallASectionMakesCarriesADeadline (0.01s)
--- PASS: TestASectionWalksItsOwnTailOfALogSomebodyElseFilled (0.33s)
--- PASS: TestEverySectionIsCertifiedAtTheNarrowestLimitsAStoreMayPublish (0.02s)
--- PASS: TestASectionReachesAVerdictOverALogSomebodyElseIsStillWritingTo (0.02s)
--- PASS: TestAStoreThatKeepsWhatItWroteIsCertifiedForDurability (0.00s)
--- PASS: TestAStoreThatMintsTheRecordedInstantWhenAnEventIsReadIsNotCertified (0.00s)
--- PASS: TestAStoreWhoseCapabilitiesOrBackingChangeUnderOneValueIsNotCertified (0.00s)
--- PASS: TestAFactoryWhoseLaterStoresPublishSomethingElseIsRefused (0.00s)
--- PASS: TestACursorAStoreCannotParseIsRefusedRatherThanReadFromTheBeginning (0.00s)
--- PASS: TestEverySectionFailsAgainstAStoreThatRefusesEverything (0.00s)
--- PASS: FuzzKeysReportsACollisionExactlyWhenTwoIdentitiesRenderOneKey (0.00s)
ok  	github.com/frostgrove/vv/event/eventtest	1.446s

$ go test -race -count=1 -v -run 'TestATransactionCapableStoreSatisfiesTheContract' ./event/eventtest/
19 passed, 1 not certified (durability) — unchanged after round 3's three new clauses

$ go test -run '^$' -fuzz FuzzKeys -fuzztime 45s ./event/eventtest/
elapsed: 45s, execs: 1 779 459 (84 210/sec), new interesting: 61 — PASS

$ go test -race -count=1 ./event/...                 # twice, no flake
ok  	github.com/frostgrove/vv/event	1.273s
ok  	github.com/frostgrove/vv/event/eventmemory	1.052s
ok  	github.com/frostgrove/vv/event/eventtest	1.436s
ok  	github.com/frostgrove/vv/event	1.280s
ok  	github.com/frostgrove/vv/event/eventmemory	1.053s
ok  	github.com/frostgrove/vv/event/eventtest	1.437s

$ gofmt -l .           # silent
$ make unit            # every module, no FAIL
$ make check
check-deps: ok   check-tiers: ok   check-utils: ok   check-triplets: ok
check-todo: ok   check-replaces: ok   check-tidy: ok   check-otel-schema: ok
check-workspace: ok
check-workspace: ok
```

---

### S6 — structural checks, documentation and the five ADRs  `[ ]`

**Delivers** the checks `go test` cannot perform, the docs in the same change as
the code (a stale doc is a defect here, not untidiness), and the five decisions.

**Files**

- `scripts/checks.sh` — `event` added to `SUBSYSTEMS`, and that is the whole
  edit. **No `check_surface` arm, no `scripts/vv` command, no `Makefile`
  `COMMANDS` row**: `CLAUDE.md` says of `docs/api/surface.md` that "nothing
  checks it and nothing should", and an earlier draft of this plan added a
  `make check` arm that compared it. Withdrawn — see `### The defining test`
  item 2 for the argument and for what the baseline is instead.
- `scripts/extensions_test.go` — **new, and it is what makes the next two files
  possible.** `scripts/` is one Go package, and `scripts/tenancy_test.go` already
  declares `listed` (`:11`), `firstPartyDependencies` (`:28`), `under` (`:37`) and
  `const extension` (`:9`) at package level, plus two of the three test names the
  first draft of this plan gave the new file. A second file copying it verbatim
  does not compile, and `go test ./scripts/...` — S6's own first clause — fails
  with `redeclared in this block`. So the three helpers move here, `under` gains
  a `prefix` parameter, and each extension's file keeps its own prefix constant.
  This is **not** the table over extensions `## Risks and refusals` refuses: that
  refusal is about one test iterating a list of subsystems, and this is two
  independent tests sharing three functions.
- `scripts/tenancy_test.go` — the three helpers removed, its two test **names
  unchanged**. They are cited by `docs/ai/decisions/D-116…:115,119`,
  `docs/ai/flows/FL-033…:214`, `docs/modules/{en,ru}/tenancy.md:46` and the
  multitenancy roadmap, and `scripts/docs_test.go` fails on a cited test name
  that does not exist — so renaming the incumbent is a five-document change for
  nothing.
- `scripts/event_test.go` — on that model, three tests, **named so they do not
  collide**: `TestNoBaseSubsystemDependsOnTheEventExtension` (walks
  `go list ./...` edges, fails outright if fewer than 50 packages were listed, so
  it cannot prove nothing); `TestNoEventPackageCostsMoreThanTheSeamItNames` —
  **allowing `crud` and `errs` explicitly**, or it fails on its first run and gets
  loosened under pressure ([REC] R6); and
  `TestMerelyImportingTheEventExtensionStartsNothing`, which is a **`go/ast` walk,
  not a grep**: `*ast.GoStmt` anywhere and any `*ast.FuncDecl` named `init` under
  `event/`. The grep the first draft named (`^func init(`, `go func(`,
  `go <ident>`) is unanchored on its third arm, so the literal text "go " followed
  by a letter matches "go through" or "go to" in a comment — and `event/store.go`
  and `event/codec.go` are planned to carry exactly that kind of prose. A
  structural check that goes red on a comment gets loosened on its first run,
  which is R6 again. `scripts/docs_test.go:5-6,427,691` already carries the
  `go/ast`, `go/parser` and `token.NewFileSet` plumbing to build it from.
- `scripts/docs_test.go` — the `exactly.?once` arm (C10.6).
- A new `event/mutablestate_test.go` — the AST check of §INV-013's table, which
  targets **mutation** rather than kind, so the twenty-four sentinels and the
  `*Aggregate`/`*Fact` idiom pass and an assignment, index-assignment, `delete`,
  `append` or `sync` type does not. Same `go/ast` plumbing.
- A new `event/refusalmessages_test.go` — §INV-011's source check, which the
  coverage matrix promises S6 and no earlier draft planned:
  `TestNoRefusalRendersAnIdentityAPayloadAKeyOrACursor` walks every string
  literal reaching an error constructor under `event/` and fails on a format verb
  applied to a `Key`, a `Position`, a `Cursor`, a payload or a stored type name.
  Its control is `Stream.String()` and `Name()`, which must pass — they render a
  family and a declared identifier, which §INV-025 permits.
- A new `event/transactioncontrol_test.go` — the AST check the coverage matrix
  promised §INV-007 and §INV-027 and no earlier draft planned.
  `TestTheKernelNeverIssuesTransactionControlAndNeverRetries` walks the non-test
  files directly under `event/` and fails on (a) any call whose selector is
  `Begin`, `Commit` or `Rollback` — the kernel calls none, and the `Store` seam
  declares none, so there is no exemption to write; (b) any call to the store's
  `Append` lexically inside a `for` or `range` statement, which is what retrying
  a conflict looks like; (c) any `goto` under `event/`. `Load`'s paging loop
  contains `ReadStream` and is untouched by (b), so the check needs no
  allow-list — an allow-list is how a structural check becomes decorative (R6).
  Its anti-vacuity demonstration is the same discipline
  `TestATrivialStoreNeedsNoInternalAccess` carries: put a `for` around `Append`
  in `repo.go`, watch it go red, restore it, and say so in the section report.
  Same `go/ast` plumbing as the two files above.
- `docs/ai/decisions/D-121…D-125` + `Index.md` rows.
- `docs/ai/flows/FL-036-…` + both index tables in `docs/ai/flows/Index.md` — the
  only place file paths and symbols may appear, including §2.3's sentinel table.
  **It also carries the coverage matrix's `Proved by` column as a table**, which
  is what makes those names enforceable: `scripts/docs_test.go:49`
  (`TestEveryTestNameTheDocsCiteExists`) fails on a cited test name that does not
  exist, and nothing greps `.agents/artifacts/`. A test name that is renamed or
  never written turns `make unit` red from the doc that claims it.
- `docs/ai/usecases/modules/event/UC-032-…` + its `Index.md` row — links only to
  flows, names no symbol.
- `docs/modules/en/{event,eventmemory,eventtest}.md` and the three Russian
  translations, with rows in `docs/modules/Index.md`.
- `docs/roadmaps/2026-09-01-postgres-event-sourcing-roadmap.md` — E0 decisions 1
  and 3 marked superseded **in place**.
- `docs/roadmaps/Roadmap.md` — §15 and its summary row 27.
- `docs/api/surface.md` — regenerated by `make api`; the `event/…` sections
  become the phase-1 baseline **a person reads the diff of**, which is the whole
  of what `CLAUDE.md` asks of that file.
- [SPEC] §UC-004, §UC-017, §UC-022, §UC-030, §UC-048, §UC-053, §UC-060, §D.1,
  §D.2, §D.9, §D.12, §D.13, §D.14, §INV-018, §INV-021, §INV-024, §2.3 — edited to
  record what this plan decided, per `CLAUDE.md`'s doc-drift rule. The additions
  this round's validator forced: §D.1 gains `Compose`'s rendering; §UC-022 gains
  "the conflict arrives from `Append`, on every store"; §UC-030 gains
  `Repo.Authority(ctx)` as the comparison an operation that may decide nothing
  uses; §D.9 gains the `bounds` section and six cases (two appends in one
  transaction, a read inside one, two concurrent transactions, the aftermath of a
  conflict inside one, the mis-paged page, the illegal read product); §D.13 gains
  `MaxResidentBytes`, the derivation of every ceiling and the read-product /
  append-measurement split; §D.14 gains the read-shape, short-page,
  read-your-own-writes and conflict-door clauses.

  **And the correction round 3 forced, which is a program and not a sentence:**
  §UC-022's and §D.6's northstar spell the conflict recovery as a reload *inside
  the same transaction*. That is correct on `eventmemory` and impossible on
  `eventpg`, whose losing `INSERT` poisons the block. Both are rewritten so the
  **retry unit is the transaction** — roll back, begin again, reload, decide
  again — which is correct on both stores and is the ordinary SQL shape. §UC-057
  gains the bare-cancellation obligation, §2.3 the wrapper's `Is`/`As` split and
  `ErrSample`, §INV-031 the "zero calls, because `Limits()` is retained"
  restatement, and §INV-017's fold/upcaster asymmetry gains the rule that decides
  it for every extension point.

  **And the three corrections S1's implementation forced**, each recorded where
  [SPEC] says the opposite today: §2.3's *"the store's own error rides along as
  the wrapped cause, so `errors.Is` reaches it"* and its cause paragraph, and
  §INV-045's falsification clause *"the store's own error asserted reachable with
  `errors.Is` through every mapped sentinel"*, both become **reachable through
  `event.CauseOf` and by nothing else** (GAP-135); and §D.15's standard-library
  row gains `strings`, `unicode` and `unicode/utf8`, which `Compose` and the
  kernel text rule need and which that row omits.

  **And the two round 6 forced.** §2.3's request-class row gains the class wrap
  that makes *"a transport answers a client error"* true of every member and not
  one — `crud.ErrBadRequest` on `ErrKey`, `ErrEncode`, `ErrSample` and
  `ErrTooLarge`, with `ErrTooLarge`'s `*errs.Fault` above it (GAP-157). And
  §2.3's *"checkable prohibition"* on a store returning one of this vocabulary's
  sentinels as its cause becomes a statement about the **kernel**: the
  prohibition is not checked, it is made harmless — a wrap never answers for a
  target in this vocabulary — because checking it cost the decorator's and the
  upcaster's own error its `errors.Is` reachability (GAP-159). §INV-024's
  *Falsified by* clause moves with it, and the conformance defect it named leaves
  `defects.go` (C8).

**The five ADRs** ([REC] §7's agenda, next free number **D-121**; the fifth is
this plan's own addition and Q2's answer moves to **D-121…D-125**):

| # | Title | The paragraph a future reader comes looking for |
|---|---|---|
| **D-121** | The event vocabulary is a root package, and its second implementation is what earns it | [ES]'s E0 decision 3 is **its own condition being met** — "a second store would reopen it" — and E0 decision 1's aggregate blocker is aimed at a live schema, which phase 1 writes none of. Also: that §INV-019's proof is the third-package fixture, that a surface baseline is a report and never a gate (`CLAUDE.md`), and what the git arm would add after the first tag if a person wants it; and why **`ReadOnly` is the one wrapper in this repository with no `Next()`**, so a [[D-061]] review closes rather than reopens it |
| **D-122** | A store classifies its own failure in a closed vocabulary, and the kernel maps it through `errors.As` | `jobs/queue.go:808:normalizeSenderError` reads its classification with `err.(rejectedPlacement)`, so a decorator wrapping with `%w` turns a conflict into an ambiguity. `event` uses `errors.As`; write down that this is where the rule was learned. Also: why the vocabulary is its own enum and not the sentinel set; why `Support` is a tri-state when both existing capability structs are `bool`s; and **the refusal wrapper's two traversals** — `errors.Is` reaches the sentinel and the declared wrap, `errors.As` the declared wrap only, and **neither reaches the cause**, which `event.CauseOf` is for (GAP-135: `port.KindOf` falls through `errs.AsFault` to six `errors.Is` branches over `crud` sentinels, so a cause-reaching `Is` renders an uncertain commit 503) — with the measurement that forced it (`cache/errors.go`'s shape declares no `Unwrap` and no `As`, so `port.KindOf`'s `errs.AsFault` cannot see a class wrap through it) and the two exceptions, `ErrRefused` and `ErrUpcast`, whose cause **is** their wrap. **And that the kernel uses a bounded `errors.As` of its own** (`findAs`, GAP-158): a store's chain is a foreign chain, and the stdlib walk over one has no budget and no `recover`, so a cyclic `Unwrap` wedged the request goroutine on the first statement of every store error. The same ADR carries why no wrap answers for a target inside this vocabulary (GAP-147, GAP-159) — the partition is enforced on the target of the traversal, not by refusing or dropping a cause, because dropping it took the application's own error down with it |
| **D-123** | The declaration panics, and `Try…` is what returns the error | `crud/sqlrepo/blueprint.go:74:Define` / `:82:TryDefine`, which [[D-021]] already cites by line. The sibling exists for the **negative tests** — ten triggers, otherwise ten `recover()` blocks — and §INV-013 survives because both calls return the same value through one code path |
| **D-124** | `event.JSON` is deliberately smaller than the codecs beside it | The 20 shared unexported functions and the three-directory build-tag pair; the trust boundary **derived** — the byte cap and the field checks on `event/store.go`, not the provenance of the log — and, stated in the same paragraph, **what the decode path does not bound**: no `MaxDecodedBytes`, no `MaxPayloadDepth`, so phase 2 knows what `eventpg` inherits; **what the codec is as a *party*** — C1's aliasing clause and C9's concurrency clause; and the trigger for extracting a shared analyser, so the conversation resumes rather than restarts |
| **D-125** | A composed key is a wire format, and phase 1 freezes its rendering | `Compose`'s escape-and-join, byte for byte, with the length-prefix misreading of `cache/address.go:NamespaceOf` recorded so it is not proposed again (that prefix is hashed, never rendered). What reopening it costs: §INV-005 makes the rendering part of every stream ever written, so changing it orphans every aggregate — every one reads as version 0, silently, which is §D.1's own named failure. A store's key column and a migration are the only ways back |

**Covers** INV-007 (the AST check), INV-011 (the source check), INV-013,
INV-019 (the regenerated baseline, a report and not a gate), INV-027 (the AST
check), INV-036 (the doc arm); every documentation obligation of §9 items 4
and 5.

**Checkpoint S6 — phase 4 (implementation)**

```
test "$(go test -list '^(TestNoBaseSubsystemDependsOnTheEventExtension|TestNoEventPackageCostsMoreThanTheSeamItNames|TestMerelyImportingTheEventExtensionStartsNothing|TestNoDocPromisesExactlyOnceDelivery|TestEveryTestNameTheDocsCiteExists|TestEverySymbolTheDocsCiteIsDeclaredWhereTheDocSaysItIs)$' ./scripts/ | grep -c '^Test')" = 6 && test "$(go test -list '^(TestNoPackageLevelStateIsEverMutated|TestNoRefusalRendersAnIdentityAPayloadAKeyOrACursor|TestTheKernelNeverIssuesTransactionControlAndNeverRetries)$' ./event/ | grep -c '^Test')" = 3 && go test -count=1 ./scripts/... && go test -race -count=1 ./event/... && make check && make unit && make vet && test -z "$(gofmt -l .)" && make api && git --no-pager diff --stat docs/api/surface.md
```

S6 is the one section whose deliverables **are** its tests and its checks, so its
two blocks are one. The `-list` clause is still first: `./scripts/` must already
contain all three new names *and* both incumbent `tenancy_test.go` names, which
`go test -count=1 ./scripts/...` then runs — that is the pair that proves the
redeclaration was actually resolved rather than worked around by dropping a test.
The trailing `make api` and `git diff --stat` **produce** the surface diff and
nothing gates on it; the section report quotes it, and a person answers it.

then, for the no-regression claim `CLAUDE.md` requires, twice in a row:

```
make integration && make integration
```

Expected: green except the two pre-existing `vvdb` pool failures
(`TestOneConfigShapeOpensEveryEngine/postgres`,
`TestDbpgxOpensAPoolFromTheSameConfig`), which are verified failing with this
change set stashed and are recorded in `## Debt`.

---

## Coverage matrix

Every UC and INV in [SPEC], and every carried gap. The **section** column names
where it is delivered; the **checkpoint** column names the one whose output
proves it.

### Use cases

| UC | Section | Checkpoint | Proved by |
|---|---|---|---|
| UC-001 declare an aggregate, three facts, two revisions | S2 | S2 | `TestADeclaration` |
| UC-002 a second aggregate, both state-type cases | S2 (compile) + S4 (`ErrWrongStream`) | S4 | `TestTheCrossingsThatMustNotCompile`, `TestFoldRefusesAnotherInstance`, `TestAppendRefusesInItsStatedOrder` |
| UC-003 same shape across a revision bump | S2 | S2 | `TestADeclaration` |
| UC-004 a malformed declaration | S2 | S2 | `TestEveryMalformedDeclarationPanicsAndTryDefineReturnsIt` |
| UC-005 a fact declared after the table was read | S2 | S2 | `TestTheSealRefusesALateFact` |
| UC-006 construct a store, bind two aggregates | S3 (constructor) + S4 (`Open`/`Bind`) + S5 (`binding`) | S5 | `TestTwoStoreValuesOverOneLog`, `TestEveryNumberAStoreAndItsLogTakeIsBoundedAtBothEnds`, `binding` |
| UC-007 two stores over two backings | S4 | S4 | `TestWithinComposesForTwoBackings`, `TestAppendRefusesInItsStatedOrder` |
| UC-008 a readiness answer | S3 | S3 | `TestCheckAnswersWhileTheStoreIsOpenAndAfterItIsClosed` |
| UC-009 a fresh stream loads as the zero state | S4 | S4 | `TestAFreshStreamLoadsAsZero` |
| UC-010 a stream with history is folded | S4 | S4 | `TestAStreamWithHistoryFoldsToItsCurrentState` |
| UC-011 a long stream is paged | S4 + S5 | S5 | `TestAStreamWithHistoryFoldsToItsCurrentState` (its page-of-1 arm), `stream paging` |
| UC-012 an older revision | S2 (chain) + S4 (load) | S4 | `TestAStreamWithHistoryFoldsToItsCurrentState` (its revision-1 stored event) |
| UC-013 an unknown event type | S4 | S4 | `TestEveryHistoryClassRefusalIsRaisedByTheThingItNames` |
| UC-014 an unreadable revision | S2 (the chain refuses it) + S4 (the surfacing) | S4 | `TestEveryHistoryClassRefusalIsRaisedByTheThingItNames` |
| UC-015 a malformed payload | S2 (the codec refuses it) + S4 (the surfacing) | S4 | `TestEveryHistoryClassRefusalIsRaisedByTheThingItNames`, `FuzzAStoredPayloadIsFoldedOrRefused`, `FuzzALoadFoldsAStoredStreamOrRefusesItWhole` |
| UC-016 the upcaster refuses | S2 (recovery) + S4 (surfacing) | S4 | `TestAnUpcasterMayRefuseAndAFoldMayNot`, `TestEveryHistoryClassRefusalIsRaisedByTheThingItNames`, `TestAPanickingCodecBecomesTheRefusalItsErrorWouldHaveBeen` |
| UC-017 a stored payload over the cap → `ErrPayload` (C5) | S4 | S4 | `TestEveryHistoryClassRefusalIsRaisedByTheThingItNames`, `TestARequestClassRefusalRendersAClientStatusAndAHistoryClassOneDoesNot` |
| UC-018 a load under a cancelled context | S4 + S5 | S5 | `cancellation` |
| UC-019 load, decide, append | S4 + S5 | S5 | `expected version` |
| UC-020 a decision that changed nothing | S4 | S4 | `TestAnEmptyAppendChecksTheKeyAndNothingElse` |
| UC-021 two decisions in one operation | S2 (`Fold`) + S4 (`Append`) | S4 | `TestOneAppendCarriesTwoIdenticalChanges` |
| UC-022 two writers, one stream | S3 (admission) + S5 (`expected version`, `transactions`) | S5 | `TestTwoTransactionsOnOneStreamLeaveOneWinnerAndAConflictFromAppend`, `TestAnAppendIsAdmittedOnlyAtTheVersionItWasDecidedAt`, `expected version`, `transactions` (the aftermath case) |
| UC-023 **WITHDRAWN** — resolves to UC-022 + UC-034 | S4 + S5 | S5 | — |
| UC-024 an unencodable or oversized payload | S2 (`New`) + S4 (surfacing) | S4 | `TestFoldRefusesAnotherInstance`, `TestACodecPanicBecomesThatMethodsOwnRefusal`, `TestARequestClassRefusalRendersAClientStatusAndAHistoryClassOneDoesNot` |
| UC-025 a batch over the bound | S4 + S5 | S5 | `bounds`, `TestAnAppendWhoseActualBytesExceedTheResidentCeilingIsRefused` |
| UC-026 another aggregate's change (compile error) | S2 + `testdata/crossings` | S2 | `TestTheCrossingsThatMustNotCompile` |
| UC-027 a caller names an expected version | S4 + `testdata/crossings` + S5 | S5 | `TestTheCrossingsThatMustNotCompile`, `expected version` |
| UC-028 a caller transaction spans load and append | S3 (`Tx`, and the two-stream unit of work) + S4 (`Within`) + S5 | S5 | `TestASecondAppendInOneTransactionIsAdmitted`, `TestATransactionThatStagesToTwoStreamsNeverCrossesThem`, `TestWithinComposesForTwoBackings`, `transactions` |
| UC-029 the caller's transaction rolls back | S3 + S5 (`transactions`) | S5 | `TestARolledBackAppendBurnsItsPositions`, `transactions` |
| UC-030 two subsystems, one transaction | S4 (`Authority`) | S4 | `TestRepoAuthorityIsTheComparisonTwoSubsystemsUse` |
| UC-031 a token over another backing | S4 | S4 | `TestAppendRefusesInItsStatedOrder`, `TestAnEmptyAppendChecksTheKeyAndNothingElse` |
| UC-032 `Within` with no transaction | S4 | S4 | `TestWithinAnswersTheStoresTransactionQuestion` |
| UC-033 `Within` on a store with no transactions | S4 | S4 | `TestWithinAnswersTheStoresTransactionQuestion` |
| UC-034 the commit outcome is unknown | S1 (map) + S4 + S5 | S5 | `TestAnOutcomeOutsideTheVocabularyTakesTheFailSafeDefault`, `store failure classification` |
| UC-035 the same command twice, no idempotency key | S4 (the absence) | S4 | `TestOneAppendCarriesTwoIdenticalChanges` |
| UC-036 a bounded read | S4 + S5 | S5 | `global paging` |
| UC-037 no monotone-visibility promise | S3 (commit-order positions, the cursor's encoding, and `MonotoneVisibility: Supported` re-derived from them) + S5 (`resumption`, whose out-of-position-order writer is driven by the stage-time-allocating defect store because `eventmemory` cannot commit out of order) | S5 | `TestTheStoreDeclaresWhatItCanDoAndSaysWhatItCannot`, `TestManyWritersLeaveOneDenseHistoryAndAMonotoneLog`, `monotone visibility`, `resumption` |
| UC-038 per-stream order is a subsequence | S5 (`global order`) | S5 | `global order`, `conservation` |
| UC-039 repeated reads tile a quiescent log | S5 (`global paging`) | S5 | `global paging` |
| UC-040 an aggregate unit-tested with no database | S3 + S5 | S5 | `TestTheMemoryStoreSatisfiesTheContract` |
| UC-041 a fold with no store at all | S2 | S2 | `TestFoldRefusesAnotherInstance`, `TestAChangeRetainsNoApplicationValue` |
| UC-042 a payload's revisions are round-tripped | S2 (`Fact.RoundTrip`) + S5 (`eventtest.RoundTrip`) | S2 | `TestACodecThatDecodesIntoAReusedBufferIsCaught` |
| UC-043 a store implementer runs the suite | S5 | S5 | `TestTheMemoryStoreSatisfiesTheContract`, `TestEverySectionInTheInventoryWasReported` |
| UC-044 a store cannot exercise a capability | S3 (`eventmemory` says what it cannot) + S5 (the suite's three words) | S5 | `TestTheStoreDeclaresWhatItCanDoAndSaysWhatItCannot`, `TestEverySectionInTheInventoryWasReported`, `store failure classification` |
| UC-045 the suite falsifies itself | S5 | S5 | `TestTheSuiteStillDetectsEveryDefectItWasBuiltToDetect` |
| UC-046 an ambient transaction, no `Within` | S4 | S4 | `TestWithinAnswersTheStoresTransactionQuestion` |
| UC-047 close, and everything after | S3 + S5 (`lifecycle`) | S5 | `TestCloseIsIdempotentAndDecidesNothing`, `lifecycle` |
| UC-048 a decorator wraps a store | S1 (`Refused`) + S5 (decorated runs) | S5 | `refusal classes` |
| UC-049 a marked context meets a different transaction | S4 | S4 | `TestWithinAnswersTheStoresTransactionQuestion` |
| UC-050 a composite identity | S1 (`Compose`) + S2 (the mapper and `Aggregate.Key`) | S2 | `TestADeclaration`, `TestComposeRendersTheFrozenKey`, `FuzzComposeRendersAKeyThatIsLegalAndReversible` |
| UC-051 the mapper produces an illegal key | S2 (`New`, `Fold`) + S4 (`Load`, `Append`) + S5 | S5 | `TestAppendRefusesInItsStatedOrder`, `stream identity` |
| UC-052 a state reaching a slice or a map | S2 | S2 | `TestADeclaration`, `TestAReferenceKindStateFoldsWithoutAliasing` |
| UC-053 a cursor persisted, resumed, refused when foreign | S3 + S5 (`resumption`) | S5 | `TestACursorIsTheBackingsAndResumesThroughAnyStoreValueOverIt`, `FuzzACursorEitherResumesInsideTheLogOrIsRefused`, `resumption`, `store failure classification` |
| UC-054 two store values, one backing | S3 | S3 | `TestTwoStoreValuesOverOneLog` |
| UC-055 a store whose lifetime is one request | S3 + S4 (`Bind` is O(1)) | S4 | `TestAStoreIsBuiltPerRequestOverALogThatOutlivesIt`, `TestABindInterrogatesNoCodecAndMutatesNoDeclaration` |
| UC-056 a payload its own codec cannot encode | S2 | S2 | `TestEveryMalformedDeclarationPanicsAndTryDefineReturnsIt` |
| UC-057 cancellation during an append, two windows | S3 (the store's half) + S4 (the surfacing) + S5 (`cancellation`) | S5 | `TestACancellationTravelsAsItselfFromEveryDoorTheStoreOperates`, `TestAContextCauseNeverTravelsThroughARefusal`, `cancellation` |
| UC-058 one append, two identical changes | S4 | S4 | `TestOneAppendCarriesTwoIdenticalChanges` |
| UC-059 two aggregates, one family | S4 (`Bind`) + S5 | S5 | `binding` |
| UC-060 the backend fails an append | S1 (classification) + S4 (surfacing) + S5 | S5 | `store failure classification`, `refusal classes` |
| UC-061 the caller mutates a handed-out state | S4 + S5 | S5 | `payload ownership` |
| UC-062 the fold line is load-bearing | S4 | S4 | `TestAStreamWithHistoryFoldsToItsCurrentState` |
| UC-063 a payload mutated after `New` | S2 | S2 | `TestAChangeRetainsNoApplicationValue` |
| UC-064 one operation, two instances | S2 (`Fold`) + S4 (`Append`) | S4 | `TestFoldRefusesAnotherInstance`, `TestAppendRefusesInItsStatedOrder` |
| UC-065 a fold aliases a reference kind out of a change | S2 | S2 | `TestAReferenceKindStateFoldsWithoutAliasing`, `TestAChangeRetainsNoApplicationValue` |
| UC-066 a transaction open and nothing bound | S4 + S5 (`transactions`) | S5 | `transactions` |
| UC-067 a fold panics | S2 (not recovered, out of `Fold`) + S4 (out of `Load`) | S4 | `TestAnUpcasterMayRefuseAndAFoldMayNot`, `TestAFoldPanicUnwindsOutOfLoad` |
| UC-068 the state type **is** a reference kind | S2 | S2 | `TestAReferenceKindStateFoldsWithoutAliasing` |

### Invariants

| INV | Section | Checkpoint | Proved by |
|---|---|---|---|
| INV-001 history is append-only | S1 (method inventory) + S5 | S5 | `TestTheStoreSeamHasEightMethodsAndNoneMutatesOrQueries`, `dense versions` |
| INV-002 admission only at the observed version | S3 + S5 (`expected version`) | S5 | `TestAnAppendIsAdmittedOnlyAtTheVersionItWasDecidedAt`, `expected version` |
| INV-003 one append is one atomic unit | S3 (admission, and each record's own identity) + S5 | S5 | `TestAnAppendIsAdmittedOnlyAtTheVersionItWasDecidedAt`, `TestAnEnvelopeCarriesTheTypeAndRevisionOfTheRecordItWasWrittenFrom`, `expected version`, `transactions` |
| INV-004 folds and upcasters are pure | S2 | S2 | `TestAFoldIsPureOverTheStateItIsGiven`, `TestAChangeRetainsNoApplicationValue`, `TestAReferenceKindStateFoldsWithoutAliasing` |
| INV-005 wire identity declared; the key's rendering frozen | S1 (the frozen rendering, byte for byte) + S2 (the declared identifiers) | S1 | `TestComposeRendersTheFrozenKey`, `FuzzComposeRendersAKeyThatIsLegalAndReversible`, `TestADeclaration` |
| INV-006 no partially rehydrated state from `Load` | S4 | S4 | `TestALoadThatFailsMidStreamReturnsNothing`, `TestEveryHistoryClassRefusalIsRaisedByTheThingItNames`, `FuzzALoadFoldsAStoredStreamOrRefusesItWhole` |
| INV-007 a conflict is never retried by the framework | S4 (a recording store) + S6 (the AST walk) | S6 | `TestOneLoadAndOneAppendMakeExactlyTheStoreCallsTheContractNames`, `TestTheKernelNeverIssuesTransactionControlAndNeverRetries` |
| INV-008 full replay is the only authority | S4 | S4 | `TestAStreamWithHistoryFoldsToItsCurrentState` |
| INV-009 dense versions, sparse positions, no clock ordering | S3 + S5 | S5 | `TestVersionsAreDenseAndPositionsAscendWhateverTheClockSays`, `TestManyWritersLeaveOneDenseHistoryAndAMonotoneLog`, `dense versions`, `global order` |
| INV-010 a revision is derived, never typed | S2 (`Revisions()`) | S2 | `TestADeclaration` |
| INV-011 no identity, payload, version, position, key or cursor becomes a default field | S1 (message table) + S6 (source check) | S6 | `TestEveryRenderingNamesAClassAndNeverAValue`, `TestNoRefusalRendersAnIdentityAPayloadAKeyOrACursor` |
| INV-012 event history is not a CRUD collection | S1 (method inventory) | S1 | `TestTheStoreSeamHasEightMethodsAndNoneMutatesOrQueries` |
| INV-013 no package-level mutable state, no constructor starts anything | S3 (obeyed) + S6 (the AST checks) | S6 | `TestAStoreIsBuiltPerRequestOverALogThatOutlivesIt`, `TestNoPackageLevelStateIsEverMutated`, `TestMerelyImportingTheEventExtensionStartsNothing` |
| INV-014 no start-up migrates, creates or discovers | S3 (by construction) | S3 | `TestTwoStoreValuesOverOneLog`, `TestAStoreIsBuiltPerRequestOverALogThatOutlivesIt` |
| INV-015 an at-token is minted, never manufactured | S4 + `testdata/crossings` | S4 | `TestAForgedTokenIsRefusedBeforeAnyStatement`, `TestTheCrossingsThatMustNotCompile` |
| INV-016 store identity is the backing; `Equal`, never `==` | S1 | S1 | `TestABackingAndAnAuthorityAreComparedAndNeverIdentical` |
| INV-017 a fold cannot fail; only an upcaster may refuse | S2 + S4 | S4 | `TestAnUpcasterMayRefuseAndAFoldMayNot`, `TestACodecPanicBecomesThatMethodsOwnRefusal`, `TestAFoldPanicUnwindsOutOfLoad`, `TestAPanickingCodecBecomesTheRefusalItsErrorWouldHaveBeen` |
| INV-018 the store never sees a Go type; who applies which bound (C4) | S1 (the seam's values carry bytes and no Go type) + S4 (the verification) | S4 | `TestTheSeamsValuesAreDefinedTypesWithTheDeclaredFields`, `TestAnOverLongPageIsRefused`, `TestAMisPagedStreamIsRefusedBeforeItIsFolded` |
| INV-019 a second store costs zero diffs to `event/` | S4 (the caller half) + S5 (**the proof**) + S6 (the regenerated baseline, a report and not a gate) | S5 | `TestATrivialStoreNeedsNoInternalAccess` |
| INV-020 one aggregate's value is not usable with another, and the scope | S2 (compile) + S4 (runtime) | S4 | `TestTheCrossingsThatMustNotCompile`, `TestFoldRefusesAnotherInstance`, `TestAppendRefusesInItsStatedOrder` |
| INV-021 nothing held and nothing handed out share mutable memory | S1 (clauses) + S2 (1, 2, 8) + S3 (4, 7, committed and staged alike) + S5 (3, 6 and `payload ownership`) | S5 | `TestAChangeRetainsNoApplicationValue`, `FuzzADecidedFactIsFrozenAgainstItsCallersBuffer`, `TestAPageIsTheCallersIncludingItsCapacity`, `TestAPageOfStagedRecordsIsTheCallersToo`, `TestNothingACallerHandsToAnAppendIsRetainedOrRewritten`, `FuzzAPayloadIsHandedBackByteForByteAndBelongsToWhoeverReadsIt`, `payload ownership` |
| INV-022 every bound declared, reachable, unescapable; two doors | S3 (the resident ceiling counted in envelopes, where a store derives its page and the kernel checks it again) + S4 | S4 | `TestTheResidentPageIsTheCeilingCountedInEnvelopes`, `TestBothDoorsCheckTheStore`, `TestAStoreWhoseProductExceedsTheResidentCeilingIsRefused`, `TestAnAppendWhoseActualBytesExceedTheResidentCeilingIsRefused` |
| INV-023 encodability is the codec's question, answered before `main` | S2 | S2 | `TestEveryMalformedDeclarationPanicsAndTryDefineReturnsIt` |
| INV-024 the refusal vocabulary is a partition | S1 | S1 | `TestTheRefusalVocabularyIsAPartition`, `TestADeclaredWrapIsReachableByErrorsAsAndACauseIsNot` |
| INV-025 a refusal names identifiers, never data | S1 | S1 | `TestEveryRenderingNamesAClassAndNeverAValue` |
| INV-026 a conflict carries no version | S1 + S5 | S5 | `TestEveryRenderingNamesAClassAndNeverAValue`, `refusal classes` |
| INV-027 the framework never opens, commits or rolls back a transaction | S1 (the seam declares none) + S4 (a recording store) + S6 (the AST walk) | S6 | `TestTheStoreSeamHasEightMethodsAndNoneMutatesOrQueries`, `TestOneLoadAndOneAppendMakeExactlyTheStoreCallsTheContractNames`, `TestTheKernelNeverIssuesTransactionControlAndNeverRetries` |
| INV-028 an authority **is** its transaction's identity | S1 + S3 (two live transactions on one store are not `Same`) + S4 | S4 | `TestABackingAndAnAuthorityAreComparedAndNeverIdentical`, `TestASecondAppendInOneTransactionIsAdmitted`, `TestRepoAuthorityIsTheComparisonTwoSubsystemsUse` |
| INV-029 cancellation identity preserved, uncertainty outranks it (C3) | S1 + S5 (`cancellation`) | S5 | `TestAContextCauseNeverTravelsThroughARefusal`, `cancellation` |
| INV-030 every question is answered by the exact outer value | S1 (all required) + S5 (decorated runs) | S5 | `refusal classes`, `store failure classification` |
| INV-031 an empty append touches no store | S4 | S4 | `TestAnEmptyAppendChecksTheKeyAndNothingElse` |
| INV-032 close is idempotent and decides nothing | S3 + S5 (`lifecycle`) | S5 | `TestCloseIsIdempotentAndDecidesNothing`, `lifecycle` |
| INV-033 a stream is (family, key) byte-exact; injectivity is the application's | S1 (`Compose`'s own two properties, and the rendering they hold over) + S2 + S3 (the store keys its history by both halves) + S5 (`stream identity`) | S5 | `FuzzComposeRendersAKeyThatIsLegalAndReversible`, `TestComposeRendersTheFrozenKey`, `TestADeclaration`, `TestTwoFamiliesSharingOneKeyAreTwoStreams`, `stream identity` |
| INV-034 the two reads describe one set | S5 (`conservation`) | S5 | `conservation` |
| INV-035 a returned resume point is safe to persist | S3 + S5 (`resumption`) | S5 | `TestACursorIsTheBackingsAndResumesThroughAnyStoreValueOverIt`, `FuzzACursorEitherResumesInsideTheLogOrIsRefused`, `resumption` |
| INV-036 a walk tiles; delivery is at least once | S5 + S6 (the doc arm) | S6 | `global paging`, `TestNoDocPromisesExactlyOnceDelivery` |
| INV-037 **WITHDRAWN** — resolves to INV-040 | S4 | S4 | — |
| INV-038 what is safe to copy and to share, plus C9's row | S1 (table, and the inert rows under `-race`) + S5 (`concurrency`) | S5 | `TestTheKernelsValuesAnswerTheSameFromManyGoroutines`, `concurrency` |
| INV-039 one family names one aggregate, forever | S4 (`Bind`) + S5 (`binding`) | S5 | `binding` |
| INV-040 an append has outcomes | S4 + S5 | S5 | `TestAnOutcomeOutsideTheVocabularyTakesTheFailSafeDefault`, `store failure classification` |
| INV-041 the transaction rule is [[D-118]]'s | S3 (the store's half) + S4 + S5 (`transactions`) | S5 | `TestOneLoadAndOneAppendMakeExactlyTheStoreCallsTheContractNames`, `transactions` |
| INV-042 the kernel retains no application value | S2 (`Change` keeps three things and no value) + S5 (`payload ownership`'s inbound half, where a retained value becomes observable) | S5 | `TestAChangeRetainsNoApplicationValue`, `payload ownership` |
| INV-043 an unstated capability is refused, at both doors | S4 + S5 (the anti-vacuity rule) | S5 | `TestBothDoorsCheckTheStore`, `binding` |
| INV-044 a change names its stream; an append is one stream's | S2 (`Fold`) + S4 (`Append`) + S5 (`expected version`) | S4 | `TestFoldRefusesAnotherInstance`, `TestAppendRefusesInItsStatedOrder`, `expected version` |
| INV-045 a store classifies; the kernel maps that and nothing else | S1 + S5 | S5 | `TestAnOutcomeOutsideTheVocabularyNormalises`, `store failure classification` |

### Carried gaps

| Item | Section | Checkpoint | Proved by |
|---|---|---|---|
| C1 GAP-100 a decoded value may not alias the codec's own reused memory | S1 (clause) + S2 (`RoundTrip`'s proxy, the zero-value second payload, `ErrSample`) | S2 | `TestACodecThatDecodesIntoAReusedBufferIsCaught` |
| C2 GAP-101 a decorator's own refusal — `Refused` + `ErrRefused` | S1 (enum, sentinel, map) + S5 (the case) | S5 | `refusal classes` |
| C3 GAP-102 `errors.Is(err, context.DeadlineExceeded)` is **false** on `ErrUncertain` | S1 | S5 | `TestAContextCauseNeverTravelsThroughARefusal`, `cancellation` |
| C4 GAP-103 the kernel verifies the page's length and its shape | S4 (the check) + S5 (`bounds`, `stream paging`, the three defects) | S5 | `TestAnOverLongPageIsRefused`, `TestAMisPagedStreamIsRefusedBeforeItIsFolded`, `bounds`, `stream paging` |
| C5 GAP-104 `ErrTooLarge` off the load path; `ErrCursor` → wiring; the wrapper's `Is`/`As` split | S1 (the classes and the wrapper) + S4 (the rendering) | S4 | `TestADeclaredWrapIsReachableByErrorsAsAndACauseIsNot`, `TestARequestClassRefusalRendersAClientStatusAndAHistoryClassOneDoesNot` |
| C6 GAP-105 the inbound enumeration is scoped and the remainder named | S1 (clause) + S6 (the module page's table) | S6 | `TestEverySymbolTheDocsCiteIsDeclaredWhereTheDocSaysItIs` |
| C7 GAP-106 a hand-out is the recipient's including its capacity | S3 (obeyed) + S5 (the one-byte `append` case) | S5 | `TestAPageIsTheCallersIncludingItsCapacity`, `payload ownership` |
| C8 GAP-107 one defect count, one inventory, computed by a test | S5 | S5 | `TestTheSuiteStillDetectsEveryDefectItWasBuiltToDetect` |
| C9 GAP-108 codec, mapper, fold, upcaster and clock concurrency | S1, S2, S3 (clauses) + S5 (the policed half) | S5 | `TestOneDeclarationIsDecidedAndFoldedFromManyGoroutines`, `concurrency` |
| C10 GAP-109 six residuals | S1 (3, 5), S2 (1, 2), S4 (3, 4, 5), S5 (2, 4), S6 (6) | S6 | `TestTheSealRefusesALateFact` (1), `TestAReferenceKindStateFoldsWithoutAliasing` and `lifecycle` (2, 4), `TestAppendRefusesInItsStatedOrder` (3), `TestRepoAuthorityIsTheComparisonTwoSubsystemsUse` (5), `TestNoDocPromisesExactlyOnceDelivery` (6) |

Every UC-\*, INV-\* and carried item appears exactly once above.

**The matrix and the sections agree in both directions.** Round 1 found thirteen
rows whose section named an item that section's own `Covers` omitted, and an
implementer works from the section rather than from the matrix. Diffed pairwise
after the repair, with the disposition of each:

| Item | Repair |
|---|---|
| UC-021, UC-042 | added to S2's `Covers` |
| UC-028 | added to S3's `Covers` |
| UC-034, UC-040 | added to S5's `Covers` |
| UC-055, UC-067 | added to S4's `Covers` |
| UC-058 | **moved**: removed from S3's `Covers`, added to S4's, and named in S4's phase-5 checkpoint as `TestOneAppendCarriesTwoIdenticalChanges`. It is kernel behaviour — two identical `Change` values in one `Append` — so S3's claim was wrong on the merits and not only on the bookkeeping |
| INV-001, INV-011, INV-018, INV-021, INV-042 | the section's `Covers` gained the row, with the half it delivers named |
| INV-042's third leg | the matrix row **dropped S6**: the plan promised a source check there and S6 planned none. It is proved by S2 (the `Change` carries frozen bytes, a stream and an adapter, and no application value) and, since round 3, by S5's `payload ownership` inbound half rather than by S4 — a retained value is only observable where the same fact is minted twice and one list is never appended |

The **other** direction was checked as well, which round 1 did not tabulate:
eight rows named fewer sections than the sections themselves claimed, and the
matrix row was widened in each — UC-014 and UC-015 gained S2, UC-044 and INV-041
gained S3, UC-057 and INV-019 gained S4, INV-043 and INV-044 gained S5.

**Round 3 added the third column, and it is not decoration.** A row whose only
evidence was the trailing blanket `go test -race ./event/...` was not a proved
row: that command passes with no test written for it. Every row now names a
**checkpoint test** or a **conformance section**, and both kinds are enforceable
— a checkpoint test by its section's `-list` count clause, a conformance section
by `TestEverySectionInTheInventoryWasReported`, which `go test -list` alone
cannot do because every section is a subtest. The column is copied into
`docs/ai/flows/FL-036` in S6, where `scripts/docs_test.go:49` fails on a cited
test name that does not exist — so a renamed or unwritten test turns `make unit`
red rather than going quiet.

What that forced, tabulated so the diff is checkable:

| Change | Rows |
|---|---|
| Nine tests added to S4's checkpoint, which had twelve names and ~30 rows pointing at it | `TestAStreamWithHistoryFoldsToItsCurrentState`, `TestEveryHistoryClassRefusalIsRaisedByTheThingItNames`, `TestWithinAnswersTheStoresTransactionQuestion`, `TestRepoAuthorityIsTheComparisonTwoSubsystemsUse`, `TestALoadThatFailsMidStreamReturnsNothing`, `TestOneLoadAndOneAppendMakeExactlyTheStoreCallsTheContractNames`, `TestAnAppendWhoseActualBytesExceedTheResidentCeilingIsRefused`, `TestAPanickingCodecBecomesTheRefusalItsErrorWouldHaveBeen`, `TestARequestClassRefusalRendersAClientStatusAndAHistoryClassOneDoesNot` |
| One added to S1, two to S2, one to S3, one to S5, one to S6, one to `./scripts/` | `TestTheStoreSeamHasEightMethodsAndNoneMutatesOrQueries`; `TestEveryMalformedDeclarationPanicsAndTryDefineReturnsIt`, `TestAReferenceKindStateFoldsWithoutAliasing`, `TestAChangeRetainsNoApplicationValue` (S2 also gained the last of these); `TestCheckAnswersWhileTheStoreIsOpenAndAfterItIsClosed`; `TestEverySectionInTheInventoryWasReported`; `TestTheKernelNeverIssuesTransactionControlAndNeverRetries`; `TestNoDocPromisesExactlyOnceDelivery` |
| One renamed, because the plan's own anchoring paragraph uses the old name as the example of a name that matches too much | `TestClose` → `TestCloseIsIdempotentAndDecidesNothing`; `TestTwoBackingsAreComparedAndNeverIdentical` → `TestABackingAndAnAuthorityAreComparedAndNeverIdentical`, which also gives §INV-028 a proof at S1 |
| Checkpoint moved later, because the proof is a conformance section | UC-006, UC-011, UC-018, UC-019, UC-025, UC-027, UC-028, UC-036, UC-051, UC-059, UC-061, INV-026, INV-042, INV-043 — S5's `Covers` gained each |
| Checkpoint moved to S6, because the proof is an AST walk | INV-007, INV-027 — S6's `Covers` gained both, S4 keeps the recording-store half, S1 keeps INV-027's seam-shape half |
| Row narrowed | INV-025 back to S1 alone: the S6 source check is named for §INV-011 and `TestEveryRenderingNamesAClassAndNeverAValue` already covers every `String()` in the vocabulary |

Zero disagreements remain in either direction, computed rather than eyeballed:
113 rows, 68 UC and 45 INV, each appearing exactly once, each section list equal
to the set of sections whose `Covers` names it, and each row naming at least one
counted test or one inventoried section. The two withdrawn items (UC-023,
INV-037) are absent from every `Covers` by design and forward in the matrix.

---

## Architecture metrics

Counted, not asserted. `references/architecture.md`'s thresholds: ≤ 400 lines per
file, ≤ 7 public methods per type, ≤ 7 public symbols per module, ≤ 5 internal
imports per file, ≤ 2 fakes to unit-test an object, 0 import cycles, 0 global
mutable state.

### Per file

| File | Exported | Internal imports | Fakes a unit test needs | Est. lines |
|---|---|---|---|---|
| `event/doc.go` | 0 | 0 | 0 | 10 |
| `event/identity.go` | 6 | 0 | 0 | 140 |
| `event/bounds.go` | 6 | 0 | 0 | 35 |
| `event/text.go` | 0 | 0 | 0 | 70 |
| `event/backing.go` | 2 + 2 methods | 1 (`crud`) | 0 | 90 |
| `event/authority.go` | 2 + 4 methods | 1 (`crud`) | 0 | 90 |
| `event/outcome.go` | 9 | 0 | 0 | 80 |
| `event/errors.go` | 25 — 24 sentinels + `CauseOf` (GAP-135) | 2 (`crud`, `errs`) | 0 | 320 |
| `event/store.go` | 11 | 0 | 0 | 180 |
| `event/codec.go` | 2 | 0 | 0 | 260 → **103 actual** |
| `event/encodable.go` | 0 | 0 | 0 | — → **290 actual** (round 2's split of `codec.go`) |
| `event/chain.go` | 3 | 0 | 1 (a `Codec`) | 220 → **106 actual** |
| `event/aggregate.go` | 4 | 0 | 0 | 260 → **112 actual** |
| `event/fact.go` | 3 | 0 | 0 | 280 → **312 actual** — over the estimate by the two value walks `RoundTrip`'s two properties are now asked with, and 88 under the threshold |
| `event/change.go` | 1 | 0 | 0 | 90 → **44 actual** |
| `event/seal.go` | 0 | 0 | 0 | 60 → **54 actual** |
| `event/token.go` | 2 + 8 methods | 0 | 0 | 110 → **47 actual** |
| `event/binding.go` | 3 | 0 | 1 (a `Store`) | 150 → **134 actual** |
| `event/repo.go` | 1 + 4 methods | 0 | 1 (a `Store`) | 330 → **261 actual** |
| `event/marker.go` | 0 | 0 | 0 | 80 → **40 actual** |
| `event/reader.go` | 3 + 3 methods | 0 | 1 (a `Log`) | 170 → **77 actual** |
| `event/eventmemory/log.go` | 3 | 1 (`event`) | 0 | 140 → **90 actual** |
| `event/eventmemory/store.go` | 3 | 1 | 0 | 200 → **118 actual** |
| `event/eventmemory/append.go` | 0 | 1 | 0 | 190 → **64 actual** |
| `event/eventmemory/read.go` | 0 | 1 | 0 | 170 → **84 actual** |
| `event/eventmemory/transaction.go` | 2 | 1 | 0 | 230 → **143 actual** |
| `event/eventmemory/cursor.go` | 0 | 1 | 0 | 90 → **42 actual** |
| `event/eventtest/suite.go` | 3 | 1 | 0 | 300 → **233 actual** after round 2's split; `Factory` carries **7** fields after round 3's `Unparsable`, at `architecture.md`'s per-class threshold and not over it |
| `event/eventtest/probe.go` | 0 | 1 | 0 | — → **292 actual** (round 2's split of `suite.go`, plus round 3's constancy reading) |
| `event/eventtest/inventory.go` | 0 | 1 | 0 | 120 → **74 actual** |
| `event/eventtest/sections_*.go` (6 files after round 3) | 0 | 1 | 0 | ≤ 380 each → **364 / 354 / 306 / 251 / 238 / 127 actual**, recounted with `wc -l`. Round 3's cursor clause took `sections_read.go` to 421, over the 400 threshold, so `resumption` and the three helpers only it uses moved to `sections_resumption.go` |
| `event/eventtest/defects.go` | 0 | 1 | 0 | 360 → **335 actual** (18 defects after round 3) |
| `event/eventtest/proxies.go` | 3 | 1 | 0 | 150 → **92 actual** |
| `event/eventtest/report.go` | 0 | 1 | 0 | 90 → **66 actual** |
| `event/eventtest/declaration.go` · `stores.go` | 0 | 1 | 0 | — → **103 / 116 actual** (the two files S5 added) |

**No file breaches 400 lines**, and one had to be split to keep that true:
round 2's work took `event/eventtest/suite.go` to **473**, so the probe and every
call it makes on a store moved to `probe.go` and the two are 224 and 259.
`sections_read.go` at **399** is one line under and is the next to split.
`event/encodable.go` is the largest shipped in the kernel
at **290**, and S4's five files came in at 559 against an estimate of 840. `codec.go` reached **393** once GAP-178's promoted-name walk and
GAP-179's map-key route were in it — seven lines under the threshold with
GAP-176's repair still owed — and the walk moved into a file named for the
question it answers, which is *what a payload may be* rather than *what the codec
seam is*. `event/repo.go` at ~330,
`event/eventtest/defects.go` at ~360 and `event/eventtest/sections_*.go` at ~380
are the three to watch, and the section files split further by name rather than
by size if they grow.

**No file under `event/` is named `helpers`, `utils`, `common`, `misc` or
`base`.** `architecture.md` hard-bans exactly those, and
`find . -name 'helpers*.go' -o -name 'utils*.go' -o -name 'common*.go'` outside
`test/` and `_examples/` returns nothing in this tree — every file in `crud/`,
`jobs/`, `cache/`, `auth/`, `storage/` and `tenancy/` is named for what it owns.
`event/eventtest/proxies.go` owns the suite's three **runnable proxies** for
obligations the framework cannot check — round-trip fidelity, mapper injectivity,
family distinctness — which is a responsibility with a name; `helpers.go` was how
that file would have become the place the next three unrelated things went.
**No file imports more than two internal packages.** `event` imports `crud` and
`errs` and nothing else — `crud.SameDataSource`, `crud.ErrConflict`,
`crud.ErrUnavailable`, `errs.TooLarge`, and no executor vocabulary at all (Q4).
**No object needs more than one fake**, and every one of those fakes is an
interface this phase defines rather than a vendor's.

### Breaches, each justified in writing

| Metric | Value | Justification |
|---|---|---|
| Public symbols per module, threshold **7** | `event` **≈ 82** | Recounted against this plan's contracts, which is **higher than [SPEC]'s ≈ 71**: +`TryDefine`, +`TryDeclare` (Q21), +`Refused`, +`ErrRefused` (C2), +`ErrSample` and −`Backing.Valid` (round 3, net zero), +`CauseOf` (round 4, GAP-135 — the cause left `errors.Is` and needed a named reader), +6 kernel-ceiling constants §INV-022 and the product rule require and §D.16's count omitted. The threshold is a metric over a **class**, where every extra method is another way for one object to be inconsistent; `event` is the **vocabulary of a subsystem**. 24 sentinels are a partition a caller matches with `errors.Is` — collapsing them puts a string where `errors.Is` is, which is [[D-015]]'s defect. 27 types are two seams, and §INV-019 requires every store-seam type to be constructible from another package, which is what forces them to be exported: **a smaller count is a smaller extension point.** 16 constants are six ceilings, three `Support` values and seven `Outcome` values, all of which a store or a deployment reads. 15 functions are the declaration idiom plus three minting constructors, each with a named reader here and none a "for later" API. `docs/api/surface.md` shows `crud`, `jobs` and `cache` each exporting dozens; this is the tree's own scale for a subsystem |
| Public methods per type, threshold **7** | `Store` **8** | A **port** with two independently consumable halves of four: a consumer holds a `Log` and cannot append (§UC-036), a store implements `Store`. `architecture.md`'s ≤ 7 is about a class's ways to be inconsistent; splitting this port further would mean an optional interface, which is exactly what §INV-030 refuses. Every other exported type is ≤ 6 |
| Fakes to test an object | ≤ 1 everywhere | `At`, `Change`, `Commit`, `Backing`, `Authority`, `Stream` need **zero** — they are inert values. `Repo` needs one `Store`; `Reader` one `Log`; `Chain` one `Codec` |
| Global mutable state | **0** | Enforced by S6's AST check over §INV-013's table, which targets **mutation** rather than kind — so the 24 sentinels (`errors.New`, never reassigned) and the `*Aggregate`/`*Fact` declaration idiom pass, and any assignment, index-assignment, `delete`, `append` or `sync` type fails. A check that must be suppressed on its first run is how a structural check becomes decorative (GAP-63) |
| Import cycles | **0** | `event` → `crud`, `errs`; `eventmemory` → `event`; `eventtest` → `event`. `crud` and `errs` name neither. Proved by `go list -deps` in S1's and S6's checkpoints |
| Modules edited for one feature, threshold 3 | 3 in S6 (`scripts/`, `docs/`, `Makefile`) | Structural checks and docs are the change's own obligations, not a feature spread |
| Fields per class **7** · lines per class **200** · parameters **4** · functions over 50 lines | `event/eventtest`'s `probe`: **7 fields**, ~1 200 lines across **55** methods in eight files, **0** of **157** functions over four parameters, five functions of 50–62 lines (`payloadOwnershipSection` 62, `refusalClassesSection` 60, `streamIdentitySection` 58, `concurrencySection` 55, `bindingSection` 50 after round 3's constancy reading) | **The lines-per-class half is not justified and is carried as debt** (round 1, GAP-10); the other three are closed. Every one of the 55 methods is unexported and on nothing's surface, no public contract turns on the shape, and phase 2 adds no section, so the cost lands the first time somebody writes a twenty-first. Round 1 took the parameter breaches from four to two by moving a case into a `failureCase` value and a door into a function (GAP-4, GAP-9); round 2 took them to **zero** — `wroteWhatItSaid` 7 → 2 through a `fixture` value and `from` 5 → 3 through a `walking` value (GAP-22) — and the field count from 8 to **7** by grouping what the run learns at the door into one embedded `opening`, which is also what let every section derive its counts from `Limits()` (GAP-17). Counted with `go/ast` over the package, not eyeballed. The split — fixture builder, store façade, verdict sink — is in `## Debt` |

### The one-sentence responsibility of each new package

- **`event`** — *turns declared facts into an append-only per-aggregate history
  over any store.*
- **`event/eventmemory`** — *keeps that history in memory, with real transactions.*
- **`event/eventtest`** — *proves a store satisfies the contract, and proves it can
  still fail.*

### Budgets (`restrictions.md` §9), from §D.13

| Path | Budget |
|---|---|
| `Load` of a 100 000-event stream | 391 pages, one page (~90 KB) plus the state resident, **zero** kernel clones, three comparisons per envelope for the page verification, 100 000 decodes and folds, **nothing retained after it returns but the state** |
| `Fact.New` | one encode (~1 µs at 200 B) and one mapper call, at decision time |
| `Aggregate.Fold` | one clone plus one decode per change; the ordinary operation folds at most `MaxBatch` = 64 changes ≈ 13 KB |
| `Append` | one key scan of ≤ `MaxKey` bytes, one struct comparison per change, one `len()` sum over the batch, one `Backing()` field read, one `Transaction(ctx)` context lookup — no statement, no allocation of a token, no map. An **empty** append is the key scan and nothing else: zero calls on the store, which is what retaining `Limits()` at `Bind` buys |
| `Bind` | five constant-time checks — the nil store, the family, the backing, the limits (five ceilings and two divisions for the read products), the capabilities — one allocation, and the capabilities and limits retained |
| `eventmemory` hand-out | one allocation and one ~200 B copy per envelope returned |

The rule those numbers encode, and it is what a deployment must understand:
**raising `MaxPayload` without lowering `StreamPage` multiplies the memory one
read can hold.** `MaxResidentBytes` is the ceiling, and it is enforced in **two
different ways because the two paths are not the same problem**: on the read
paths as a worst-case **product** of the store's published numbers, checked as a
division at `Bind` and at `Read`, because a store fills a page before the kernel
sees a byte of it; on the append path as the **actual sum** of the record payload
bytes at step 4, because there the kernel is holding them. There is deliberately
no product check on `MaxBatch` — enforcing one would refuse an 80 KB batch
because some other fact in the same store may be 1 MiB, and the only remedy would
be lowering a payload cap the application needs (`event/bounds.go`). A store
whose **read** product is illegal is `ErrWrongStore` at both doors and the
`bounds` conformance section carries the fixture and its at-the-product control;
an append over `MaxResidentBytes` is `ErrTooLarge`, and
`TestAnAppendWhoseActualBytesExceedTheResidentCeilingIsRefused` is where that is
pinned.

---

## Disagreements

The spec reached its shape through five validator rounds and a judged panel. It is
not a draft to improve. These three are recorded with the argument; **the plan
implements the spec's shape anyway**, and the owner decides.

### D1 — Policy does not belong on the store seam, and C2 is the price of putting it there

C2 adds `Refused` to a closed enum and `ErrRefused` to the sentinel set so that
§UC-048's quota wrapper can spell its own refusal. GAP-101's option **(c)** is the
answer I would take: a quota is a decision about a *caller*, made from a request's
identity, and the store seam is where the kernel asks a **backend** what happened.
Under (c) §UC-048's precondition keeps "count appends" and "record what was read"
and drops "refuse writes for a tenant that is over quota", the refusal moves one
layer out to the app-usecase where every other authorisation decision in this
framework already lives (`security.Gate`, `auth.Guard`, `tenancy.Authority`), and
`Outcome` stays at six values that describe **what happened to a write** rather
than six-and-one-that-describes-a-policy.

The cost of (c) is that every store implementer now reads a value no store will
ever return — one enum row of permanent surface, on the extension point the phase
freezes.

**Why the plan takes (b) anyway.** §UC-048 is a `[happy]` use case of one of the
seven actors, and [ES] obligation 4 states the before-the-forwarded-call rule for
exactly that wrapper. Deleting the actor's own happy path to avoid an enum value
is narrowing the spec, which this plan may not do. If the owner takes (c), the
change is: delete `Refused` and `ErrRefused`, edit §UC-048's precondition, and add
one `## Debt` row naming where a quota refusal belongs instead. It is **one
section's edit in S1** and nothing downstream depends on it.

### D2 — Freezing the contract at the end of phase 1 freezes it before any consumer has used it

§D.14's compatibility paragraph freezes `Store`, `Log`, `Codec`, the structs and
the two enums at the end of phase 1, against **zero** real consumers: no
application has declared an aggregate, `eventpg` does not exist, and the first
projector is a later phase. A contract frozen before its first use means the first
consumer's discovery arrives as a breaking change rather than as a revision.

**What I would do instead:** state that the freeze begins at the **first tag**,
with `eventpg` in phase 2 allowed to force one contract revision if the walk in
§D.15 turns out to be wrong somewhere. `git tag` is empty today, so nothing is
lost by saying so.

An earlier draft of this entry proposed spelling that with a `check-surface` arm
of `make check`. **Withdrawn, and not quietly:** `CLAUDE.md` says of
`docs/api/surface.md` that "nothing checks it and nothing should", and this plan
does not implement around a binding rule — it says so and lets the owner decide.
The freeze-at-first-tag question is what is actually being asked here, and it
needs no mechanism at all: it is a sentence in D-121.

**Why the plan takes the spec's shape anyway.** The freeze is what makes
§INV-019's zero-diff claim mean anything, and a freeze with an escape hatch is not
a freeze. The disagreement is recorded because a reader of D-121 in phase 2 will
want to know whether the escape was considered.

### D3 — `eventmemory`'s transactions are the schedule risk, and the fallback is reversible where a frozen contract is not

Q12 is taken as **yes** and §UC-040's asymmetry is the right argument. But [REC]
§3.8 prices it at 30–40 % of `eventmemory` — staged writes, admission against
committed + staged with a per-stream claim, a second concurrency surface under
`-race`, rollback leaving no readable event and burning the positions it would
have taken, plus the third `eventtest` fixture that needs staging of its own —
and the tree said **no** to the identical
question twice (`jobsmemory` asserts no `jobs.Stager`; `cachememory` asserts no
`cache.Transactional`; `cachetest` has no transaction section).

If S3 slips, the fallback is honest, cheap and **reversible**: `eventmemory`
declares `Transactions: Unsupported`, the `transactions` section reports *not
certified*, and phase 1 ships a named `## Debt` item listing the ten use cases and
five sentinels with no test in the root module. Nothing about the *contract*
changes, so nothing is frozen wrongly. Taking the fallback should be a scheduling
decision at S3's checkpoint, not a design one — and it should be taken loudly, in
the report, because a green `make unit` with that capability unset is a run in
which group E was never exercised.

---

## Risks and refusals

### What this plan deliberately does not do

- **No SQL, no schema, no migration, no driver, no `go.mod`, no `go.work` edit,
  no `replace` line.** An agent following [ES]'s `eventpg` checklist will add them
  and `check_workspace` will fail, because `workspace_modules` discovers modules
  by `go.mod` and finds none ([REC] R9).
- **No telemetry.** No import, no span, no metric; `eventotel` is forbidden by name
  in [ES] and by [[D-114]].
- **No `eventfx`, no `port.Service` adapter, no `storage.Store` adapter.** [ES]'s
  capability obligations 8 and 9 are conditioned on the last two and are therefore
  **vacuous rather than unmet**; non-goal 21 is where that is argued.
- **No projector, no outbox, no subscription, no snapshot, no retention floor, no
  idempotency key, no point-in-time load, no multi-stream append, no caller-supplied
  page size, no position range on the receipt.** Each is a named [SPEC] non-goal
  with its own argument and its own "what would reopen it".
- **No fix to `jobs/queue.go:808:normalizeSenderError`'s type assertion.** It is a
  real defect in a live subsystem ([REC] §3.1) and it is a separate change with its
  own review. D-122 names it as where the `errors.As` rule was learned; the fix is
  a `## Debt` row.
- **No fix to `crudsql.Transaction`'s bare type assertion.** [[D-061]]'s failure in
  `crudsql`, pre-existing, and it decides whether `eventpg` can join a transaction
  behind a decorated executor. Raised now so phase 2 does not discover it as an
  `event` bug.
- **No `event` row in `TRIPLETS`.** `eventmemory` and `eventpg` are not a triplet:
  they are two implementations of one contract, and what holds them equal is
  `eventtest.Run` run from each implementation's own package — a stronger tie than
  matching test names, because it compares behaviour ([REC] §2.5).
- **No generalisation of `scripts/tenancy_test.go` into a table over extensions.**
  That file's own comment argues against a list. `scripts/event_test.go` is a
  second file on the same model — sharing three helper functions through
  `scripts/extensions_test.go`, which is deduplication and not a table — and the
  missing `SUBSYSTEMS` rows for `jobs`, `cache`, `health`, `runtime` and `otel`
  are a separate finding.
- **No rename of `scripts/tenancy_test.go`'s two test names**, even though the
  first draft of `scripts/event_test.go` collided with both. Five documents cite
  them and `scripts/docs_test.go` fails on a cited name that does not exist; the
  newcomer takes the distinct names.

### Risks

| # | Risk | Mitigation |
|---|---|---|
| **R1** | **`make unit` goes red on the docs, not on the code.** `scripts/docs_test.go:526` and `:49` read every file under `docs/` and fail when a cited `path.go:Symbol` or `TestName` does not exist — the new flow, three module pages **and their three Russian translations** are all in scope. This is the most likely first red | Every citation is written **after** the symbol exists; S6 is the last section for exactly this reason, and its checkpoint runs `go test ./scripts/...` first |
| **R2** | **`eventmemory`'s transactions are the schedule risk** (D3) | The fallback is named, priced and reversible; the decision point is S3's checkpoint |
| **R3** | **The single text rule admits a family that is not a legal identifier in any real store.** [REC] §3.5 recommends `validRegistryName`'s narrower domain for declared identifiers; the plan keeps [SPEC]'s single rule | The cost is stated where the rule is written. If phase 2 hits it at the first `CREATE TABLE`, narrowing a *declared identifier* rule is a breaking change for declarations already written — so this is worth the owner's five minutes now |
| **R4** | **The self-falsification suite has no precedent anywhere in the tree.** Eight defects, six forwarding decorators and two purpose-built stores, plus the rule that each fails the section named for it | Priced as new work in S5. The nearest precedent is the control-case discipline `CLAUDE.md` already mandates (`test/integration/gate_relscope_test.go:81`), and every section carries one |
| **R5** | **The second JSON analyser copy is pre-existing debt this phase will be blamed for.** An ECONV DRY audit of `event` will find `jobs/json.go` and `cache/codec.go` and read a third codec as the third instance | D-124's paragraph exists to stop that conversation being had a third time, and it names the trigger for extracting a shared analyser |
| **R6** | **`scripts/event_test.go` fails on its first run if copied verbatim.** `tenancy_test.go`'s cost test computes the core's first-party graph against `./crud`'s | The `event` version allows `crud` **and** `errs` explicitly. A structural check loosened under pressure on its first run is a decorative one |
| **R7** | **§INV-019 now has no automated change-detector at all**, because the `make check` arm was withdrawn (`CLAUDE.md` forbids it; `### The defining test` item 2) | The **proof** was never the detector: it is `TestATrivialStoreNeedsNoInternalAccess`, a compile-time fixture in a third package, and it runs in `make unit`. What the detector added was a nudge to read the surface diff, and S6's checkpoint produces that diff for a person to read. D-121 records both, so phase 2 does not read the absence as an oversight |
| **R8** | **The exported-symbol breach is argued and will still be read as unargued** | `## Architecture metrics` carries the count, the per-type measurement and the four-group argument, and the module page repeats the per-type half |
| **R9** | **C2 grows a frozen enum.** If the owner takes D1's option (c) after S1 lands, `Refused` and `ErrRefused` must be deleted before the phase-1 baseline is committed | The decision is cheap until `make api` records the baseline in S6, and expensive after. Flag it in S1's report |
| **R10** | **Two pre-existing `make integration` failures** (`TestOneConfigShapeOpensEveryEngine/postgres`, `TestDbpgxOpensAPoolFromTheSameConfig`) and a pre-existing `make check-tidy` failure in four modules | Verified failing on the clean tree before any change here; recorded in `## Debt` and stated in the report rather than omitted |
| **R11** | **`Compose`'s rendering is frozen by §INV-005 the moment the first stream is written, and it is decided here on a whiteboard rather than against a real key column.** A later change orphans every aggregate silently — each reads as version 0 | D-125 states the rendering byte for byte and names what reopening it costs. The escape set is the exact complement of the kernel key rule and reads the same predicate, so the two cannot drift; `TestComposeRendersTheFrozenKey` and `FuzzComposeRendersAKeyThatIsLegalAndReversible` both fail if the framing is removed. If the owner wants a different rendering, **S3 is the last cheap moment** — S2 is written and no stream has been stored by anything yet |
| **R12** | **`eventmemory` refuses a second transaction on a claimed stream at once where a SQL store waits.** A caller that reads `ErrConflict` as "someone else committed" and logs it as a data race will see it in a unit test where nothing committed | The contract states the door and the class and explicitly **not** the waiting (`event/store.go`); the `transactions` section asserts survival and class rather than timing; the degradation paragraph in S3 says who starves whom |
| **R13** | **The read product ceiling refuses a store that a deployment believes it configured correctly.** `MaxPayload: 1<<20` with `StreamPage: 256` is 256 MiB and is `ErrWrongStore` at `Bind`, which reads as the framework breaking a working configuration | It never worked; it held 256 MiB per concurrent load. The refusal names the product, the ceiling and the two factors, and `event/bounds.go`'s table says what to do — lower the page, which costs nothing because a load is paged. The same argument does **not** hold for a batch, which is why `MaxBatch` has no product check (round 3) |
| **R14** | **A store that panics unwinds into the caller's request goroutine with the caller's transaction open.** The kernel does not recover it (`event/store.go`) | It is the caller's transaction and closing it is already the caller's job under [[D-118]] — the ordinary `defer tx.Rollback()` covers it, and it is the same shape as any panicking `database/sql` call today. Recovering it would mean the kernel classifying a failure the store did not classify (§INV-045). Named here so phase 2's `eventpg` does not add a `recover()` and call it robustness |
| **R15** | **"Assume the ambient transaction is unusable after a refusal" is stricter than `eventmemory` needs and looks like a caller inconvenience invented for PostgreSQL.** A caller who tests against `eventmemory` will never see the constraint bite, and will discover it against `eventpg` | It **is** PostgreSQL's rule, and stating it in phase 1 is the only moment it costs nothing: §UC-022's northstar now retries the transaction rather than the append, no conformance case exercises the freedom `eventmemory` happens to have, and `eventpg` must document it in its own module page — `## Debt`. The alternative, a per-store capability, makes every caller write two programs |

---

## Debt

Deferred, never dropped. Everything still open in [GAPS], everything [SPEC] §8
and §9 hand forward, and what this plan's own reconciliation found.

**From [GAPS] `## Deferred` — the one still open** (GAP-26, GAP-66, GAP-77,
GAP-78, GAP-91, GAP-98 and GAP-99 were closed rather than carried, verified in
their dispositions tables; **GAP-41 and GAP-53 are decided in S2 and S4 above and
leave this list**):

- **GAP-25 — a conformance defect does not fail exactly one section.** The
  admit-everything store also fails `concurrency`; the short-page decorator also
  fails `dense versions` and `conservation`; the position-reusing one fails
  whatever else compares positions. The claim the suite ships is the one it can
  hold — *each defect fails the section named for it* — with the exceptions named.
  Partitioning the sections so a stronger claim holds is left to when the suite has
  a second real store to run against. `[medium][deferred]`

**From [SPEC] §9 — debt this phase hands to the next:**

- **`event/eventpg`**, with everything §1 and §D.15 say it must write and nothing
  they say it must not. `[high][deferred]`
- **The git-diff arm of the zero-diff check**, as a **report** against the
  phase-1 tag, once a tag exists — never as a `make check` arm, which `CLAUDE.md`
  forbids for a surface baseline. Until then the S6 checkpoint's own
  `make api && git diff --stat` is what produces the diff a person reads.
  `[medium][deferred]`
- **A composition-root catalogue** on `jobs/catalog.go:25:NewCatalog`'s model, if
  Q19's two family-collision escapes are judged worth closing. Additive;
  `eventtest.Families` is the runnable proxy until then. `[medium][deferred]`
- **A position range on the receipt, and a projector**, when there is a reader for
  them (non-goals 20 and 3). `[low][deferred]`
- **The savepoint half of §INV-028, in `eventpg`'s own package test** — that
  `crudsql`'s savepoint resolves to the parent's `*sql.Tx`, so an append inside a
  savepoint and one inside its parent carry authorities that are `Same`. Not a
  conformance section: a savepoint is `crudsql` vocabulary and the memory store has
  none, and a factory hook whose requiredness is derivable from no capability is
  what §D.9's own rule forbids. `[medium][deferred]`

**From S1's test review (round 1), the findings it graded `[deferred]`** — each
names the section that owns the assertion, and each is here because after that
section nobody will look again:

- **GAP-T7 — the six ceilings in `event/bounds.go` are asserted by nothing.**
  Only `MaxNameBytes` is read by a test, and only relatively. `MaxKeyBytes`
  `2<<10` → `2<<20`, `MaxPayloadBytes` → `1<<10`, `MaxResidentBytes` → `0` and
  `MaxBatchCount`/`MaxPageCount` swapped all survive the S1 suite, and so do the
  relations the derivation table rests on: `MaxResidentBytes >= MaxPayloadBytes`,
  `MaxResidentBytes / MaxPayloadBytes >= 64`, `MaxKeyBytes + MaxNameBytes + 8 <= 2704`
  and `MaxBatchCount = MaxResidentBytes / jobs.DefaultPayloadBytes`. **S4** is
  where the ceilings are first enforced and where the relation assertions belong,
  beside that enforcement — a wrong `MaxKeyBytes` is invisible even there,
  because S4's checks are written in terms of the constant. `[medium][deferred]`
- **GAP-T8 — `Support`'s three-state distinction is asserted only in its
  rendering.** Round 7 closed the rendering half (three distinct phrases, which
  turns M31 and M80 red); `stated()` is still the one function in `event/` with
  0.0 % coverage and no caller. **S4** owns it, beside `Bind`'s and `Read`'s reads
  of it for §INV-043: `Unstated`/`Unsupported`/`Supported` must answer `stated()`
  false/true/true, and M50 and M51 must go red. `[medium][deferred]`
- **GAP-T10 — the walk's budget has no absolute assertion.** Every assertion in
  `TestAJoinedCauseCannotOutlastTheWalksBudget` is written in terms of
  `causeHops`, so widening it 64× (`64` → `4096`) survives, and past ~65 536 the
  only detector is the suite's one wall-clock construct. One row pinning
  `causeHops` to a stated absolute value, with the reason, makes a silent
  widening a visible diff. Owner: **S5**, with the defect fixtures.
  `[low][deferred]`
- **GAP-T11 — three small edges.** A family of exactly `MaxNameBytes` is never
  driven, so `checkText`'s `>` could become `>=` at `Stream.String`'s call site
  undetected (**S4**, which owns the text rule's doors); and the `Log` relation
  is asserted by nothing — `Store` spelling `Log`'s four methods inline instead
  of embedding survives, because the `NumMethod` check catches it first only when
  the count also changes (**S6**, with the structural checks; `var _ Log = (Store)(nil)`
  in a compile position, or an `ast` check that `Store` embeds `Log`). The third
  edge — `Compose` at an arity other than three — was closed by round 7's golden
  table, which composes one, two and three parts and pins the `Compose()` /
  `Compose("")` pair. `[low][deferred]`
- **GAP-T12 — the two `go/ast` checks read the source text of an unexported
  function and one file name.** `namesDeclaredInErrorsFile` reads only
  `errors.go`, so an `Err*` sentinel declared elsewhere in the package is
  invisible to the partition table; and `namesListedInTheGate` reads the
  identifiers inside the function literally named `vocabulary`, so a
  behaviourally identical refactor is reported as a drift that did not happen.
  Owner: **S6**, whose doc-and-surface pass is where the walk widens to every
  non-test file and the gate check's message stops claiming a behavioural
  observation it did not make. `[low][deferred]`

**From S5's implementation review (round 1), the findings it graded `[deferred]`:**

- **GAP-10 — `event/eventtest`'s `probe` is one object with twenty entry points.**
  **7 fields** after round 2, 55 unexported methods across eight files, ~1 200 lines of
  type-plus-methods against `architecture.md`'s 200, and **no** method over four
  parameters. It still matches one distributed-god-object signal: a mutable
  `state` blob (`unmet`, `broke`) written from seven files. It changes no
  public contract — every method is unexported, `Certify` and the section
  inventory are `export_test.go`'s — and phase 2 adds no section, so nothing is
  blocked. The shape when it is paid: the **fixture builder** (`store`,
  `declared`, `bind`, `open`, `account`, `streamOf`, `keyAtCap`, `tail`), the
  **store façade** (`load`, `append`, `batched`, `readStream`, `drain`, `readAll`,
  `walkLog`, `advanced`) and the **verdict sink** (`refuse`, `unable`, `verdict`,
  `walk`, `context`) as three values with three sentences. Round 1 took the
  parameter breaches from four to two (GAP-4, GAP-9); round 2 took them to zero
  and the fields from eight to seven (GAP-17, GAP-22), and split the file in two
  to stay under 400 lines. What is left is the lines-per-class figure and the
  method count, which the three-way split is what pays. `[medium][deferred]`
- **GAP-15 — `cancellation`'s uncertainty window is driven with one context
  sentinel.** `stores.go`'s `unconfirming` always answers
  `Failure(Unconfirmed, context.Canceled)`, so the `DeadlineExceeded` arm of the
  assertion beside it can never be true. The bare-cancellation window *is* driven
  with both. GAP-135 widened the suppression to the whole cause, so both sentinels
  go through one branch of `event/errors.go` and the untested arm cannot differ
  from the tested one — a coverage residue rather than a hole. Closing it is a
  second `unconfirming` value, or a note on the assertion saying which arm is
  redundant. `[low][deferred]`
- **GAP-16 — three small duplications and two dead struct fields.**
  (a) `defects_test.go`'s `factoriesFor` dispatches the two store-shaped defects on
  their **prose names**, where the `defect` row could carry what builds its store;
  it fails loudly (`t.Fatalf`) when a name stops matching, so it cannot silently
  drift. (b) `report.go`'s `certified` and `export_test.go`'s `Certified` are two
  bodies for one question. (c) `suite.go`'s `missing` table gives the `Persistence`
  and `MonotoneVisibility` rows a `hook` of `""` and an `absent` of `false`, so the
  `Supported && absent` branch is unreachable for them and the table's shape says
  it rather than its zero values. None can produce a wrong verdict.
  `[low][deferred]`

**From S5's implementation review (round 3), the findings it graded
`[deferred]`:**

- **GAP-27 — `payload ownership` writes `max(StreamPage, MaxRead) + 1` events
  with no ceiling.** The count is derived rather than fitted and is the genuine
  minimum for the property — a read must answer a page and not a history — but a
  store publishing `StreamPage` 4 096 and `MaxBatch` 1, both legal, is asked for
  4 097 appends inside one `sectionWindow`, which at a 5 ms network append is the
  whole window. The remedy already exists and is the store's (`Factory.Window`);
  narrowing the count is impossible here because the property under test is what
  the **store's own** slice does. What is missing is that nobody is told, and two
  of the three places they should be told now say it: the derivation in
  `sections_ownership.go` states the cost and names `Factory.Window`, and this row
  is what **phase 2 reads before it meets it**. What is left is the third — a
  refusal that names `Factory.Window` when the section's own writes are what ran
  out of the window, rather than reporting the append. `[medium][deferred]`
- **GAP-30 — `Factory.Tail`'s answer is trusted.** A hook that answers a cursor
  minted over another value's backing makes the first `readAll` refuse with the
  store's name on it; one answering `""` degrades silently to the full scan the
  hook exists to avoid. It is a factory defect and no in-tree factory has it, but
  `Tail` is a hook phase 2 must implement and it is the one whose wrong answer is
  invisible. `[low][deferred]`
- **GAP-31 — the suite's own key-width reserve is enforced at one door and
  bypassed at seven.** Nothing is wrong today: the whole suite at `MaxKey` 21,
  one byte above its own floor, certifies the same twenty verdicts, because the
  widest directly-built suffix renders to 6 bytes against a reserve of 8. The trap
  is the next case added there. `[low][deferred]`
- **GAP-32 — one published number with two spellings, and a value object read one
  field deep.** `drain` reads `store.Limits().StreamPage` where every other site
  reads `this.limits`; `injectedAtRead` uses one of `fixture`'s six fields.
  Neither can produce a wrong verdict. `[low][deferred]`
- **GAP-33's second half — `persistentFactory`'s captured `shared`** is read and
  written from `New` with nothing around it. The first half is closed:
  `stagingTx.done` is an `atomic.Bool` whose transition is a `CompareAndSwap`
  inside the lock that publishes. `[low][deferred]`

**From this plan's own reconciliation with the tree:**

- **`jobs/queue.go:808:normalizeSenderError` reads its classification with a bare
  type assertion**, so a forwarding decorator that wraps a driver's
  `RejectPlacement(ErrConflict)` with `%w` turns a conflict into `jobs.ErrAmbiguous`
  — one class converted into another by the kernel. Real, live, and **not this
  phase's**. Raise as a finding against `jobs`. `[high][deferred]`
- **`crudsql.Transaction` reaches through a bare type assertion**
  (`executor.(interface{ Tx() *sql.Tx })`) while `crud.IsTransaction` above it walks
  `unwrapSource`, so a decorated executor passes the transaction test and fails the
  extraction. [[D-061]]'s failure in `crudsql`; it decides whether `eventpg` can
  join a transaction behind a decorated executor. `[high][deferred]`
- **`SUBSYSTEMS` omits `jobs`, `cache`, `health`, `runtime` and `otel`**, so
  `check_utils` would not notice a package under `utils/` importing any of them.
  `event` is added by this change; the other five are a separate finding.
  `[medium][deferred]`
- **`crud/executor.go:441:bindingFor`'s `strict` arm** lets §UC-028's
  two-backings-chained control survive by a *type mismatch* rather than by design
  (`KeyOf(db)` is a `*sql.DB` and `KeyOf(tx)` a `*sql.Tx`, so `strict` is false).
  Pin it with a test in phase 2 rather than assume it. `[medium][deferred]`
- **`event` restates `crud`'s nil-by-any-route predicate**, because
  `crud/executor.go:507:isNilValue` is unexported (verified) and
  `crud.SameDataSource` at `:562` correctly answers `true` for two typed nils of
  one type — correct for its own job and useless as a validity test. So
  `NewBacking`, `NewAuthority` and now `Bind`'s nil-store check all read a second
  implementation of one predicate. The change that retires it is `crud` exporting
  it — `crud.ValidDataSource(identity any) bool`, one function over the existing
  `isNilValue` — after which `event/backing.go` deletes its copy. Additive on the
  `crud` side and therefore safe; it is out of scope here only because it is a
  change to a different subsystem's surface. `[medium][deferred]`
- **`event.JSON`'s decode path is bounded in bytes and nothing else** — no
  `MaxDecodedBytes`, no `MaxPayloadDepth`, where `jobs` has both
  (`jobs/bounds.go:22-23`). It is sound under the trust boundary
  `event/store.go` states (the payload cap plus the identifier, revision and
  ordering checks) and it is the assumption phase 2 inherits; a store whose bytes
  are written by something outside this system needs a codec of its own until
  this is closed. D-124 records it. `[medium][deferred]`
- **The second copy of the hardened JSON analyser** (`jobs/json.go`,
  `cache/codec.go`, 20 shared unexported functions, a build-tag pair in three
  directories). Pre-existing; D-124 names the trigger for extracting a shared
  stdlib-only analyser — a fourth consumer, or the first defect fixed in one copy
  and not the other. `[medium][deferred]`
- **Two pre-existing `make integration` failures**
  (`TestOneConfigShapeOpensEveryEngine/postgres`,
  `TestDbpgxOpensAPoolFromTheSameConfig`) about `vvdb` pool settings not reaching
  the handle, and a pre-existing **`make check-tidy`** failure in
  `app/http/appfiber`, `auth/access/accessjwt`, `.../revokeredis` and
  `.../revokeredisfx`. Both present on the clean tree before any change here.
  `[medium][deferred]`
- **C9's unpoliced half** — that a codec, a mapper, a fold, an upcaster and a
  store's clock are safe for concurrent use is stated and not enforced, because a
  deliberately racing fixture would make every run red rather than one section. The
  same standing as §INV-033's injectivity obligation. `[medium][deferred]`
- **A store whose `Capabilities()` or `Limits()` changes after the door is
  unpoliced.** The kernel reads each once at `Bind` and once at `Read` and
  retains it (`event/store.go`), which is what makes an empty append literally
  zero store calls; the contract says both are constant for the store's life and
  nothing checks it. A conformance case cannot: a store that answers honestly on
  the call the suite makes and dishonestly later is indistinguishable from a
  conformant one. Same standing as injectivity. `[medium][deferred]`
- **`eventpg` must document what a refusal leaves the caller's transaction in.**
  The contract says *assume unusable* for everything but `ErrClosed` and
  `ErrRefused` (`event/store.go`), which is PostgreSQL's own rule; `eventmemory`
  is more forgiving and no conformance case may rely on that. `eventpg`'s module
  page states it, and its `## Debt` names the internal-savepoint variant as the
  thing that would let a store promise more — with the cost, one round trip per
  append, and the §INV-027 question it reopens. `[medium][deferred]`
- **There is no rate-limit kind and no 429 anywhere in this framework** —
  `errs/code.go:45-60` declares ten kinds and `port/porthttp/errors.go:27-49`
  maps ten statuses, none of them `http.StatusTooManyRequests` (GAP-145). A
  §UC-048 quota decorator can therefore reach 403, 409, 503 and the other seven,
  and not the status a quota most naturally wants. Adding one is a change to
  `errs` **and** `port` — a second and third subsystem's surface — and is out of
  this phase's scope; it is additive on both sides when somebody wants it.
  `[low][deferred]`
- **`event.Failure` sits beside `jobs/durability.go:81:Failure`** (a `uint8` enum),
  and `event.Aggregate` beside `crud/aggregate.go:51:Aggregate` (an aggregation
  option). Neither is a Go collision and [[D-035]] covers both, but both are worth
  one line in the module page so a reader of both does not misread them.
  `[low][deferred]`
- **[REC] §3.6's fallible mapper** — `cache`'s key encoder returns
  `([]byte, error)` and §D.1's mapper is infallible `func(ID) Key`, so an identity
  that cannot be rendered must render *something* and §UC-051 catches it one door
  later with a refusal that names the family and not the reason. Recommended twice
  and not taken; recorded once more and then dropped. `[low][deferred]`

**From the S1 implementation review, round 3 of the re-audits** (GAP-162 to
GAP-169): GAP-162, GAP-163 and GAP-165 were closed in phase 5 and are not
carried; GAP-167 was closed by phase 5 existing at all. These four were deferred
with their reasons:

- **GAP-164 — the store extension point declares no compatibility note where its
  implementer reads it.** `microkernel.md` requires an extension point to declare
  a contract, a registration mechanism, a failure policy **and** a compatibility
  note; the first three are on the seam in code and the fourth is only in this
  artifact, which `eventpg`'s author in another repository cannot read. The second
  half is a contradiction to settle rather than to copy: §D.14 says *structs grow
  by field*, and a fifth `Capabilities` field is `Unstated` in every shipped
  store's composite literal, which §INV-043 refuses at `Bind` **and** `Read` — so
  the growth rule as written stops every already-shipped store at the door. S6
  writes the rule on `event/store.go` and the decision in `docs/ai/decisions/`,
  beside D-121…D-125. `[medium][deferred]`
- **GAP-166 — `Stream.String()` still lets a store-supplied family forge a second
  bracketed field.** GAP-150 closed the newline route: `checkText` refuses empty,
  over-cap, invalid UTF-8 and control characters, and `]` and `[` are ordinary
  printable runes. Measured, `Stream{Family: "orders] admin logged in [stream x"}`
  renders `"[stream orders] admin logged in [stream x]"` — a forged *field* inside
  one line and not a forged line, since no newline survives the rule. Phase 5
  deliberately wrote no test for it, because a test asserting today's answer
  encodes a known defect as expected; the newline, over-cap, invalid-UTF-8, NUL
  and empty cases are all in `TestEveryRenderingNamesAClassAndNeverAValue`, so the
  bracket case is one row to add once S6 picks `%q` or the wider rule.
  `[low][deferred]`
- **GAP-168 — GAP-154 re-measured at 27.8 %.** The finding is GAP-154's and the
  answer is GAP-154's row above; what round 3 adds is the reason it should be
  settled **while** S6 writes the ADRs rather than before them — the right cut is
  *the obligation stays in the code, the argument moves to the decision*, and the
  decision does not exist yet. Phase 5 added one clause, on the walk, which
  GAP-162 required. `[low][deferred]`
- **GAP-169 — `Envelope.RecordedAt` is checked by nothing while the trust-boundary
  comment claims a store is trusted "not at all".** Nothing in the kernel reads
  it, so no framework decision turns on it; the exposure is a consumer that orders
  or ages by it, and there is no obviously right check — a clock-skew bound is a
  calibrated threshold this framework has no basis to pick. S6 either names the
  field as unchecked and the caller's to distrust or narrows the sentence, and
  states whether the store's injected clock must be UTC and monotone within a
  stream, since `eventtest` cannot certify what the contract does not say.
  `[low][deferred]`

**From the S1 implementation review, round 6** (GAP-157 to GAP-161): **nothing
was deferred.** All five were closed in the section, including GAP-160, which
the reviewer graded `[low][deferred]` on the reasoning that closing it meant
either an `As` arm on the walk or a bounded `errs.AsFault` — a change to another
subsystem's surface. It rides with GAP-158's traversal work by the finding's own
words, and once `faultIn` became `findAs`, the `As` arm was one line inside a
function that was being rewritten anyway and needed no `errs` edit. Deferring it
would have left two answers to one question in a file that freezes at S6.

**From the S1 implementation review, round 5** (GAP-147 to GAP-151 and GAP-153
were closed in the section and are not carried; these four were deferred with
their reasons):

- **GAP-152 — `NewAuthority` admits a value-typed transaction identity.** §INV-028
  derives an authority from *the transaction's own identity, by reference*, and
  claims the wrong shape is structurally excluded; the constructor admits a
  counter, a sequence number, a UUID string or a small value struct, and two live
  transactions carrying equal values compare `Same`. §UC-030's checkable atomicity
  claim is then true when it is false, with no second signal. Not refused at the
  constructor because a value struct naming a connection and a sequence is a
  legitimate identity for a store whose transactions are not pointers, so a
  blanket refusal has a real cost; the plan records the decision at
  `#### event/authority.go`. The remedy is S5's `transactions` section, whose
  `Begin`-twice case asserts two live transactions are **not** `Same` — which
  catches any store that runs the suite with `Transactions: Supported`. What is
  still open is the kernel-side half: stating the obligation on the constructor
  where an implementer reads it, and why address reuse is prevented by the
  retention and only by it. Rides with S5 or S6. `[medium][deferred]`
- **GAP-154 — `event/` is 24 % comment against 0.5 % in `jobs` and 0.6 % in
  `cache`.** Measured: `event/store.go` 85/173, `errors.go` 48/262, 176/729 in
  total. Most of it is contract text §D.14 writes verbatim — the ownership
  obligations, the three-answer `Transaction` contract, the read-shape clauses,
  the two-traversal invariant, the `nilByAnyRoute` rationale — and earns its keep
  under `CLAUDE.md`'s own rule. Two do not: `event/text.go`'s comment restates its
  own four guards before saying the two things that are not obvious, and
  `event/identity.go`'s middle sentence restates `escapePart`. Cheapest as one
  pass at S6, when the contract text and `docs/modules/en/event.md` are written
  together and the duplication between them becomes visible; the plan states there
  which comments are contract text and are therefore expected, so S2–S5 have a
  norm rather than a percentage. Round 6 moved the number again — the request
  class's wrap, the vocabulary gate on `inVocabulary`, the promotion budget on
  `causeAsWrap`, `refusal.As`'s recover and `findAs`'s one-answer rule are five
  more invariants the code cannot make visible, and round 5's `causeAsWrap`
  paragraph came out — so the section is 839 lines. The list stays a list of
  which comments, not a target for how many. `[low][deferred]`
- **GAP-155 — the six kernel ceilings carry no derivation anywhere a reader of
  this repository is told to look.** The §D.13-form table exists in this plan and
  `CLAUDE.md` points every reader at `docs/`, which has no `event` page, flow or
  ADR yet. The row worth naming is `MaxKeyBytes = 2 << 10`, derived from the
  narrowest index tuple among the engines phase 2 targets — a **SQL** limit
  installed as a ceiling in a store-agnostic kernel, which a document store or a
  file-per-stream store inherits with no way to publish a larger `MaxKey`. S6
  already schedules `docs/modules/en/event.md` and its RU page; the table, the
  non-SQL-store answer for `MaxKeyBytes`, and a `scripts/docs_test.go` symbol arm
  over the constants go there. `[low][deferred]`
- **GAP-156 — `Cursor("")` has no stated meaning at either end of `ReadAll`.**
  It is what a caller starting a projection has and what a store returns if it
  forgets to mint one, and three readings are consistent with the contract as
  written: from the beginning, unparseable, and nothing to resume from. Any
  disagreement is loud — a store that refuses `""` fails the first `ReadAll`, one
  that returns `""` fails §UC-039's tiling assertion — so it is a store-author
  trap and not a correctness hole. One clause on `ReadAll`, cheapest written with
  the `resumption` and `global paging` sections in S5, and `resumption` starts a
  read from `Cursor("")` explicitly so the answer is certified. `[low][deferred]`

**From the S2 implementation review, round 1** (GAP-170 to GAP-177): GAP-170,
GAP-171, GAP-172, GAP-173 and GAP-174 were closed in the section and are not
carried. These three were graded `[deferred]` and are here because after S2
nobody will look again:

- **GAP-175 — `Fold` returns a half-applied state through the same signature as
  an untouched one.** Causes 1–3 return the argument untouched; cause 4 — a
  decode or upcast failure mid-list — returns the state as of the last change
  applied, and for a map, slice or pointer state the argument the caller passed
  has already been written through by the folds that ran. The ordinary Go reflex
  on an error (`if err != nil { … }` and keep using the variable you already had)
  therefore keeps a half-rehydrated aggregate. **Kept, deliberately, and the
  argument is §UC-041's own:** an all-or-nothing `Fold` means decoding the whole
  list before applying any of it, which holds *n* decoded application values
  resident at once — the one thing §INV-042 and the whole `Change` design exist
  to prevent — and for a reference-kind state it is not achievable at all,
  because the caller's own map is what the fold writes into and the kernel may
  not copy it. What is owed is not a repair but a **statement**: S6's
  `docs/modules/en/event.md` must say which of the two a caller gets and that the
  argument is unusable after any error, beside `Fold`'s own comment, which says
  it today and is the only place that does. The trigger is narrow in S2 — a
  `Change` from `Fact.New` always carries the current revision, whose `decode`
  runs no upcaster — and widens at S4's `Load`, which is where the reload-is-the-
  authority rule earns its keep. `[medium][deferred]`
- **GAP-176 — a type graph over the walk's node or edge bound is refused as "the
  codec cannot encode its own reader type", and the depth bound is unreachable.**
  Two things. (1) `depth > codecGraphDepth` cannot fire: `visit` returns early
  for a seen position, so reaching depth 1025 needs 1025 distinct positions and
  `len(this.seen) >= codecGraphNodes` fires first. (2) exhausting the node or
  edge bound takes `ErrCodecType` → `ErrDeclaration` and therefore **panics at
  package initialisation** with a diagnosis that names the wrong thing; `jobs`
  answers `ErrTooLarge` for the same condition (`jobs/json.go:1067-1070`). The
  bounds are `jobs`'s own numbers and are not in question — only the
  classification and the dead branch, and the shape needed to reach either is a
  generated model with more than 1024 distinct positions or 4096 field edges
  reachable from one payload. Deferred because no contract depends on it and no
  data is at risk; the repair is a bound refusal that names the bound and the
  remedy, and either deleting the depth branch or adding a case that reaches it.
  Owner: **S6**, with the structural checks. `[low][deferred]`
- **GAP-177 — a recovered codec or upcaster panic discards the panic value.**
  `encodeWith`, `decodeWith` and `upcastTo` each build their refusal with a `nil`
  cause, so `CauseOf` answers `nil` and no message, type or stack of the
  panicking value survives; a store operator holding an `ErrPayload` from a
  hand-rolled binary codec that panicked on a truncated buffer has the sentinel
  and nothing else, and `port.Logger(ctx)` is not reachable from those call
  sites. §INV-025 forbids the text **travelling in the rendered refusal**; it does
  not require the cause to be destroyed, and `refusal.cause` exists precisely so
  a cause can be carried without being rendered. **Not fixed unasked**, because
  `ErrUpcast` wrapping nothing is an explicit plan decision on
  `jobs/upcast.go:71`'s shape and `TestAnUpcasterMayRefuseAndAFoldMayNot` asserts
  `CauseOf(err) != nil` is false for a panicking upcaster — so changing it is a
  contract question for the owner, not a defect. The two codec doors are
  separable from the upcaster and could carry the recovered value while
  `ErrUpcast` keeps its rule; that is the shape to propose. Owner: **S6**, with
  the ADRs. `[low][deferred]`

**From the S2 implementation review, round 2** (GAP-178 to GAP-184): GAP-178,
GAP-179, GAP-180 and GAP-181 were closed in the section and are not carried.
These three were graded `[deferred]`, and they are here because after S2 nobody
will look again:

- **GAP-182 — a change minted on one aggregate folds through a different
  aggregate of the same family and state type.** `Change.decidedFor` compares
  `Stream{Family, Key}` and nothing else, and `apply` is captured from the *fact*
  at `Fact.New` rather than looked up in the folding aggregate's own table — so
  two declarations over one family and one state type in one process fold each
  other's facts, running a fold the second aggregate never declared. It is the
  third crossing, and the only one the compiler does not close: the state-type
  and identity-type crossings are build-failure fixtures. **Deferred and not
  dropped**, because the repair belongs beside S4's `Bind` check 4 (*the family
  is not already bound through this `Binding` to a different declaration →
  `ErrFamily`*), which closes the bound path; the store-free `Fold` door
  (§UC-041) has no `Binding` and therefore no such check, and the two doors must
  not answer differently. The shape needed is a wiring mistake rather than an
  ordinary call, and in S2 nothing is written. Owner: **S4**, with `ErrFamily`.
  `[medium][deferred]`
- **GAP-183 — `reusesItsBuffer`'s second arm accuses a non-deterministic codec of
  aliasing.** Round 2's repair made the *first* arm exact — two answers compared
  for a shared slice or map, which does not re-encode anything — and kept the
  zero-value disturbance behind it, because that is what reaches reuse through a
  `string` leaf whose address `reflect` will not give up without `unsafe`. The
  second arm still assumes `Encode` is a function of its argument, which
  protobuf's Go implementation is explicitly not for a message with a map field;
  such a codec answers `before != after` for a value it never aliased and is told
  it reuses its decode buffer. `RoundTrip` is a declaration-time diagnostic, it
  fails closed, and the cost is a wrong diagnosis rather than data. **The
  symmetric false positive is now the first arm's**: a codec that hands out a
  slice of a table its package keeps for ever — an interned constant, a shared
  empty payload — is two equal addresses and is not the reuse the contract
  forbids. Both are one question: establish that `Encode` is deterministic first,
  and name non-determinism as its own refusal. Owner: **S6**, with the module
  page, which owes the sentence either way. `[low][deferred]`
- **GAP-184 — `ownMethods` refuses a working `MarshalText`/`UnmarshalJSON`
  pair.** The first arm refuses a type whose write route and read route differ in
  either direction, so `MarshalText` on the value receiver paired with
  `UnmarshalJSON` on the pointer receiver is refused as *"writes itself through
  MarshalText and declares no UnmarshalText"* — measured, that pair encodes
  `{"m":"7"}` and decodes back correctly, because the text output is a JSON
  string and `UnmarshalJSON` receives it. It fails closed, at declaration, so
  nothing is at risk; it costs a consumer a workaround for a pair that works.
  Deferred because loosening the arm is a change to what the walk asks, which
  GAP-178 and GAP-179 have just settled, and because the crossing pair is rarer
  than either of the asymmetries the arm exists for. Owner: **S6**.
  The finding's other half — two comments naming an `eventtest` package no tree
  provides — was closed in the section instead: both now name the conformance
  suite and say it has not arrived. `[low][deferred]`

**Two shapes round 2's repairs surfaced and did not close**, recorded so the next
reader does not have to find them again. Neither is a review finding; both were
measured against this checkout while GAP-178 was being repaired:

- **A struct that embeds a type with its own JSON codec is written by that
  codec, and its other fields are dropped.** `type Payload struct{ stamped;
  Amount int }` promotes `stamped`'s `MarshalJSON` and `UnmarshalJSON` to
  `Payload`, so `ownMethods` finds a type whose write and read routes agree,
  stops descending — correctly, by its own rule — and `Amount` is never written:
  measured `{"Meta":"stamped"}`, `Amount` back as `0`, no refusal at any door.
  It is GAP-178's class reached through a promoted *method* rather than a
  promoted *field*, and it is not closed here because `reflect` offers no way to
  ask whether a method is declared on the type or promoted into it; the
  distinguishing question has to be reconstructed from the embedded fields, which
  is a rule of its own and belongs beside a decision rather than inside a repair.
  `Fact.RoundTrip` does catch it, which is why it is `[low]` rather than
  critical: as the fidelity refusal where the promoted codec's output varies with
  its input, and as `ErrSample` — *encodes its sample exactly as its own zero
  value* — where it does not, which is what the fixture above answers. Owner:
  **S6**, with the ADRs. `[low][deferred]`
- **An embedded pointer to an unexported struct type writes and does not read
  back.** `type P struct{ *inner }` with `inner` carrying exported fields
  encodes `{"X":5}` and decodes with *"cannot set embedded pointer to unexported
  struct type"* — a poison pill of GAP-171's exact class, through a shape no arm
  of the walk asks about. Not closed unasked: it is three lines in
  `members.collect`, and it is exactly the kind of unrequested arm whose false
  positive (the same embedding contributing no member at all) nobody would have
  reviewed. Owner: **S6**. `[low][deferred]`

**Both shapes above were raised as findings by round 3** — GAP-185 and GAP-187 —
and both are **closed** in the section, in the spelling their close criteria
asked for. They leave this list; the two paragraphs stay because they are the
measurement the repair was written against.

**From the S2 test review, round 1** (GAP-T20 to GAP-T33): GAP-T20 to GAP-T27 and
GAP-T29 were closed in the section and are not carried. These five were graded
`[deferred]`, and they are here because after S2 nobody will look again:

- **GAP-T28 — §UC-052's own named control, a state holding a `time.Time`, is
  absent.** The subtest reaches three of the seven shapes [SPEC] enumerates. The
  property holds today *by construction* — the kernel walks no state type at all
  — so nothing is broken; what is missing is the regression barrier against the
  copier walk §UC-052 deleted being reintroduced, which is the reason the control
  was written into the use case. Owner: **S4**, whose `Load` and `Bind` are the
  first doors a state type crosses that could grow such a walk.
  `[medium][deferred]`
- **GAP-T30 — `Fold` with an empty change list is asserted by nothing**, so a
  short-circuit that skips the key check survives. §UC-041's cause 1 states no
  exception for an empty list, and the mirror property at the other door is a
  planned S4 case (`TestAnEmptyAppendChecksTheKeyAndNothingElse`, §INV-031), so
  the two doors would disagree. Owner: **S4**, beside that case, so the pair is
  written once and reads as one rule. `[low][deferred]`
- **GAP-T31 — the round-trip fidelity cases read the real clock.** Three
  `time.Now()` calls construct the monotonic-reading and local-zone samples. Not
  flaky in practice — `time.Time.Equal` ignores both and RFC3339Nano round-trips
  losslessly, and `-count=5 -shuffle=on` found nothing — but the property under
  test needs a *stated* instant rather than an incidental one. Owner: **S6**,
  with the doc-and-surface pass. `[low][deferred]`
- **GAP-T32 — the concurrency test never crosses an upcaster**, which C9's clause
  names among the callbacks called from every request goroutine. A `Change`
  always carries the current revision, so every concurrent decode goes through
  the current link; four of C9's five callbacks are driven and the upcaster is
  the fifth. Owner: **S5**, where C9 is checkpointed with the conformance suite's
  own concurrency section. `[low][deferred]`
- **GAP-T33 — a marker fact is on the accepted table and can never pass
  `Fact.RoundTrip`.** The test half of GAP-190, and it moves with whatever that
  decides: once a `struct{}` payload has an answer, one case drives the accepted
  shape through the second door and asserts it. Owner: **S5**, with GAP-190 and
  the `*testing.T` wrapper. `[low][deferred]`

**From the S3 implementation review, round 1** (GAP-1 to GAP-12 of
`EVENTSOURCE_P1_S3_GAPS.md`): GAP-1 to GAP-7, GAP-9 and GAP-10 were closed in the
section — the six `[immediate]` ones because they are what the section is
answerable for, and GAP-7, GAP-9 and GAP-10 because each was one line inside a
file the closures were already rewriting. These three were deferred with their
reasons:

- **GAP-8 — an empty `AppendRequest` is admitted without checking `Expected`.**
  `Append` short-circuits on `len(req.Records) == 0` before the version check, so
  `Append(Expected: 999)` on a stream at version 1 answers nil, which reads as
  *the stream was at 999*. Unreachable through the kernel — S4 short-circuits an
  empty append before step 2 — but `Store` is directly callable and `eventtest`
  may issue one. The fix is a sentence on `event/store.go`'s `Append`, not code:
  either an empty batch decides nothing and a store may skip the check, or every
  store must issue the check anyway, which costs `eventpg` a round trip for a
  no-op the kernel never sends. **S5** owns it, because the suite is what would
  otherwise encode whichever answer this store happens to give — and until it is
  written down, no conformance case may issue an empty append. `[low][deferred]`
- **GAP-11 — three zero-value behaviours a consumer cannot see from the type.**
  A nil `Spec.Clock` becomes `time.Now`, every operational `int` at zero becomes a
  default, and `Check` answers `ctx.Err()` before it answers closure. The plan's
  contract block now states all three; what is left is the consumer-facing half,
  `docs/modules/{en,ru}/event.md`, which the house comment rule keeps out of the
  struct. Owner: **S6**. `[low][deferred]`
- **GAP-12 — no documentation exists for a package that is complete.**
  `grep -rln "eventmemory" docs/` is still zero files: no module page, no flow, no
  reverse-index row, none of the five ADRs. Scheduled in **S6**, which names each
  file; recorded here so the S3 gate does not read as evidence that `CLAUDE.md`'s
  same-change obligation was met. `[medium][deferred]`

**From the S3 test review, round 1** (GAP-T1 to GAP-T18 of
`EVENTSOURCE_P1_S3_TEST_GAPS.md`): GAP-T1 to GAP-T8 — every `[immediate]` one,
three `[critical]`, four `[high]` and one `[medium]` — were closed in the
section, with nine mutations applied and killed. These ten were graded
`[deferred]`, and each names the section that owns the assertion, because after
S3 nobody will look at this package again:

- **GAP-T9 — `Rollback`'s "reads no deadline" half is asserted nowhere**, though
  `Commit`'s is: adding `ctx.Err()` to `Rollback` survives the suite. The hazard
  is the one `transaction.go:77-80` and `doc.go:16-20` both state — a rollback
  that refuses under the cancelled context of the request that is unwinding
  leaves its claims held for the life of the process, and every later append to
  those streams is a conflict that never clears. The code is correct today and
  the fix is one assertion in an existing subtest. Owner: **S5**, whose
  `transactions` section runs the same assertion against every store rather than
  against this one. `[medium][deferred]`
- **GAP-T10 — what position a staged envelope reads back at is asserted
  nowhere**, so setting it to `log.position + 1` survives. `read.go:11-12`
  decides it (a staged envelope carries none, because a position is assigned at
  commit) and §S3's **Decides** paragraph names it among the nine. Narrow today:
  §UC-053's cursor comes from `ReadAll`, which returns no staged envelope at all.
  Owner: **S5**, because what a staged read answers is a store-contract question
  every store must answer identically. `[medium][deferred]`
- **GAP-T11 — the commit-time re-validation and `errStaleClaim` have no test and
  can be deleted outright.** §S3 states plainly that `errStaleClaim` is
  unreachable while the claim mechanism holds, which is the point of it: this
  section therefore **records `revalidate` as an unreachable assertion and
  "gut `revalidate` to `return nil`" as a known-surviving mutation**, rather than
  as an untested branch. What is left is the reachable arm — a defect fixture
  that narrows the claim and watches the net fire. Owner: **S5**, with the
  suite's defect fixtures. `[medium][deferred]`
- **GAP-T12 — the 200-round transaction race asserts nothing about how often
  either arm was refused**, so every round could return `nil` from both the
  append and the read and the test would pass. Its real content — `-race` plus
  the byte-identity of the refusal when one occurs — is sound; what is missing is
  the evidence that the interleaving it is named for ever happened, which
  `CLAUDE.md`'s "put a control case next to any test that could pass vacuously"
  is exactly about. Owner: **S5**, beside C9's concurrency checkpoint, where the
  same counting control covers every store. `[medium][deferred]`
- **GAP-T13 — `Append`'s empty-records branch is dead**, and its position
  relative to the ambient check is unpinned: both making it answer a conflict and
  moving it above the ambient resolution survive. It is the test half of GAP-8
  above and moves with whatever that decides, which is why the two share an
  owner: until the contract says whether an empty batch decides anything, no case
  may issue one. Owner: **S5**. `[medium][deferred]`
- **GAP-T14 — the goroutine-leak test measures a process-global count**, so its
  strength depends on which test ran before it: under `-shuffle=on` a preceding
  many-goroutine test can inflate the baseline and `settled(before)` returns on
  its first poll. Every one of those goroutines is joined by a `WaitGroup`, so
  this is a strength dependency rather than a flake — five shuffled runs and
  three `-count=3` runs are green. The fix is a second baseline taken by the same
  method and asserted equal to the first. Owner: **S6**, the pass that last
  touches this tree. `[medium][deferred]`
- **GAP-T15 — the persistence subtest's assertion cannot fail for the reason its
  message gives.** Opening a fresh log and reading it empty proves two logs are
  two backings, which two other subtests already pin; non-persistence has no
  in-process falsifier, so the honest evidence is the capability value plus
  `doc.go:33`. Cosmetic: the capability value itself is asserted and the claim
  mutation dies. Owner: **S6**. `[low][deferred]`
- **GAP-T16 — `Check`'s ordering of the deadline and the closure is unspecified
  and untested**, and `Append`, `ReadStream`, `ReadAll` and `Begin` all make the
  same choice. Both answers are defensible; it matters only because §UC-057 and
  §INV-029 make cancellation identity load-bearing. It is the test half of GAP-11
  above and moves with it. Owner: **S6**. `[low][deferred]`
- **GAP-T17 — every key in the suite is short ASCII**, so `MaxKey` and the key's
  own edges — empty, exactly at the bound, multi-byte, and carrying the cursor
  separator `":"` — are untouched. The payload side is fuzzed; the key side is
  not. Low, because the kernel validates the key before the store sees it (§D.14)
  and the store keys its maps by `event.Stream`, so a separator-bearing key is
  structurally safe — which is why one cheap table should say so. Owner: **S5**,
  where a store must round-trip whatever key the kernel admits. `[low][deferred]`
- **GAP-T18 — §S3's `Covers` names UC-040 and no aggregate is declared anywhere
  in the package.** Every test builds `event.Record` values by hand, so the
  ergonomic claim §UC-040 makes — an aggregate unit-tested against this store
  with no database and no fixtures — is asserted at S5 and nowhere earlier. The
  coverage matrix already routes UC-040's checkpoint to S5, so this is
  bookkeeping rather than a hole: either S3's `Covers` drops it or one test in the
  package drives a declared aggregate through this store. Owner: **S5**, where the
  aggregate seam and the store meet by design. `[medium][deferred]`

**From the S4 implementation review, round 1** (GAP-1 to GAP-6 of
`EVENTSOURCE_P1_S4_GAPS.md`): GAP-1 and GAP-2 — the two `[immediate]` ones —
were closed in the section as the two comment repairs §S4 records. These four
were graded `[deferred]`:

- **GAP-3 — `admitLimits` and `admitCapabilities` mirror the `Limits` and
  `Capabilities` structs by hand**, which is the default §INV-043 says cannot
  happen: a sixth `Capabilities` field not added to the table defaults silently
  to `Unstated` and every store is admitted with it unstated, and a sixth
  `Limits` field is an unenforced bound. Not silent end to end — `store_test.go`'s
  `declaredValues` table fails on a new field — so it is a keep-in-sync defect on
  the mechanism a stated invariant claims generalises, not a present wrong answer.
  The repair is either a `reflect` walk that derives the field list, or a test
  pinning each table's row count to `NumField()`, plus a fixture that adds a field
  and shows the check fail. Phase 2 is the first realistic occasion for a new
  field. Owner: **S6**, with the structural checks. `[medium][deferred]`
- **GAP-4 — `At[S]` and `Commit` marshal to `{}` in silence while `Authority`
  refuses.** §INV-015 is satisfied literally — no exported field, no unmarshaller
  — and it fails closed: a token that made a JSON round trip comes back zero and
  `Append` refuses it at §D.5 step 1 before any store call. The cost is DX. The
  refusal a developer sees is *"the identity does not render a legal stream key"*,
  rendered 400, for a mistake that is "an at-token is not transportable", and
  `{"token":{}}` in a response body gives no hint at the point the mistake was
  made. `Authority` already carries the sentence that would have said so. Either
  a `MarshalJSON` that refuses on both, or the plan records why `Authority` was
  treated differently. Additive; nothing depends on the current behaviour.
  Owner: **S6**, with the ADRs. `[low][deferred]`
- **GAP-5 — `replay`'s page loop has no `ctx.Err()` guard.** It terminates only
  on a short page or a store error; progress is strict, so it cannot spin on one
  page, but a store that both ignores the context and keeps answering full,
  correctly-versioned pages holds the request goroutine inside the kernel until
  `Version` overflows. Everywhere else the kernel treats a store's answers as
  untrusted; here it trusts the store to honour a deadline. It needs a store that
  ignores `ctx` **and** lies about the end of a stream — `eventmemory` checks
  `ctx.Err()` on every `ReadStream` and any real driver does — and the belt is one
  line. Either the check plus a fixture that runs away without it, or the plan
  records that the store alone owns cancellation on this path. Owner: **S6**.
  `[low][deferred]`
- **GAP-6 — comment weight, and one comment that is a second copy of the code it
  sits on.** `event/` measures 24 % comment lines against 14 % in `tenancy/`, 3 %
  in `storage/` and 0 % in the five older subsystems; S4's own files run from
  `binding.go` at 13 % to `marker.go` at 37 %, and the longest function in the
  section is `Append` at 36 lines against `CLAUDE.md`'s 40-line gate. Most of it
  earns the escape hatch — `marker.go`'s resolve-by-backing argument and
  `repo.go`'s short-page decision are not recoverable from the source. **One does
  not:** `Repo.Append`'s numbered six-step list restates the six code blocks below
  it line for line, which is an order kept in sync by hand and was already out of
  sync once, which is that review's GAP-1. Either the list goes or the plan names
  what keeps the two in sync; the density figure rides with GAP-154. Owner:
  **S6**. `[low][deferred]`

**From the S4 test review, round 1** (GAP-1 to GAP-8 of
`EVENTSOURCE_P1_S4_TEST_GAPS.md`): GAP-1, GAP-2, GAP-3, GAP-4, GAP-7 and GAP-8
were closed in the section — the `[immediate]` one because it is what the section
is answerable for, and the other five because each was a row in a table the
closure was already opening, or a plan edit. These two were deferred with their
reasons, and both are here because after S4 nobody will look at this seam again:

- **GAP-5 — the whole caller seam is proven against one hand-written fixture and
  no real store.** `Repo.Load`, `Repo.Append`, `Repo.Within`, `Repo.Authority`,
  `Bind` and `Read` have never run against `event/eventmemory`, the one complete
  implementation this phase ships; the store contract is encoded twice, in
  `repo.go` and in `recordingStore`, and where the two agree wrongly the suite is
  green and the real store disagrees at runtime. The plan already assigns the
  crossing to S5, whose `binding`, `stream paging` and `expected version`
  sections run over `eventmemory` — what is recorded here is that at least one of
  them must drive `event.Open` → `Bind` → `Load` → `Append` → `Read` rather than
  the `Store` surface directly, and that deleting `Repo.checkPage`'s length check
  must turn an S5 section red as well as `TestAnOverLongPageIsRefused`. Owner:
  **S5**. `[medium][deferred]`
- **GAP-6 — `*Repo` is documented safe for many goroutines and nothing exercises
  it.** Adding a mutable field to `Repo` and writing it in `Load` survives
  `go test -race ./event/...`, because no test calls anything on one `*Repo` from
  two goroutines. §INV-038's own *falsified by* clause names the missing test, and
  the row it certifies is the one a consumer most relies on: a process-lifetime
  repository shared by every request. The plan assigns it to S5's `concurrency`
  section ("many goroutines, one repository, `-race` clean"); carried so the
  assignment is checked rather than assumed. Owner: **S5**. `[medium][deferred]`
