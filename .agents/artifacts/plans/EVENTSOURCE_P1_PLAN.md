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

*Runnable proxy* (this is what makes it not a sentence): `Fact.RoundTrip`
decodes the caller's payload for a revision, re-encodes the result and keeps the
bytes, decodes a **second, different** payload of the same revision, re-encodes
the first result again, and refuses if the two encodings differ. `event.JSON`
passes; a scratch-reusing codec fails. **S1** (the clause) + **S2** (the proxy).

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
refusal is what stops the zero sample from reading as a pass.

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
obeys it) + **S5** (the case). *Test:* `payload ownership` appends **one byte**
to page one's **first** payload and asserts the **second** envelope's payload is
unchanged; and the same for the `[]Envelope`.

### C8 — GAP-107 `[medium]` One defect count, one fixture inventory

**Decided: the inventory lives in code, not in five prose sites.**
`event/eventtest/defects.go` holds one slice of `{name, section, build}` and the
suite iterates it; a test asserts **every defect fails the section named for it**
and that every section a defect names exists in the suite. The count is
**twelve**: [SPEC]'s six, plus the pooling decorator §INV-021's falsification
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
kernel belongs rather than in a store's fixture. A
count in prose is what drifted; a count a test computes cannot, and the twelve
above is the enumeration rather than a total to keep in step.
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
| 1 | **The sealing readers are exhaustive and enumerated in code**: one unexported `seal()` on the declaration, called from `Bind`, `Aggregate.Fold`, `Fact.New`, `Fact.RoundTrip`, `eventtest.Keys` and `eventtest.Families`. A test enumerates the six call sites and fails when a seventh reader appears without a row (§UC-005, §INV-013). **S2 correction:** the last two live in another package and cannot call an unexported method, so they seal through the two exported methods they call — `Aggregate.Family` (also `Bind`'s) and `Aggregate.Key`. The six `seal()` call sites inside `event` are therefore `Aggregate.Family`, `Aggregate.Key`, `Aggregate.Fold`, `Fact.New`, `Fact.RoundTrip` and `Bind`; S2 delivers five and S4 adds `Bind` to both the code and `TestTheSealRefusesALateFact`'s table. `Fact.Name` and `Fact.Revisions` read the fact's own immutable chain and not the aggregate's table, so they do not seal |
| 2 | **§UC-068's `Ledger` fold is the guarded one** — `if this == nil { this = Ledger{} }` — and the case states the non-nil precondition its "folding twice applies every event twice" claim needs. Written that way in the module page's example and in the test |
| 3 | **`MaxKey` is step (1) of `Append`'s order, with the kernel's text rules.** `Limits()` is **read once at `Bind` and retained**, so step (1) makes no call on the store at all and the whole key check is one step before anything else; step (4) is the payload byte cap, the batch count and the batch's actual bytes. `Load`'s order is unchanged: (1) mapper + text rules + `MaxKey`, (2) `Transaction(ctx)`, (3) the paged `ReadStream` |
| 4 | **On a closed store `Within` answers the `Transaction(ctx)` question and nothing else**: with nothing bound it is `ErrNoTransaction`; with a transaction of this store's backing bound it **succeeds**, and the next `Load` or `Append` is `ErrClosed`. `lifecycle` asserts both |
| 5 | **`Repo.Authority(ctx)`** returns the store's answer for this context: the valid authority when a transaction of this store's backing is bound; the **invalid** authority and a nil error when nothing is; `ErrAmbientNotTransaction` when something of this store's that is not a transaction is. On a closed store: the invalid authority, nil error |
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
upcasters to `V` — plus that revision's own `selfEncode`/`selfDecode` over its own
type and its type name, all built by generic closures so **no `reflect` is
needed**. Decoding is **once into the revision's reader type and a typed value
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

`Declaration` has exactly two readers in phase 1 — `eventtest.Families` and the
`Binding`'s bound-family set — and no third is invented for it.

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

**Three S2 clauses the contract needed and did not have.** (1) The second decode
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

`RoundTrip` lives on `*Fact` rather than in `eventtest` because the algorithm is
the kernel's and needs the erased per-revision readers; `eventtest.RoundTrip` is
the `*testing.T` wrapper that reports it, and its row on the module page says
which of the two properties it proved — **fidelity** for every sample, and
**non-aliasing** only where the sample carried data — so a store author reading
a green run knows what it did not test. `Revisions()` is §INV-010's falsification
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
   assigns to every other dishonest store rather than a nil-interface panic
2. the family is not already bound through this `Binding` to a *different*
   declaration → `ErrFamily`
3. `Backing()` is valid → `ErrWrongStore`
4. `Limits()` has no zero field, none above its kernel ceiling, and neither
   **read** product — `StreamPage` and `MaxRead`, each against
   `MaxResidentBytes / MaxPayload` — above the ceiling → `ErrWrongStore`. The
   answer is **retained**; the append path's resident bound is the measurement at
   step 4 and not a third product (`event/bounds.go`)
5. `Capabilities()` has no `Unstated` field → `ErrWrongStore`. Retained too

No codec walk, no reflection, no lock — which is what makes a store per request
over a borrowed tenant lease ordinary (§UC-055).

Checks 1, 3, 4 and 5 are **store-honesty checks and they run at both doors**,
`Bind` and `Read` (§INV-022). The family check is `Bind`'s alone.

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
4. the retained `MaxPayload` per record, the retained `MaxBatch` count, and the
   **sum of `len(Record.Payload)`** against `MaxResidentBytes` → `ErrTooLarge`
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

**`Within` returns a context**, not a bound repository and not an inner store —
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
(§INV-022). **No
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
	MaxKey     int
}
type Log struct{ /* opaque */ }
func NewLog(spec LogSpec) (*Log, error)

type Spec struct {
	Log        *Log              // required; two stores over one Log are one store
	Clock      func() time.Time  // retained and called on every append, from every goroutine (C9); its panic is not recovered
	MaxBatch   int
	StreamPage int
	MaxRead    int
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
func (this *Store) Check(ctx context.Context) error

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
those streams with immediate conflicts. `eventmemory` is not a production store
and `Persistence: Unsupported` says so; a store that waits instead is equally
conformant. `Close` is idempotent, returns nil every time, refuses nothing, and
neither commits nor rolls back staged work (§INV-032).

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

**`Tx.Commit`'s error contract, because the suite has to know what to do with
one.** A commit-time error is the **store's own**, of the store's own class, and
the suite never asserts an `event` sentinel on it — a conflict arrives from
`Append` and nowhere else (`event/store.go`). So the suite treats a non-nil
`Commit` in any case but the deliberate double-finish as a **failure of that
case**, reported with the store's error text, and it has exactly one case that
expects a non-nil one: committing a `*Tx` that was already finished.

**Three words, never two** — *passed*, *not certified*, *failed*. `cachetest`'s
`t.Skip` at **seven** sites (`cache/cachetest/suite.go:214, 250, 1508, 1674,
1697, 1715, 1721`, counted) is the shape §UC-044 refuses and is deliberately not
copied.

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

*Test:* `TestEverySectionInTheInventoryWasReported` (S5, `package eventtest`, so
it can reach the unexported runner) runs the suite against the trivial store and
asserts every inventory name was reported **exactly once** with one of the three
words. Its control is a run over the inventory with one section removed, which
must **fail** the same assertion — otherwise the test proves that a list was
iterated and not that the sections exist. The fixture stores stay in
`package eventtest_test`, where §INV-019's compile-time proof needs them; both
files compile into one test binary, so both are visible to `go test -list`.

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

**Both blocks are executed and green.** The phase-5 suite is nine tests in six
files — `event/declaration_test.go`, `seal_test.go`, `fold_test.go`,
`roundtrip_test.go`, `upcast_test.go`, `crossings_test.go`.

**Delivers** everything an application declares and everything it can do with **no
store at all** — declare, mint a change, fold, round-trip. The kernel runs with
zero extensions registered, which is `microkernel.md`'s own requirement and
§UC-041's use case.

**Files** `event/codec.go`, `chain.go`, `aggregate.go`, `fact.go`, `change.go`,
`seal.go`; `event/testdata/crossings/` (build-failure fixtures, three packages:
`control`, `change`, `identity`).

**Realises** `Codec[V]`, `JSON`, `Chain`/`From`/`Then`, `Aggregate`/`Define`/
`TryDefine`/`Family`/**`Key`**/`Fold`, `Declaration`, `Fact`/`Declare`/
`TryDeclare`/`New`/`Name`/`Revisions`/`RoundTrip`, `Change`.

**Carried gaps** C1 (the `Codec` clause, the `RoundTrip` proxy, the zero-value
second payload and `ErrSample`), C9 (the codec and mapper clauses where an
implementer reads them, and the panic policy for all three codec methods),
C10.1 (`seal()` and its six enumerated readers), C10.2 (the guarded `Ledger` fold
in the example).

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
- **`Fact.RoundTrip`'s three clauses** — the second decode is the revision's own
  `selfDecode`, the aliasing verdict is `ErrPayload`, and every codec output the
  algorithm keeps is cloned. See `#### event/fact.go`. The third is the one the
  mutation campaign found: a codec that reuses its **encode** buffer — which
  §INV-021 hand-off 1 permits in terms — defeats the whole proxy without it.
- **The phase-5 name list moves by one in each direction.**
  `TestComposeRendersALegalKeyAndNeverCollides` is **dropped**: S1 shipped
  `TestComposeRendersTheFrozenKey` (a twelve-row golden table, including the
  separator, escape, NUL, invalid-UTF-8 and multi-byte parts this test named) and
  `FuzzComposeRendersAKeyThatIsLegalAndReversible` (legality and injectivity by
  reversibility over an unbounded domain), so a third Compose test would assert a
  strict subset. `TestAnUpcasterMayRefuseAndAFoldMayNot` is **added**, because
  S2's own **Degradation** paragraph states the upcaster/fold panic asymmetry
  (§INV-017, §UC-067) and nothing in the given list reached it. Count stays nine.
- **§UC-050's control reads "the helper length-prefixes"**, which the plan's own
  `Compose` (escaping, `#### event/identity.go`) superseded. Under escaping the
  two legal spellings of a **single-part** identity are byte-identical unless the
  part carries `/`, `%`, a control byte or invalid UTF-8, so the test pins the
  pair on an identity containing a separator. §INV-005's freeze is unaffected and
  the trap is the same one. S6's [SPEC] edit list carries the wording.

**Checkpoint S2 — phase 4 (implementation), executed 2026-09-07**

```
$ go build ./... && go vet ./event/ && test -z "$(gofmt -l event)" && go test -race -count=1 ./event/
ok  	github.com/frostgrove/vv/event	1.178s
EXIT=0

$ go list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' ./event/ | sort | paste -sd, -
github.com/frostgrove/vv/crud,github.com/frostgrove/vv/errs,github.com/frostgrove/vv/event,github.com/frostgrove/vv/utils
```

The import graph is unchanged from S1 — no new first-party or third-party
dependency, and `encoding/json`, `encoding`, `reflect` and `sync` are the only
standard-library imports S2 adds. The section is **647 lines** across six files,
the longest `event/codec.go` at 205; comment density is 118/647 = 18 %, below
S1's 24 %, and every comment is contract text an implementer needs: the codec's
three obligations and the panic policy for every extension point, the chain's
revision-is-position rule, the mapper's injectivity and concurrency clause,
`Fold`'s consumption contract and its four causes, the `Change`'s retention rule,
`RoundTrip`'s two properties, and the seal's enumeration.

**Checkpoint S2 — phase 5 (tests)**

```
test "$(go test -list '^(TestADeclaration|TestEveryMalformedDeclarationPanicsAndTryDefineReturnsIt|TestTheSealRefusesALateFact|TestFoldRefusesAnotherInstance|TestAReferenceKindStateFoldsWithoutAliasing|TestAChangeRetainsNoApplicationValue|TestACodecThatDecodesIntoAReusedBufferIsCaught|TestAnUpcasterMayRefuseAndAFoldMayNot|TestTheCrossingsThatMustNotCompile)$' ./event/ | grep -c '^Test')" = 9 && go test -race -count=1 -v -run '^(TestADeclaration|TestEveryMalformedDeclarationPanicsAndTryDefineReturnsIt|TestTheSealRefusesALateFact|TestFoldRefusesAnotherInstance|TestAReferenceKindStateFoldsWithoutAliasing|TestAChangeRetainsNoApplicationValue|TestACodecThatDecodesIntoAReusedBufferIsCaught|TestAnUpcasterMayRefuseAndAFoldMayNot|TestTheCrossingsThatMustNotCompile)$' ./event/ && go test -race -count=1 ./event/
```

**Checkpoint S2 — phase 5, executed 2026-09-07**

```
$ go test -list '^(…the nine names…)$' ./event/ | grep -c '^Test'
9
$ go test -race -count=1 -run '^(…the nine names…)$' ./event/   ok  …/event  1.148s
$ go test -race -count=1 ./event/                               ok  …/event  1.181s (twice)
$ go vet ./event/...                                            EXIT=0
$ gofmt -l .                                                    silent
$ go build ./...                                                EXIT=0
$ make check                                                    nine arms, all ok
$ make unit                                                     EXIT=0, zero FAIL lines
```

**The section checkpoint, run verbatim as handed over:**

```
$ go build ./... && go test -race -count=1 ./event/ && go test -race -count=1 -v -run 'TestADeclaration|TestTheSealRefusesALateFact|TestFoldRefusesAnotherInstance|TestACodecThatDecodesIntoAReusedBufferIsCaught|TestTheCrossingsThatMustNotCompile' ./event/ && test -z "$(gofmt -l event)"
ok  	github.com/frostgrove/vv/event	1.178s
--- PASS: TestTheCrossingsThatMustNotCompile (0.12s)
--- PASS: TestADeclaration (0.00s)                          8 subtests
--- PASS: TestFoldRefusesAnotherInstance (0.00s)             7 subtests
--- PASS: TestACodecThatDecodesIntoAReusedBufferIsCaught (0.00s)  3 subtests
--- PASS: TestTheSealRefusesALateFact (0.01s)                2 subtests
ok  	github.com/frostgrove/vv/event	1.150s
EXIT=0
```

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

### S3 — `event/eventmemory`: a complete, transaction-capable store  `[ ]`

**Delivers** the second implementation, so that S4's caller seam is exercised
against a real store rather than a double. Its conformance proof lands in S5;
this section's evidence is its own package tests.

**Files** `event/eventmemory/doc.go`, `log.go`, `store.go`, `append.go`,
`read.go`, `transaction.go`, `cursor.go`.

**Realises** `LogSpec`/`Log`/`NewLog`, `Spec`/`Store`/`New`, the eight contract
methods, `Check`, `Tx`/`Begin`/`WithTransaction`/`Commit`/`Rollback`.

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
`### event/eventmemory/`.

**Covers** UC-006 (the constructor half), UC-008, UC-022 (admission), UC-028
(the store's `Tx`), UC-029 (rollback), UC-037, UC-040, UC-044, UC-047, UC-053
(the cursor), UC-054, UC-055, UC-057 (the two windows); INV-002, INV-003,
INV-009, INV-013 (nothing started, nothing global), INV-014, INV-021 (hand-offs 4
and 7), INV-032, INV-035, INV-041 (the store's half).

**Degradation** `Persistence: Unsupported` — everything is lost on process exit,
which the capability states rather than implies. `Close` releases what the store
opened and nothing else; it commits nothing and rolls nothing back.

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
```

---

### S4 — the caller seam: binding, repository, token, receipt, reader  `[ ]`

**Delivers** load / decide / append / read, the transaction seam, every bound,
every door check and every refusal a caller can reach.

**Files** `event/binding.go`, `repo.go`, `marker.go`, `token.go`, `reader.go`;
`event/status_test.go` — the one test file in this phase that imports
`port/porthttp`, which is a **test** import of `event` and therefore invisible to
`go list -deps` without `-test` (C5).

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
test "$(go test -list '^(TestAFreshStreamLoadsAsZero|TestAStreamWithHistoryFoldsToItsCurrentState|TestAppendRefusesInItsStatedOrder|TestAForgedTokenIsRefusedBeforeAnyStatement|TestAnEmptyAppendChecksTheKeyAndNothingElse|TestOneLoadAndOneAppendMakeExactlyTheStoreCallsTheContractNames|TestWithinComposesForTwoBackings|TestWithinAnswersTheStoresTransactionQuestion|TestRepoAuthorityIsTheComparisonTwoSubsystemsUse|TestAMisPagedStreamIsRefusedBeforeItIsFolded|TestAnOverLongPageIsRefused|TestALoadThatFailsMidStreamReturnsNothing|TestEveryHistoryClassRefusalIsRaisedByTheThingItNames|TestBothDoorsCheckTheStore|TestAStoreWhoseProductExceedsTheResidentCeilingIsRefused|TestAnAppendWhoseActualBytesExceedTheResidentCeilingIsRefused|TestAnOutcomeOutsideTheVocabularyTakesTheFailSafeDefault|TestOneAppendCarriesTwoIdenticalChanges|TestAFoldPanicUnwindsOutOfLoad|TestAPanickingCodecBecomesTheRefusalItsErrorWouldHaveBeen|TestARequestClassRefusalRendersAClientStatusAndAHistoryClassOneDoesNot)$' ./event/ | grep -c '^Test')" = 21 && go test -race -count=1 -v -run '^(TestAFreshStreamLoadsAsZero|TestAStreamWithHistoryFoldsToItsCurrentState|TestAppendRefusesInItsStatedOrder|TestAForgedTokenIsRefusedBeforeAnyStatement|TestAnEmptyAppendChecksTheKeyAndNothingElse|TestOneLoadAndOneAppendMakeExactlyTheStoreCallsTheContractNames|TestWithinComposesForTwoBackings|TestWithinAnswersTheStoresTransactionQuestion|TestRepoAuthorityIsTheComparisonTwoSubsystemsUse|TestAMisPagedStreamIsRefusedBeforeItIsFolded|TestAnOverLongPageIsRefused|TestALoadThatFailsMidStreamReturnsNothing|TestEveryHistoryClassRefusalIsRaisedByTheThingItNames|TestBothDoorsCheckTheStore|TestAStoreWhoseProductExceedsTheResidentCeilingIsRefused|TestAnAppendWhoseActualBytesExceedTheResidentCeilingIsRefused|TestAnOutcomeOutsideTheVocabularyTakesTheFailSafeDefault|TestOneAppendCarriesTwoIdenticalChanges|TestAFoldPanicUnwindsOutOfLoad|TestAPanickingCodecBecomesTheRefusalItsErrorWouldHaveBeen|TestARequestClassRefusalRendersAClientStatusAndAHistoryClassOneDoesNot)$' ./event/ && go test -race -count=1 ./event/...
```

---

### S5 — `event/eventtest`: the conformance suite and its self-falsification  `[ ]`

**Delivers** the exported suite, its twenty sections, its three fixture stores and
its twelve self-falsification defects, plus the three runnable proxies. This is the
section that makes every earlier claim evidence rather than prose, and it is the
artefact phase 2's `eventpg` runs **verbatim**.

**Files** `event/eventtest/doc.go`, `suite.go`, `inventory.go` (the twenty
section names and the runner that iterates them), `sections_write.go`,
`sections_read.go`, `sections_lifecycle.go`, `sections_ownership.go`,
`sections_transactions.go`, `defects.go`, `proxies.go`, `report.go`;
`event/eventtest/inventory_test.go` (`package eventtest`, the inventory
assertion and its shortened-inventory control);
`event/eventtest/*_test.go` (the three fixture stores, in the suite's **own test
package** — a third package, which is §INV-019's compile-time proof);
`event/eventmemory/conformance_test.go`.

**Realises** `Factory`, `Tx`, `Run`, `RoundTrip`, `Keys`, `Families`.

**Carried gaps** C1 (`RoundTrip`'s proxy is reported), C2 (the refusing-decorator
case), C3 (`cancellation` asserts both halves in both windows), C4 (the `bounds`
case, the illegal-product store, the over-long-page defect, and the two mis-paging
defects with the at-the-bound control), C7 (the one-byte `append` case), C8 (one
inventory in `defects.go`, twelve defects, a test computes the coverage),
C9 (the policed half), C10.2, C10.4.

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
UC-045, UC-047, UC-048, UC-051 (`stream identity`), UC-053, UC-057, UC-059
(`binding`), UC-060, UC-061 (`payload ownership`), UC-066; INV-001 (the
append-only walk), INV-002, INV-003, INV-009, INV-019 (the compile-time half),
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
| UC-006 construct a store, bind two aggregates | S3 (constructor) + S4 (`Open`/`Bind`) + S5 (`binding`) | S5 | `TestTwoStoreValuesOverOneLog`, `binding` |
| UC-007 two stores over two backings | S4 | S4 | `TestWithinComposesForTwoBackings`, `TestAppendRefusesInItsStatedOrder` |
| UC-008 a readiness answer | S3 | S3 | `TestCheckAnswersWhileTheStoreIsOpenAndAfterItIsClosed` |
| UC-009 a fresh stream loads as the zero state | S4 | S4 | `TestAFreshStreamLoadsAsZero` |
| UC-010 a stream with history is folded | S4 | S4 | `TestAStreamWithHistoryFoldsToItsCurrentState` |
| UC-011 a long stream is paged | S4 + S5 | S5 | `stream paging` |
| UC-012 an older revision | S2 (chain) + S4 (load) | S4 | `TestAStreamWithHistoryFoldsToItsCurrentState` (its revision-1 stored event) |
| UC-013 an unknown event type | S4 | S4 | `TestEveryHistoryClassRefusalIsRaisedByTheThingItNames` |
| UC-014 an unreadable revision | S2 (the chain refuses it) + S4 (the surfacing) | S4 | `TestEveryHistoryClassRefusalIsRaisedByTheThingItNames` |
| UC-015 a malformed payload | S2 (the codec refuses it) + S4 (the surfacing) | S4 | `TestEveryHistoryClassRefusalIsRaisedByTheThingItNames` |
| UC-016 the upcaster refuses | S2 (recovery) + S4 (surfacing) | S4 | `TestAnUpcasterMayRefuseAndAFoldMayNot`, `TestEveryHistoryClassRefusalIsRaisedByTheThingItNames`, `TestAPanickingCodecBecomesTheRefusalItsErrorWouldHaveBeen` |
| UC-017 a stored payload over the cap → `ErrPayload` (C5) | S4 | S4 | `TestEveryHistoryClassRefusalIsRaisedByTheThingItNames`, `TestARequestClassRefusalRendersAClientStatusAndAHistoryClassOneDoesNot` |
| UC-018 a load under a cancelled context | S4 + S5 | S5 | `cancellation` |
| UC-019 load, decide, append | S4 + S5 | S5 | `expected version` |
| UC-020 a decision that changed nothing | S4 | S4 | `TestAnEmptyAppendChecksTheKeyAndNothingElse` |
| UC-021 two decisions in one operation | S2 (`Fold`) + S4 (`Append`) | S4 | `TestOneAppendCarriesTwoIdenticalChanges` |
| UC-022 two writers, one stream | S3 (admission) + S5 (`expected version`, `transactions`) | S5 | `TestTwoTransactionsOnOneStreamLeaveOneWinnerAndAConflictFromAppend`, `expected version`, `transactions` (the aftermath case) |
| UC-023 **WITHDRAWN** — resolves to UC-022 + UC-034 | S4 + S5 | S5 | — |
| UC-024 an unencodable or oversized payload | S2 (`New`) + S4 (surfacing) | S4 | `TestFoldRefusesAnotherInstance`, `TestARequestClassRefusalRendersAClientStatusAndAHistoryClassOneDoesNot` |
| UC-025 a batch over the bound | S4 + S5 | S5 | `bounds`, `TestAnAppendWhoseActualBytesExceedTheResidentCeilingIsRefused` |
| UC-026 another aggregate's change (compile error) | S2 + `testdata/crossings` | S2 | `TestTheCrossingsThatMustNotCompile` |
| UC-027 a caller names an expected version | S4 + `testdata/crossings` + S5 | S5 | `TestTheCrossingsThatMustNotCompile`, `expected version` |
| UC-028 a caller transaction spans load and append | S3 (`Tx`) + S4 (`Within`) + S5 | S5 | `TestASecondAppendInOneTransactionIsAdmitted`, `TestWithinComposesForTwoBackings`, `transactions` |
| UC-029 the caller's transaction rolls back | S3 + S5 (`transactions`) | S5 | `TestARolledBackAppendBurnsItsPositions`, `transactions` |
| UC-030 two subsystems, one transaction | S4 (`Authority`) | S4 | `TestRepoAuthorityIsTheComparisonTwoSubsystemsUse` |
| UC-031 a token over another backing | S4 | S4 | `TestAppendRefusesInItsStatedOrder`, `TestAnEmptyAppendChecksTheKeyAndNothingElse` |
| UC-032 `Within` with no transaction | S4 | S4 | `TestWithinAnswersTheStoresTransactionQuestion` |
| UC-033 `Within` on a store with no transactions | S4 | S4 | `TestWithinAnswersTheStoresTransactionQuestion` |
| UC-034 the commit outcome is unknown | S1 (map) + S4 + S5 | S5 | `TestAnOutcomeOutsideTheVocabularyTakesTheFailSafeDefault`, `store failure classification` |
| UC-035 the same command twice, no idempotency key | S4 (the absence) | S4 | `TestOneAppendCarriesTwoIdenticalChanges` |
| UC-036 a bounded read | S4 + S5 | S5 | `global paging` |
| UC-037 no monotone-visibility promise | S3 (commit-order positions, the cursor's encoding, and `MonotoneVisibility: Supported` re-derived from them) + S5 (`resumption`, whose out-of-position-order writer is driven by the stage-time-allocating defect store because `eventmemory` cannot commit out of order) | S5 | `monotone visibility`, `resumption` |
| UC-038 per-stream order is a subsequence | S5 (`global order`) | S5 | `global order`, `conservation` |
| UC-039 repeated reads tile a quiescent log | S5 (`global paging`) | S5 | `global paging` |
| UC-040 an aggregate unit-tested with no database | S3 + S5 | S5 | `TestTheMemoryStoreSatisfiesTheContract` |
| UC-041 a fold with no store at all | S2 | S2 | `TestFoldRefusesAnotherInstance`, `TestAChangeRetainsNoApplicationValue` |
| UC-042 a payload's revisions are round-tripped | S2 (`Fact.RoundTrip`) + S5 (`eventtest.RoundTrip`) | S2 | `TestACodecThatDecodesIntoAReusedBufferIsCaught` |
| UC-043 a store implementer runs the suite | S5 | S5 | `TestTheMemoryStoreSatisfiesTheContract`, `TestEverySectionInTheInventoryWasReported` |
| UC-044 a store cannot exercise a capability | S3 (`eventmemory` says what it cannot) + S5 (the suite's three words) | S5 | `TestEverySectionInTheInventoryWasReported`, `store failure classification` |
| UC-045 the suite falsifies itself | S5 | S5 | `TestTheSuiteStillDetectsEveryDefectItWasBuiltToDetect` |
| UC-046 an ambient transaction, no `Within` | S4 | S4 | `TestWithinAnswersTheStoresTransactionQuestion` |
| UC-047 close, and everything after | S3 + S5 (`lifecycle`) | S5 | `TestCloseIsIdempotentAndDecidesNothing`, `lifecycle` |
| UC-048 a decorator wraps a store | S1 (`Refused`) + S5 (decorated runs) | S5 | `refusal classes` |
| UC-049 a marked context meets a different transaction | S4 | S4 | `TestWithinAnswersTheStoresTransactionQuestion` |
| UC-050 a composite identity | S1 (`Compose`) + S2 (the mapper and `Aggregate.Key`) | S2 | `TestADeclaration`, `TestComposeRendersTheFrozenKey`, `FuzzComposeRendersAKeyThatIsLegalAndReversible` |
| UC-051 the mapper produces an illegal key | S2 (`New`, `Fold`) + S4 (`Load`, `Append`) + S5 | S5 | `TestAppendRefusesInItsStatedOrder`, `stream identity` |
| UC-052 a state reaching a slice or a map | S2 | S2 | `TestADeclaration`, `TestAReferenceKindStateFoldsWithoutAliasing` |
| UC-053 a cursor persisted, resumed, refused when foreign | S3 + S5 (`resumption`) | S5 | `resumption`, `store failure classification` |
| UC-054 two store values, one backing | S3 | S3 | `TestTwoStoreValuesOverOneLog` |
| UC-055 a store whose lifetime is one request | S3 + S4 (`Bind` is O(1)) | S4 | `TestBothDoorsCheckTheStore` |
| UC-056 a payload its own codec cannot encode | S2 | S2 | `TestEveryMalformedDeclarationPanicsAndTryDefineReturnsIt` |
| UC-057 cancellation during an append, two windows | S3 (the store's half) + S4 (the surfacing) + S5 (`cancellation`) | S5 | `TestAContextCauseNeverTravelsThroughARefusal`, `cancellation` |
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
| INV-002 admission only at the observed version | S3 + S5 (`expected version`) | S5 | `expected version` |
| INV-003 one append is one atomic unit | S3 + S5 | S5 | `expected version`, `transactions` |
| INV-004 folds and upcasters are pure | S2 | S2 | `TestAChangeRetainsNoApplicationValue`, `TestAReferenceKindStateFoldsWithoutAliasing` |
| INV-005 wire identity declared; the key's rendering frozen | S1 (the frozen rendering, byte for byte) + S2 (the declared identifiers) | S1 | `TestComposeRendersTheFrozenKey`, `FuzzComposeRendersAKeyThatIsLegalAndReversible`, `TestADeclaration` |
| INV-006 no partially rehydrated state from `Load` | S4 | S4 | `TestALoadThatFailsMidStreamReturnsNothing` |
| INV-007 a conflict is never retried by the framework | S4 (a recording store) + S6 (the AST walk) | S6 | `TestOneLoadAndOneAppendMakeExactlyTheStoreCallsTheContractNames`, `TestTheKernelNeverIssuesTransactionControlAndNeverRetries` |
| INV-008 full replay is the only authority | S4 | S4 | `TestAStreamWithHistoryFoldsToItsCurrentState` |
| INV-009 dense versions, sparse positions, no clock ordering | S3 + S5 | S5 | `dense versions`, `global order` |
| INV-010 a revision is derived, never typed | S2 (`Revisions()`) | S2 | `TestADeclaration` |
| INV-011 no identity, payload, version, position, key or cursor becomes a default field | S1 (message table) + S6 (source check) | S6 | `TestEveryRenderingNamesAClassAndNeverAValue`, `TestNoRefusalRendersAnIdentityAPayloadAKeyOrACursor` |
| INV-012 event history is not a CRUD collection | S1 (method inventory) | S1 | `TestTheStoreSeamHasEightMethodsAndNoneMutatesOrQueries` |
| INV-013 no package-level mutable state, no constructor starts anything | S3 (obeyed) + S6 (the AST checks) | S6 | `TestNoPackageLevelStateIsEverMutated`, `TestMerelyImportingTheEventExtensionStartsNothing` |
| INV-014 no start-up migrates, creates or discovers | S3 (by construction) | S3 | `TestTwoStoreValuesOverOneLog` |
| INV-015 an at-token is minted, never manufactured | S4 + `testdata/crossings` | S4 | `TestAForgedTokenIsRefusedBeforeAnyStatement`, `TestTheCrossingsThatMustNotCompile` |
| INV-016 store identity is the backing; `Equal`, never `==` | S1 | S1 | `TestABackingAndAnAuthorityAreComparedAndNeverIdentical` |
| INV-017 a fold cannot fail; only an upcaster may refuse | S2 + S4 | S4 | `TestAnUpcasterMayRefuseAndAFoldMayNot`, `TestAFoldPanicUnwindsOutOfLoad`, `TestAPanickingCodecBecomesTheRefusalItsErrorWouldHaveBeen` |
| INV-018 the store never sees a Go type; who applies which bound (C4) | S1 (the seam's values carry bytes and no Go type) + S4 (the verification) | S4 | `TestTheSeamsValuesAreDefinedTypesWithTheDeclaredFields`, `TestAnOverLongPageIsRefused`, `TestAMisPagedStreamIsRefusedBeforeItIsFolded` |
| INV-019 a second store costs zero diffs to `event/` | S4 (the caller half) + S5 (**the proof**) + S6 (the regenerated baseline, a report and not a gate) | S5 | `TestATrivialStoreNeedsNoInternalAccess` |
| INV-020 one aggregate's value is not usable with another, and the scope | S2 (compile) + S4 (runtime) | S4 | `TestTheCrossingsThatMustNotCompile`, `TestFoldRefusesAnotherInstance`, `TestAppendRefusesInItsStatedOrder` |
| INV-021 nothing held and nothing handed out share mutable memory | S1 (clauses) + S2 (1, 2, 8) + S3 (4, 7) + S5 (3, 6 and `payload ownership`) | S5 | `TestAChangeRetainsNoApplicationValue`, `TestAPageIsTheCallersIncludingItsCapacity`, `payload ownership` |
| INV-022 every bound declared, reachable, unescapable; two doors | S4 | S4 | `TestBothDoorsCheckTheStore`, `TestAStoreWhoseProductExceedsTheResidentCeilingIsRefused`, `TestAnAppendWhoseActualBytesExceedTheResidentCeilingIsRefused` |
| INV-023 encodability is the codec's question, answered before `main` | S2 | S2 | `TestEveryMalformedDeclarationPanicsAndTryDefineReturnsIt` |
| INV-024 the refusal vocabulary is a partition | S1 | S1 | `TestTheRefusalVocabularyIsAPartition`, `TestADeclaredWrapIsReachableByErrorsAsAndACauseIsNot` |
| INV-025 a refusal names identifiers, never data | S1 | S1 | `TestEveryRenderingNamesAClassAndNeverAValue` |
| INV-026 a conflict carries no version | S1 + S5 | S5 | `TestEveryRenderingNamesAClassAndNeverAValue`, `refusal classes` |
| INV-027 the framework never opens, commits or rolls back a transaction | S1 (the seam declares none) + S4 (a recording store) + S6 (the AST walk) | S6 | `TestTheStoreSeamHasEightMethodsAndNoneMutatesOrQueries`, `TestOneLoadAndOneAppendMakeExactlyTheStoreCallsTheContractNames`, `TestTheKernelNeverIssuesTransactionControlAndNeverRetries` |
| INV-028 an authority **is** its transaction's identity | S1 + S4 | S4 | `TestABackingAndAnAuthorityAreComparedAndNeverIdentical`, `TestRepoAuthorityIsTheComparisonTwoSubsystemsUse` |
| INV-029 cancellation identity preserved, uncertainty outranks it (C3) | S1 + S5 (`cancellation`) | S5 | `TestAContextCauseNeverTravelsThroughARefusal`, `cancellation` |
| INV-030 every question is answered by the exact outer value | S1 (all required) + S5 (decorated runs) | S5 | `refusal classes`, `store failure classification` |
| INV-031 an empty append touches no store | S4 | S4 | `TestAnEmptyAppendChecksTheKeyAndNothingElse` |
| INV-032 close is idempotent and decides nothing | S3 + S5 (`lifecycle`) | S5 | `TestCloseIsIdempotentAndDecidesNothing`, `lifecycle` |
| INV-033 a stream is (family, key) byte-exact; injectivity is the application's | S1 (`Compose`'s own two properties, and the rendering they hold over) + S2 + S5 (`stream identity`) | S5 | `FuzzComposeRendersAKeyThatIsLegalAndReversible`, `TestComposeRendersTheFrozenKey`, `TestADeclaration`, `stream identity` |
| INV-034 the two reads describe one set | S5 (`conservation`) | S5 | `conservation` |
| INV-035 a returned resume point is safe to persist | S3 + S5 (`resumption`) | S5 | `resumption` |
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
| C9 GAP-108 codec, mapper, fold, upcaster and clock concurrency | S1, S2, S3 (clauses) + S5 (the policed half) | S5 | `concurrency` |
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
| `event/codec.go` | 2 | 0 | 0 | 260 |
| `event/chain.go` | 3 | 0 | 1 (a `Codec`) | 220 |
| `event/aggregate.go` | 4 | 0 | 0 | 260 |
| `event/fact.go` | 3 | 0 | 0 | 280 |
| `event/change.go` | 1 | 0 | 0 | 90 |
| `event/seal.go` | 0 | 0 | 0 | 60 |
| `event/token.go` | 2 | 0 | 0 | 110 |
| `event/binding.go` | 3 | 0 | 1 (a `Store`) | 150 |
| `event/repo.go` | 1 + 4 methods | 0 | 1 (a `Store`) | 330 |
| `event/marker.go` | 0 | 0 | 0 | 80 |
| `event/reader.go` | 3 | 0 | 1 (a `Log`) | 170 |
| `event/eventmemory/log.go` | 3 | 1 (`event`) | 0 | 140 |
| `event/eventmemory/store.go` | 3 | 1 | 0 | 200 |
| `event/eventmemory/append.go` | 0 | 1 | 0 | 190 |
| `event/eventmemory/read.go` | 0 | 1 | 0 | 170 |
| `event/eventmemory/transaction.go` | 2 | 1 | 0 | 230 |
| `event/eventmemory/cursor.go` | 0 | 1 | 0 | 90 |
| `event/eventtest/suite.go` | 3 | 1 | 0 | 300 |
| `event/eventtest/inventory.go` | 0 | 1 | 0 | 120 |
| `event/eventtest/sections_*.go` (5 files) | 0 | 1 | 0 | ≤ 380 each |
| `event/eventtest/defects.go` | 0 | 1 | 0 | 360 |
| `event/eventtest/proxies.go` | 3 | 1 | 0 | 150 |
| `event/eventtest/report.go` | 0 | 1 | 0 | 90 |

**No file breaches 400 lines**; `event/repo.go` at ~330,
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
