# EVENTSOURCE_P5 — the caller's guarantees (ES-05, ES-07, ES-08, ES-09) — GAPS

## Round 1 — spec audit (coverage / invariants / DX) — 2026-09-12

Audited: [`EVENTSOURCE_P5_USECASES.md`](../usecases/EVENTSOURCE_P5_USECASES.md) against
[`EVENTSOURCE_P5_STUDY.md`](../usecases/EVENTSOURCE_P5_STUDY.md), the four appendices at
`docs/roadmaps/2026-09-01-postgres-event-sourcing-roadmap.md:783-822` (read in the original
Russian, part 3 of each taken sentence by sentence),
[`EVENTSOURCE_REFERENCE.md`](EVENTSOURCE_REFERENCE.md) including its three **Reject** entries and
its Documentation obligations, the shipped `Park`/`Observe`/`Cutover`/`Advance` contracts as
published on `docs/modules/en/projection.md`, and the binding decisions
[[D-092]] [[D-101]] [[D-118]] [[D-125]] [[D-126]] [[D-128]] [[D-129]] [[D-130]] [[D-132]] [[D-133]].

**Verdict: red.** One `[critical]` and ten `[high]`. Eight of the eleven are in ES-07 and ES-05;
**ES-08 and ES-09 are clean at this severity** — ES-08 answers every sentence of its part 3 and
argues the UC-032 boundary clause by clause, and ES-09 delivers the contract-without-code route with
the one thing neither source has (§1.4's "nothing detects a forgotten bump", pinned by UC-238's
inverted control).

The shape of the failure is consistent and worth naming once: **the document is excellent on the
mechanism it is describing and thin at the seam where the caller meets it.** Every blocking finding
below is at a seam — the second minting door of a `Mark`, the second door of a receipt, the poll
that fails, the verdict a caller can ignore, the clock the horizon is compared on. §1.2's own
headline argument (a receipt "has no such window" unlike KurrentDB's) is falsified by §1.2's own
fingerprint rule three paragraphs later, and that is GAP-1.

> **Round 1 closed — 2026-09-12.** All eleven blocking findings are addressed in the spec; each
> carries its own `Status` line with what changed and why. Seven of the eleven were closed by a
> change of shape rather than an added rule: the expected version **left** the fingerprint (GAP-1),
> `receipt.Once` took ownership of an order a paragraph was owning (GAP-3), one key now covers one
> append with the two-aggregate case answered by two keys (GAP-5), `ResolveSpec.Issued` became
> optional and the ledger's instants moved onto the database's clock (GAP-6), `Mark` carries the
> sequence keys its own door could compute (GAP-7), `WaitOf` derives the five facts a request path
> was restating (GAP-9), and `Visibility.Holes` was **deleted** rather than announced (GAP-11).
> Three new use-case groups' worth of cases (§UC-242…§UC-251) and two new invariants (INV-125,
> INV-126) carry the parts nothing can check, each with the inverted control that fails the day it
> becomes checkable. The eleven `[medium]`/`[low]` findings below are untouched and stay in
> `EVENTSOURCE_BACKLOG.md` `## P5`.

**Scope creep into [[UC-032]]: none found.** §1.3's clause-by-clause argument against §6 ("not a
cache, not a snapshot, not a partial rehydration") holds, `Out of scope`'s query-language line is
respected (a version ceiling is a prefix), ES-09 exports nothing, and the only edit is a `See also`.
§7.3 is explicit that no `What must hold` clause changes. Checked and clean.

**Binding decisions: no contradiction found.** [[D-128]] (INV-108 forbids the restatement, and the
mark is a `Position` never a "last position of the page"), [[D-129]] (nothing compares, orders or
persists a `Cursor`), [[D-130]], [[D-133]], [[D-118]] (D-142 is owed and specified), [[D-092]]
(the wait runs on the caller's goroutine; `startsNothing` is named as the falsifier), [[D-126]]
(the serialisation is the primary-key index's, and UC-217 runs all three levels), [[D-101]]
(no fifth table, `SchemaVersion` stays at 2). GAP-11 is a **contract widening that is not
announced**, not a decision violated.

**Universality: no spec-level hardcode found.** Checked every literal: `Every: 50 ms` with its
`5 × 20 = 100` arithmetic (a default with a stated cost and a tension recording that the plan may
move it), `StreamPage` 4/256, the 11- and 12-event streams of UC-228/UC-229, `N >= 2`, SHA-256,
`"sha256:"`, `~50 ms` p99 (inherited from [[D-132]]), and 1 %/50 %/100 % of 100 000 events. Each is
a fixture parameter of a rule stated generally, a published wire constant, or a number carried in
from an existing decision. Nothing branches on a sample value and no rule is stated only for one
shape of input.

---

### GAP-1 [critical][immediate] The fingerprint is over inputs a retry cannot reproduce, so `Repeated` is unreachable in the one scenario ES-07 exists for

- **Where:** §1.2 ("What makes two appends 'the same operation'", "What the fingerprint covers",
  "The three verdicts of a claim"), §5.1 `Repo.Digest`, §UC-215, §UC-224, §UC-227, §INV-116,
  §INV-117.
- **What:** The fingerprint covers **the expected version the append was decided at** (§1.2, and
  §5.1's doc comment makes it load-bearing: *"an idempotence claim is only as strong as the
  concurrency check it is anchored to"*), and `Repo.Digest(at At[S], changes ...Change[S])` can only
  be computed from an `At[S]`. The public surface mints an `At[S]` exactly one way — `Repo.Load` —
  and a load after the first attempt **committed** answers the moved version. So the canonical
  ES-07 flow (connection dies between `COMMIT` and the response; a *new* request, in a *new*
  process, retries with the same key) computes a fingerprint at version *n+1* against a receipt
  written at version *n*, and `Claim` answers **`Collided`**, not `Repeated`.
  §UC-227 states the opposite as its `Then` — *"The retry's `Claim` answers `Repeated` with the
  original range"* — while its own `Given` (the connection was killed) removes the only way to
  reproduce the original token, and its `Must not` forbids the re-load that is the only thing left.
  §UC-215 makes the same claim from a `Given` that stipulates *"the same expected version"*, which
  is reachable in a test that keeps the token and not in the scenario the appendix describes.
  Two further arms of the same defect:
  - **§1.2 contradicts itself.** *"A receipt keyed on the caller's own key has no such window, and
    that is the whole argument for the appendix's adaptation"* — the window it says KurrentDB has is
    precisely "the retry must present the same expected version as the original". Putting the
    expected version in the fingerprint reinstates that window; it only changes the answer from a
    duplicate append to a refusal.
  - **`Collided` conflates two meanings a caller must act on differently.** "Somebody else spent
    your key" (report a conflict to the client) and "your own operation, re-decided at the version
    it itself moved" (report success, the work is done) are one verdict and one sentinel. §1.2's
    `Unresolved` guidance depends on telling them apart and does not: warning 1 says a re-issue
    *"is a **different** operation to the receipt, so nothing catches it"*, which the `Collided`
    rule contradicts — it does catch it, under a name that reads as a stranger's key.
  - **Payload determinism is an unstated precondition.** The fingerprint covers *"the payload
    bytes"*, which are the caller's codec's output. §UC-224 presents *"two whose domain inputs are
    identical but whose encoded records differ (a codec that records a timestamp)"* producing **two
    fingerprints** as correct behaviour. It is correct arithmetic and it means that for any codec
    that records an instant, a map, or anything else not byte-stable, **no retry ever matches** and
    every retry is `Collided`. The obligation ("a decision must be byte-reproducible under this key
    or the mechanism never fires") is nowhere, though §INV-111 is the document's own precedent for
    stating an obligation the framework cannot check.
- **Why this severity:** ES-07's «зачем», verbatim: *«после потери соединения на commit узнать,
  записана ли именно эта операция»*. Worked: a payment handler mints key K, the transaction commits
  (events at versions 6–7), the response is lost. The client retries with K. The handler loads (now
  version 7), decides the same facts, digests → `F2 ≠ F1`, claims → `Collided` → `ErrCollision`. The
  caller has a refusal that its own documentation says means "the key was spent on another
  operation", for its own operation, and the only honest recovery (`Resolve`) is not the path the
  spec's example or its use cases take. The primary scenario is broken as specified, and the two use
  cases that claim to prove it (§UC-215, §UC-227) are written against a `Given` the public surface
  cannot produce.
- **Why this timing:** It decides the shape of the fingerprint, the verdict enum and the retry
  protocol — three frozen surfaces in §5.1 and §5.3 — and D-142 is chartered to record the
  fingerprint's contents. Deciding it after the code is written means re-recording the kernel
  baseline and re-cutting the verdict enum.
- **Close criteria:**
  - [x] The spec states the retry protocol it means, end to end, for a retry that arrives in a new
        process holding only the key: which call comes first, what it answers when the first attempt
        committed, and what the caller returns to its client.
  - [x] Either the expected version leaves the fingerprint (with the weak-anchor risk argued and a
        replacement for what it was buying), or the claim answers a **fourth, distinct** verdict for
        "the same key, the same records, another anchor" so a caller can tell its own moved retry
        from a stranger's key — and the choice is argued against the other.
  - [x] §UC-227's `Given`/`Then` are rewritten so the `Then` is reachable from the `Given` through
        the published surface, or the case states plainly that `Repeated` is reachable only for a
        retry that overlaps the original in flight.
  - [x] §1.2's "no such window" paragraph is reconciled with the fingerprint rule, and warning 1's
        "nothing catches it" is corrected or scoped to a re-issue that skips the claim.
  - [x] A byte-reproducibility obligation is stated beside `Repo.Digest` in the shape §INV-111 uses,
        with §UC-224's timestamp-codec arm re-labelled as the mechanism being disabled rather than
        as correct behaviour, and a use case that pins the resulting `Collided` storm.
- **Status:** **closed** — 2026-09-12, in [`EVENTSOURCE_P5_USECASES.md`](../usecases/EVENTSOURCE_P5_USECASES.md).
  **The expected version left the fingerprint.** §1.2's "What the fingerprint covers" now digests the
  composed stream and the records and nothing else, with the canonical retry worked out in full as
  the argument. The study's nuance ES-07/3 is answered at its source rather than waved past: the
  reference's weak side is `ExpectedVersion.Any`, which weakens **the append**, while vv's append
  keeps its exact expected version and a `Repeated` verdict appends nothing — so there is no write
  for a weak anchor to weaken. The anchor is **recorded and not compared**: `Receipt.First - 1` is
  the version the first attempt was decided at, which `Resolve` shows, so no field was added and no
  information lost. No fourth verdict is needed, because the case it would have named — "my own
  operation, re-decided at the version it moved" — is now `Repeated`, which is what it is.
  §1.2's "no such window" paragraph now points forward to the decision that makes it true. The
  `Unresolved` guidance's warning 1 is corrected: a re-issue **under the same key** blocks on the
  index and answers `Repeated` or `Recorded` on the original's outcome, and what nothing catches is
  a re-issue with no key; the 202 advice keeps its own reason (a held connection). The retry
  protocol is stated end to end in five steps. Byte-reproducibility is now an explicit obligation —
  a §-of-its-own in §1.2, restated on `Repo.Digest`, INV-125, and §UC-247 pinning the fail-closed
  `Collided` storm — and §UC-224's timestamp arm is re-labelled as the mechanism being disabled.
  §UC-215 and §UC-227 are rewritten against a new-process `Given`; §UC-216's version variant is
  flipped to a **must-not-collide** arm.

---

### GAP-2 [high][immediate] `Repeated` reports "already done" from a row that may record no events at all, and a no-op decision is the ordinary way to produce one

- **Where:** §1.2 ("The three verdicts of a claim"; "`Resolve`, and the hard clause" —
  `Incomplete`), §5.3 `Held.Receipt`/`Verdict`, §UC-215, §UC-222, §INV-117.
- **What:** `Claim`'s `Repeated` arm is defined only as *"a row exists and its fingerprint compares
  equal"*. `Receipt` carries `Complete bool` and the spec's own `Resolve` side has a whole standing
  for the row whose completion never landed. `Claim` has no such arm: a retry against an incomplete
  row answers `Repeated`, and §1.2 tells the caller *"do not append. `held.Receipt()` carries the
  range the first attempt wrote, which is the answer to the original request"* — for a row whose
  range is `First = Last = 0`. The caller answers its client with a success it derived from a
  receipt recording nothing.
  §UC-222 treats an incomplete row as a defect report. **It is also an ordinary outcome:** a
  decision that yields no changes ("the order is already paid; nothing to do") claims, appends
  nothing, completes nothing, and commits — a legitimate unit of work leaving a legitimate
  incomplete row. The spec has no place for it: §1.2 calls the state *"not an ordinary state"*, and
  the four-branch example (`Recorded`/`Repeated`/`Collided`) has no branch that reaches it.
- **Why this severity:** Two reachable paths, one of them a normal domain outcome, both ending in a
  caller reporting success for events that do not exist, with no error on any path and a durable row
  that makes every later retry give the same wrong answer. It is the "compiles, runs, returns no
  error and is wrong" shape, and the data to prevent it (`Receipt.Complete`) is already in the
  struct and simply not read.
- **Why this timing:** It is an arm of a frozen three-member enum and a missing clause of `Claim`'s
  contract; both are §5.3 surface. Adding a fourth verdict or a refusal after the code exists is a
  surface-baseline change and a re-cut of every caller's switch.
- **Close criteria:**
  - [x] `Claim`'s contract states what it answers when the existing row is incomplete, and the
        answer is distinguishable from a completed repeat at the call site.
  - [x] The no-op decision is specified: what a caller does when it claims and decides nothing, and
        what the next retry of that key is told.
  - [x] A use case pairs a retry against a completed row with a retry against an incomplete one and
        asserts the two answers differ; the control is the completed row.
  - [x] §1.2's "not an ordinary state" sentence is corrected or its exception is named.
- **Status:** **closed** — `Repeated` now requires a row that exists **and is complete**, and a row
  that exists and is not complete is a **refusal**, `ErrIncomplete`, with no verdict at all: whether
  that operation's events reached the log is not a question its row answers, so a claim concludes
  nothing from it. It is a refusal rather than a fourth verdict because there is nothing for a
  caller to branch to. The no-op decision got the spelling that keeps `Incomplete` an honest defect
  report: `Repo.Append` with an empty batch answers an empty `Commit`, `Held.Complete` takes it and
  writes a complete row with a zero range, and `Once` does it without the caller thinking about it —
  so "not an ordinary state" is now true rather than contradicted. §UC-222 is rewritten with the
  retry arm, §UC-246 is the no-op case with its incomplete control, and INV-117 carries both.

---

### GAP-3 [high][immediate] The claim protocol is a four-step orchestration whose every step is ignorable, and the ordering hazard it exists for is enforced by nothing

- **Where:** §1.2 ("The order: claim, decide, append, complete"), §5.3 `Claim`, `Held`, `Verdict`,
  `Held.Complete`, §UC-214…§UC-216, §INV-114.
- **What:** The DX the spec publishes is: mint a key, open a unit, load, decide, `Repo.Digest`,
  `NewFingerprint`, `Claim`, **switch on a verdict**, `Repo.Append`, `Held.Complete`. `Claim`
  returns `(Held, error)` and answers `Repeated` and `Collided` **with a nil error** — the example
  turns `Collided` into `ErrCollision` in the *caller's* code. So the whole mechanism is advisory:
  ```go
  held, err := receipt.Claim(ctx, spec)   // err == nil on Repeated and on Collided
  if err != nil { return err }
  at, commit, err := repo.Append(ctx, at, changes...)   // appends anyway
  ```
  compiles, runs, returns no error, and appends a second copy of an operation the ledger has already
  recorded — or spends a stranger's key. Nothing in the returned value makes the append harder than
  not making it, and `Held.Complete` is the only thing that would notice, after the events are in
  the log.
  The same shape leaves the ordering rule unenforced. §1.2 argues at length that *"the claim comes
  before the append and this is not cosmetic"*, because two retries at different expected versions
  *"both append and then collide at the receipt, with both sets of events already in the log. Too
  late is not a failure mode a receipt may have."* A caller that calls `Claim` after `Repo.Append`
  gets exactly that failure, and the spec offers no refusal, no use case and no invariant against
  it — only prose.
  No combinator was considered. A shape that states the intention once
  (`receipt.Once(ctx, spec, func(ctx) (event.Commit, error))`, which opens nothing and starts
  nothing) would make the order structural and the verdict un-ignorable; §1.2 records two rejected
  alternatives (the claim inside `Repo.Append`, a caller-supplied digest) and not this one.
- **Why this severity:** DX law — the caller orchestrates steps whose order encodes a failure
  somebody hit, and the failure surface is a value it is legal to discard. The worst consumer code
  that compiles and runs writes the operation twice. `jobs`'s shipped three-way outcome, which §1.2
  borrows the shape from, is returned from a call that *performs* the placement; here the shape is
  borrowed without the thing that made it safe.
- **Why this timing:** It is the top-level API of a new package. Every example, module page and
  consumer written against the four-step form has to be rewritten if the shape changes later, and
  §5.3 is the frozen surface.
- **Close criteria:**
  - [x] Either `Claim` returns an error for `Collided` (so ignoring it is a compile-visible
        discard), or the spec argues why a nil error is right and shows what stops the append.
  - [x] A combinator that owns the order is specified or refused with its cost, in the shape §1.2
        already uses for its two refused alternatives.
  - [x] A use case: a caller that appends after a `Repeated` verdict — what the mechanism does about
        it, or the statement that nothing does and where that is written down for the consumer.
  - [x] A use case: `Claim` called after `Repo.Append` in the same unit; either it is refused, or
        the consequence is stated beside the ordering argument that exists for it.
- **Status:** **closed** — three changes, in order of how structural they are. `receipt.Once` is
  specified and is the spelling the module page and the example give: it claims, runs the work only
  on `Recorded`, and completes in the caller's transaction, so the order is a place in the control
  flow rather than a step a caller remembers. `Claim` now answers a **non-nil error** for `Collided`
  and for an unresolved prior claim, so the worst code that compiles refuses instead of spending a
  stranger's key. `Held.Complete` refuses a verdict that is not `Recorded`, so a caller that appends
  after `Repeated` has its transaction refused and `crud.InNewTx` rolls the second copy back — the
  transaction is the enforcement, which is the only one a package that opens no transaction can
  have. Claim-after-append is stated as undetectable, with the reason (`event` does not import this
  package and will not be made to), and §UC-251 pins both misorderings with the `Once` control and
  an arm that asserts the damage when `Complete` is skipped.

---

### GAP-4 [high][immediate] `Complete` and `Resolve` carry no placement check while `Claim` carries three, so the atomicity `Claim` proves can be undone by the next call

- **Where:** §1.2 ("Where it lives, and why it is the application's table"), §5.3 `Ledger` doc
  ("WHERE EACH METHOD RUNS IS PART OF THIS CONTRACT"), `Held.Complete`, `Resolve`, `ResolveSpec`,
  §UC-218, §UC-219, §UC-221, §INV-114.
- **What:** `Claim` refuses unless a transaction of the store's is bound, unless the store claims
  transactions, and unless `Ledger.Transaction(ctx).Same(Store.Transaction(ctx))` — and §UC-218
  calls that pair *"[[D-118]]'s 'Atomic across two handles is a sentence with no meaning', measured"*.
  Neither of the other two doors has anything:
  - **`Held.Complete(ctx, commit)`** is specified as *"Writes the range onto the row this claim took,
    in the same transaction"* with no refusal if the context it is handed carries a **different**
    transaction, none at all, or a different handle. A `Complete` that lands outside the claim's
    transaction writes `complete = true` and a range that survives the rollback of the events it
    names — the exact row §UC-218 exists to prevent, produced by the call that follows it. Nor is
    the commit checked against the claim: nothing says `commit.Stream()` must be `ClaimSpec.Stream`,
    nothing says `commit.Authority()` must be `Same` as the claim's, and nothing says what a second
    `Complete` on one `Held` does (the range is silently overwritten, so an operation that appended
    twice records one range).
  - **`Resolve`** takes a `Store` in its spec and never says what it is for; the only trace is
    §UC-221's control (*"zero calls to the event store other than the transaction question"*), which
    implies a check the contract does not state. The check that matters is the mirror of
    `Committed`'s second refusal (§1.1): a `Resolve` issued **on the claiming transaction's own
    context** reads its own uncommitted row and answers `Found` — a proof of an operation that can
    still roll back — and "resolve first, then decide, in one unit" is an ordinary retry shape.
- **Why this severity:** The mechanism's single guarantee is "the receipt and the events are one
  commit". It is enforced at one of the three doors. The two unguarded doors each produce a durable
  row or a returned standing that asserts an operation happened when it did not, silently.
- **Why this timing:** These are refusals on a frozen surface, and §INV-114's statement ("a claim is
  one transaction with its append, or it is refused") is falsifiable only if the calls after the
  claim are in its scope. Retrofitting a refusal to `Complete` later changes the error surface every
  caller branches on.
- **Close criteria:**
  - [x] `Held.Complete`'s contract states its refusals: a transaction that is not the claim's, an
        absent one, a commit whose stream or authority is not the claim's, and a second call.
  - [x] `Resolve`'s contract states what `ResolveSpec.Store` is for and what happens when a
        transaction of the store's is bound to the context.
  - [x] §INV-114's scope covers all three doors, and its falsifiers include a `Complete` in a second
        transaction and a `Resolve` inside the writing one.
  - [x] Two use cases with their controls: `Complete` outside the claim's unit; `Resolve` inside it.
- **Status:** **closed** — `Held.Complete` now carries five refusals (a verdict that is not
  `Recorded`; a second call; no transaction of the store's; a transaction that is not the claim's;
  a commit whose stream is not the claim's), plus the authority comparison with its one stated
  exception — an empty commit carries the invalid `Authority` by design, and the doc says which
  check is absent and why. `Held` holds an unexported pointer so a second `Complete` is refusable at
  all. `Resolve` carries the mirror refusal and `ResolveSpec.Store`'s purpose is now stated: a
  transaction of the store's **or** of the ledger's bound to the context is `ErrSpec`, because a
  resolve inside the writing transaction reads its own uncommitted row and answers `Found` for an
  operation that can still roll back. INV-114 covers all three doors; §UC-248 and §UC-249 are the
  two cases with their controls, both read in `psql`.

---

### GAP-5 [high][immediate] A receipt covers one `Digest` over one stream, so an operation that appends twice is half-fingerprinted and two different operations can compare equal

- **Where:** §1.2 ("What the fingerprint covers", "The three verdicts"), §5.1 `Repo.Digest`, §5.3
  `ClaimSpec`, `Receipt{Stream, First, Last}`, §UC-214, §UC-216, §INV-116.
- **What:** `ClaimSpec` carries one `Stream` and one `Fingerprint`; `Receipt` carries one `Stream`
  and one `First`/`Last` pair; `Repo.Digest` is a method on one typed `Repo` over one `At[S]`. The
  framework happily supports two `Repo.Append` calls, to two aggregates, in one caller transaction —
  that is what [[D-118]]'s one-transaction rule buys — and nothing in ES-07 says whether such an
  operation may carry a receipt, what its fingerprint covers, or what its range means.
  As specified the receipt covers the first append and ignores the rest, which is not a partial
  guarantee but a wrong one: two operations that share their first aggregate's batch and differ in
  the second (`{A: credit 10, B: debit 10}` and `{A: credit 10, C: debit 10}`, the second being a
  corrected retry under the same key) produce **equal fingerprints**, so the second answers
  `Repeated`, appends nothing at all, and is reported to its client as already done. `Collided`
  exists precisely to catch a key spent on another operation and here it does not fire.
  The `Found` standing under-reports for the same reason: `Resolution.Receipt` is offered as *"enough
  to answer the original request without re-reading the aggregate"* while naming one of the streams
  the operation wrote.
- **Why this severity:** A silent skip of a real command, from an ordinary composition (two
  aggregates in one unit) the rest of this framework encourages, with a durable row that makes every
  retry repeat the same wrong answer.
- **Why this timing:** It is either a refusal at `Claim` (one receipt, one append) or a shape change
  (a fingerprint that accumulates appends, a range per stream) — both on the frozen §5.3 surface,
  and D-142 is chartered to record the fingerprint's contents.
- **Close criteria:**
  - [x] The spec states whether an operation may append to more than one stream under one key.
  - [x] If it may, the fingerprint and the recorded range cover every append in the unit, and a use
        case pins two operations sharing their first batch answering `Collided`.
  - [x] If it may not, `Claim`/`Complete` refuse the second append under one key, with a use case
        and a control, and the module page says what such a caller does instead.
  - [x] §INV-116's statement says which appends the digest covers, in those words.
- **Status:** **closed** — the spec now says it: **one key covers one append, to one stream.** The
  caller with two aggregates uses one key per append, which loses nothing, because the atomicity was
  always the transaction's and never the receipt's — and the module page carries that recipe. Two of
  the three failures are refused structurally at `Complete` (a commit of another stream, a second
  completion). The third — a second append the claim never named — is stated as an obligation with
  its reason, and §UC-250 pins it in the `gate_relscope_test.go` inverted shape: the one-key arm
  asserts the **wrong** answer is there, with the key-per-append arm answering `Collided` as its
  control. INV-116 says which appends the digest covers, in those words, and INV-126 is the rule.

---

### GAP-6 [high][immediate] The horizon compares an application clock against a ledger clock, and the retry that most needs `Resolve` has no `Issued` to give it

- **Where:** §1.2 ("Retention, and why an expired receipt does not read as a rollback"), §5.3
  `ResolveSpec.Issued`, `Ledger.Horizon`, `Receipt.RecordedAt`, the claim SQL (`recorded_at` as
  `$5`), §UC-223, §INV-115.
- **What:** Three clocks, none of them reconciled:
  1. `ResolveSpec.Issued` is *"the instant the caller minted the key"* — the caller's process clock.
  2. `Ledger.Horizon(ctx)` is *"`now − retention`, or the table's own `MIN(recorded_at)`, the
     implementation's choice"* — either the ledger process's clock or the stored column's.
  3. The stored `recorded_at` is a parameter in the published claim statement (`VALUES ($1,…,$5)`),
     i.e. a Go clock — in a document whose §1.3 makes a point of `eventpg`'s `recorded_at` being
     `statement_timestamp()`, *"a database clock, so comparable across writers, which is already
     better than the reference's application clock."* The receipt table reintroduces exactly the
     thing that paragraph criticises, and [[D-128]]'s "do not order by a clock" reasoning is the
     document's own precedent.
  Skew therefore decides `Expired` against `Unresolved` at the boundary in both directions: a
  caller clock behind the ledger's answers **`Expired`** — *"the framework does not know and will
  never know"* — for an operation that is in flight right now and about to produce a row; a caller
  clock ahead answers **`Unresolved`** for a swept row, which §1.2 identifies as the failure the
  horizon exists to prevent (*"permanently wrong about an operation that certainly happened"*).
  Worse, the field is **required**: *"a zero one is refused with `ErrSpec`"*. The canonical ES-07
  caller is an HTTP handler that received an idempotency key in a header on a retry; it has the key
  and **not** the instant the key was minted, and nothing in the appendix's DX
  («`receipts.Resolve(ctx, operationKey)`») asks for one. Faced with `ErrSpec`, the obvious repair
  is `Issued: time.Now()`, which is always after any horizon and therefore permanently suppresses
  `Expired` — the mechanism silently degrades to the exact behaviour §1.2 built it to avoid, with
  no error anywhere.
- **Why this severity:** A required parameter the primary caller does not have, whose easy wrong
  value disables the feature it gates, plus a cross-clock comparison the same document forbids
  elsewhere. §UC-223 tests the mechanism with a fixture where both clocks are the test's.
- **Why this timing:** `ResolveSpec` and the `Ledger` interface are frozen in §5.3, and where the
  instant comes from decides whether `Horizon` returns a time at all or whether the comparison moves
  into the ledger's own statement (where one clock decides both sides).
- **Close criteria:**
  - [x] The spec names which clock each of the three values comes from and states the comparison's
        error term, or moves the comparison to one side (the ledger compares `Issued` against its
        own stored instants in one statement).
  - [x] The HTTP idempotency-key case is worked: where a retry handler gets `Issued`, or `Issued`
        becomes optional with a defined standing when absent, and `ErrSpec` stops directing callers
        to `time.Now()`.
  - [x] The published claim SQL takes its instant from the database, or §1.3's praise of a database
        clock is reconciled with the receipt's use of an application one.
  - [x] A use case with skew in each direction, and a control showing which standing each produces.
- **Status:** **closed** — the comparison is reduced to two clocks and the remaining one is made
  optional. The published claim statement takes `recorded_at` from `statement_timestamp()` and never
  as a parameter, `Horizon` is the ledger's own SQL-side instant (`MIN(recorded_at)`, or
  `now() - retention` computed in SQL), and a conservative horizon is explicitly conformant while an
  optimistic one is not — so §1.3's praise of a database clock is no longer contradicted three
  sections later. `ResolveSpec.Issued` is now **optional**: absent, an absent row is `Unresolved` and
  `Resolution.Horizon` carries the ledger's instant for a caller whose own request log has the date;
  it is never `ErrSpec`, so nothing directs an HTTP retry at `time.Now()`. The skew's error term is
  stated, with the argument that makes it tolerable: it decides only between `Unresolved` and
  `Expired`, two non-conclusions, and no reading of it produces a false `Found` or a false "it did
  not happen". §UC-223 gains the zero-`Issued` arm and a skew arm in each direction.

---

### GAP-7 [high][immediate] A `MarkOf` mark has no envelope, so the park question — the one thing ES-05 must get right — is undefined for the door the spec explicitly admits

- **Where:** §1.1 ("The park question, asked first, every poll" — step 2), §1.1 ("A mark is a
  position that came out of a store"), §5.2 `Mark`, `MarkOf`, `WaitSpec.Sequence`, §UC-211 (third
  arm), §INV-108, §INV-110, §INV-113.
- **What:** The park question is specified as
  `Park.Holds(ctx, of.Whole(), spec.Sequence.SequenceOf(mark.envelope()))` — *"the projection's own
  sequencer applied to the envelope `Committed` read back"*. `Committed` reads an envelope;
  **`MarkOf(barrier)` does not** — a `Barrier` is folded from checkpoint rows and carries no event.
  So for one of the two minting doors the spec publishes, and which §UC-211's third arm goes out of
  its way to **admit** into `Wait`, step 2 of the mandatory per-poll order has no input and the
  document does not say what happens: no refusal, no skip, no statement that `Reached` then means
  delivered-and-not-applied. `SequenceOf` applied to a zero `Envelope` is at best a key nothing was
  ever parked under — which is §1.1's own description of the silent failure it built the park
  question to prevent (*"a wait that compares only `Highest` returns **reached** for an event that
  was never applied"*).
  The same asymmetry breaks the `Mark` type's own description. §5.2 says *"the fields are unexported
  and the two constructors both take a number a store produced"*; §1.1 requires it to carry a whole
  `Envelope`. Which it is decides whether a `Mark` retains an application payload — §1.3 cites
  INV-042's rule that the kernel retains no application value — and what `Mark` renders under `%v`,
  which INV-124's no-data rule would otherwise cover.
- **Why this severity:** It is the appendix sentence this whole section is built around
  («Scan checkpoint после parking не доказывает применение события»), unanswered for a documented
  and explicitly-admitted input. §UC-206 and §UC-207 both mint through `Committed`, so the published
  falsifiers of §INV-110 never exercise the barrier door at all — the invariant is certified by a
  suite that cannot see the hole.
- **Why this timing:** It decides what `Mark` contains and whether `Wait` refuses a `Park` beside a
  `MarkOf` mark — the shape of a frozen type and of `Wait`'s door.
- **Close criteria:**
  - [x] §5.2 states what a `Mark` carries for each constructor, and §1.1's `mark.envelope()` is
        reconciled with "a number a store produced".
  - [x] `Wait`'s contract states what the park question is for a barrier-minted mark: refused
        beside a non-nil `Park`, skipped with `Reached` meaning delivered only, or answered by some
        other key — with the choice argued.
  - [x] §UC-211's third arm carries the park consequence, and a use case pairs a barrier mark with a
        parked sequence, with §UC-206's `Committed` arm as the control.
  - [x] §INV-110's statement names both doors and §INV-113's "applied" clause says which marks can
        reach it.
- **Status:** **closed** — `Mark`'s contents are specified per door and the park question is defined
  for both. `Committed` is now a method on `WaitSpec`, reads the commit's **whole range** (a
  `Sequencer` is any function of an envelope, so a commit can span sequences and a mark that carried
  only the last event's key would ask about a third of what the caller is waiting for), and attaches
  the distinct sequence keys its own sequencer answered. `MarkOf` attaches none, and `Wait`
  **refuses** a mark with no keys beside a non-nil `Park` — with the three alternatives (skip, ask
  `Holes`, mint with a sequencer) argued and rejected, the middle one because it would move a second
  sentence of `Park`'s contract. A mark holds no `Envelope` and no payload and renders `"[mark]"`,
  which settles the INV-042 question §1.1 raised. §UC-211 gains a fourth arm with its `Park`-nil
  control, §UC-243 is the two-sequence commit, and INV-108/110/113 carry it.

---

### GAP-8 [high][immediate] `Wait` has no exit for a poll that fails, and three of its four per-poll calls can fail for reasons that are ordinary

- **Where:** §1.1 ("The verdict, the deadline, and why there is no stale-read flag" — "Three
  exits"), §1.1 ("What the wait compares"), §5.2 `Wait`, §UC-212, §INV-112, §INV-113.
- **What:** The published exits are Reached, Parked, Deadline and (bare) Cancellation. Every poll
  makes up to three store calls that can answer an error the spec never routes:
  - **`surveyed`** — the shared walk `Wait` reuses. `Observe`'s published contract answers
    `ErrTopology` when some cover members hold rows and some do not, and refuses a member whose row
    is missing beside a retiring-split row. Both are states a live deployment **passes through**
    during a supported operation (a split, a fresh generation's first pass), so a wait spanning one
    gets a refusal rather than a slow poll.
  - **`Park.Sequences` / `Park.Holds`** — the projection's own loop treats an error from these as
    *"a read of your own table that blinked"* and retries without limit; a wait has no such rule.
  - **`Checkpoints.Load`** — a transient backend error.
  What `Wait` returns in each case, whether it is terminal or retried until the deadline, and what
  `Visibility` carries beside it, are all unspecified. §UC-212 covers a slow projector and a stopped
  one; nothing covers a failing one. §INV-113 states what `Reached` promises and says nothing about
  what a non-`Reached`, non-deadline return means.
- **Why this severity:** The failure surface of the phase's headline API is incomplete, and the two
  plausible readings are both bad: terminal makes an ordinary split abort a caller's wait with a
  topology error it cannot act on; retried-forever hides a checkpoint store that is down behind a
  deadline that reads as "the projector is behind" — which is Marten issue #3912, the defect §1.1
  cites `Moved` as the answer to.
- **Why this timing:** DX law: it is the returned-vs-raised question for a new public call, and it
  decides whether `Visibility` needs a field. Callers branch on this.
- **Close criteria:**
  - [x] `Wait`'s contract lists a fifth exit and says which errors are terminal and which are
        polled through, naming `ErrTopology`, a `Park` error and a `Checkpoints` error.
  - [x] The `Visibility` returned on that exit is specified.
  - [x] A use case: a cover that is mid-split while a wait is running, with a control where the
        split has completed; and a use case where `Checkpoints.Load` fails once and then succeeds.
  - [x] §INV-113 or a new invariant states what a wait promises when it returns neither `Reached`
        nor a deadline.
- **Status:** **closed** — `Wait` publishes a fifth exit and a rule with no knob in it: **a poll that
  fails is terminal on the first poll and is polled through after it.** A refusal that was there
  from the start is the caller asking wrong — a wrong cover, a `Checkpoints` pointing elsewhere, a
  `Park` that refuses outside a unit — and is returned on the round trip the caller was paying
  anyway; a refusal that appears later is the deployment moving (a split, a blinked read, which the
  projection's own loop treats the same way) and the deadline decides. The deadline's error wraps
  the last poll's refusal as a third `%w`, so a checkpoint store that is down does not read as "the
  projector is behind" — Marten #3912 with a new spelling, which is the defect `Moved` is cited as
  the answer to. §UC-244 is the case, with a mid-split cover and a fail-then-succeed arm; INV-113
  says what a wait that returns neither `Reached` nor a deadline has concluded, which is nothing.

---

### GAP-9 [high][immediate] `WaitSpec` restates the projection's declaration, and three of its fields are silently wrong-able by a caller that does not read the projector's wiring

- **Where:** §1.1 ("Naming the wrong sequencer is unverifiable from here"), §5.2 `WaitSpec`,
  §INV-111, §UC-206's control.
- **What:** To ask "is my change visible", a request handler must supply `Checkpoints`, `Park`,
  `Sequence`, `Of` (projection **and** generation), `Over` (the cover), `Every` and `Ticks` — i.e.
  it must restate, at the read path, five facts that the projection's own `Spec` already declares at
  the composition root. Three of the five are silently wrong-able:
  - **`Sequence`** — §1.1 admits it: *"Name another and the key is one no letter was ever parked
    under, `Holds` answers false, and the wait reports reached for a parked event. Nothing here can
    check it."*
  - **`Park: nil`** beside a parking projection — §UC-206's own control proves this answers
    `Reached: true` for a parked event. It is the default zero value of the field.
  - **`Over`** — a cover that is not the one the rows were recorded at. §1.1 relies on `surveyed`'s
    refusals to catch the mismatched case, which catches a *partial* cover and not a plausible wrong
    one.
  The justification offered is a consequence of the chosen shape, not an argument for it: *"`Wait`
  holds a `Checkpoints` and a `Park`, never a `Spec`"*. Nothing in the document asks why it does not
  — no `WaitOf(spec)`, no waitable value published by the runner or by `Generations`, no considered
  alternative with a cost. The phase-4 precedent is the opposite one: `Cutover` refuses a
  caller-supplied barrier *because a value a caller can invent is not evidence*, and `Identity`,
  `Cover` and `Mark` all exist so a caller cannot write the dangerous value by hand. Here the
  dangerous values are three plain fields.
- **Why this severity:** DX law — the caller must know the projector's implementation to use the
  read-path API correctly, and each of the three wrong answers is `Reached: true` for a change that
  is not in the read model, which is the one outcome ES-05 exists to make impossible. An invariant
  (§INV-111) that says "nothing checks this" is a report of the gap, not its closure.
- **Why this timing:** `WaitSpec` is the frozen surface; deriving it from a `Spec` later changes
  every call site, every example and the module page.
- **Close criteria:**
  - [x] The spec considers a constructor that derives `Checkpoints`, `Park`, `Sequence`, `Of` and
        `Over` from the value the host already holds, and either specifies it or records why it is
        refused with its cost — in the shape §1.2 uses for its refused alternatives.
  - [x] If the fields stay, the module page states the three wrong-able ones together, and the
        obligation is stated beside `Sequencer`'s existing three as §1.1 promises for `Sequence`
        alone.
  - [x] A use case per wrong-able field — wrong `Sequence` (already promised by §INV-111's
        falsifier), `Park` nil (§UC-206's control), wrong `Over` — each with its correct control.
- **Status:** **closed** — by derivation rather than by documentation. `WaitOf(spec, over)` takes the
  `projection.Spec` the runner was built from and the `Cover` it was built out of, and derives
  `Checkpoints`, `Park`, `Sequence` (applying `ByStream()` where the spec's is nil, which a
  hand-written spec gets wrong by leaving it zero), `Generations` and `Of`. The request path fills in
  `Until` and nothing else. The hand-assembled struct stays legal, for the reason `Cover` gives for
  its own set, and it is where the three obligations live — stated together on the module page
  rather than one of them in an invariant. Because the sequencer used to mint the mark is now the
  spec's own, a derived spec cannot name a sequencer the projection does not run at all. §UC-242 is
  the derived arm against three hand-written wrong ones, each asserting its wrong answer; INV-111 is
  rewritten around the derivation.

---

### GAP-10 [high][immediate] Nothing scopes a wait to the generation the caller's read will resolve to, and the cutover window is deferred to the plan while the surface is frozen

- **Where:** §8 tension 8, §5.2 `WaitSpec.Of`, §1.1 ("What success does NOT give", item 1),
  §INV-113, Group BA (no case).
- **What:** `Wait` takes an `Identity` — a projection **and a generation** — and the caller must
  know which generation its read path resolves to. §8 records the consequence and hands it to the
  plan: *"Waiting on the retiring generation during a cutover window answers about a read model the
  caller is no longer reading. The plan should decide whether `WaitSpec` may take a `Generations`."*
  That is a question about the shape of a frozen struct, recorded as a tension rather than answered,
  and Group BA contains no case for it. The two failures are opposite and both silent:
  - the caller names the **retiring** generation, which is ahead; `Wait` answers `Reached: true`;
    the caller reads the **arriving** generation's read model, which has not applied the change —
    a stale read *with* a successful wait, which is precisely the outcome ES-05 forbids;
  - the caller names the **arriving** generation before its cutover; the read still resolves to the
    retiring one, and the wait burns the deadline for a read model that was already current.
  §INV-113 does say a wait promises nothing about *"a second generation"*, which makes the scope
  honest — it does not make the caller able to name the right one, and `Generations.Active` is a row
  that can move under a wait that already started.
- **Why this severity:** A supported, documented, first-class operation of the previous phase
  (cutover) turns this phase's guarantee into its exact negation, with no error on any path. The
  appendix's own wording is «store-issued barrier **конкретного** projection generation» — naming
  the generation is the mechanism, and nothing helps the caller name the live one.
- **Why this timing:** Whether `WaitSpec` takes a `Generations` (or whether `Wait` refuses an
  identity that is not `Active`) is a surface decision; §5.2 freezes the struct without it and §8
  asks the plan to revisit it, which is a rewrite of the contract after the fact.
- **Close criteria:**
  - [x] The spec decides whether `Wait` resolves the generation itself, refuses a non-active one, or
        requires the caller to name it — with the cost of the chosen answer stated (a row that can
        move under the wait, for the first).
  - [x] Group BA gains a case for a cutover that completes mid-wait, in both directions, with a
        control where no cutover happens.
  - [x] §INV-113 states what `Reached` means when the generation it names stops being the one reads
        resolve to.
- **Status:** **closed** — `WaitSpec` gains an optional `Generations` and the phase publishes
  `ErrGeneration`. When it is supplied, `Wait` reads `Active` on its first poll and again on the poll
  that would answer `Reached`, and refuses when the answer is not the generation `Of` names: the
  retiring-generation case becomes a refusal instead of a stale read with a successful wait, and the
  arriving-generation case costs one round trip instead of a deadline. Twice rather than per poll is
  argued (those are the two moments the answer changes anything), and the residual — the row can
  move between the wait's last read and the caller's own read — is named as the cutover's own
  overlap window seen from the read path's side rather than implied shut. A nil `Generations` stays
  the honest spelling for a deployment tool. §8 tension 8 is replaced by a narrower one (how often to
  re-read the row); Group BA gains §UC-245 with both directions and a `Generations`-nil control;
  INV-113 states the scope.

---

### GAP-11 [high][immediate] `Park.Holes` is asked outside a unit of work too, so §0's "one sentence of `Park`'s contract moves" is wrong and a `Park` implementer is not told

- **Where:** §0 ("What does move is one sentence of `projection.Park`'s contract"), §1.1 (the
  `Visibility.Holes` field and *"`Holes` is read only when the census reports `Quarantined > 0`"*),
  §5.2 ("One contract sentence moves, and a baseline cannot see it"), §7.1, §UC-208, §INV-110.
- **What:** The published `Park` contract assigns each of its four methods a place: `Holds` and
  `Park` inside the caller's unit, `Sequences` outside it, and *"`Holes` is a cutover's question and
  no pass asks it"* — where a cutover runs *"inside the unit **you** open"*. `Wait` now asks
  **`Holes`** as well, from a caller's goroutine with no unit at all, and the document announces the
  widening for `Holds` only: §0, §5.2 and §7.1 all say one sentence moves, and the release note the
  spec schedules will say the same. A `Park` implementation that resolves its executor from the
  ambient unit — which is what the shipped contract permits for every method except `Sequences` —
  refuses or misreads when a wait calls `Holes`, and by §UC-208's own budget that only happens on a
  generation with `Quarantined > 0`, i.e. in exactly the deployments that already have a problem.
- **Why this severity:** It is an unannounced change to an interface third parties implement, on a
  path a signature baseline and `check-event-kernel` both cannot see — which is the reason the
  document gives for announcing the `Holds` widening in three places. Missing one of the two is the
  same defect the announcement exists to prevent.
- **Why this timing:** The announcement is part of the deliverable (§5.2, §7.1, the module page and
  the release note), and an implementer who reads the wrong list writes the wrong `Park`.
- **Close criteria:**
  - [x] §0, §5.2 and §7.1 say how many sentences of `Park`'s contract move and name each.
  - [x] `Holes`'s contract states that a wait asks it outside a unit and that an implementation must
        answer the committed state there — or `Wait` stops calling it and `Visibility.Holes` is
        sourced differently or dropped.
  - [x] §UC-208's call-count budget asserts the placement, not only the count.
- **Status:** **closed** — by deletion, which is what "prefer a change of shape" looks like here.
  **`Wait` no longer asks `Holes` at all** and `Visibility.Holes` is dropped: the number a waiting
  request handler acts on is `Parked`, a generation-wide hole count is a cutover's question, and
  `Visibility.Quarantined` already carries what the census read for free. So the announcement stays
  true at one sentence — `Holds`'s — and §0, §5.2, §7.1 and INV-110 now each **name** it and say
  there is no second one. §UC-208 asserts `Holes` is never called on any generation at any
  `Quarantined` count, and asserts the **placement** of the calls a wait does make: a recording
  `Park` checks that no transaction was bound on any of them.

---

## Deferred — written to [`EVENTSOURCE_BACKLOG.md`](EVENTSOURCE_BACKLOG.md) `## P5`, not fixed here

Nine `[medium]` and two `[low]`, recorded there in full and listed here by title only:

1. The appendix's «Deadline возвращает timeout/**degraded**» has no verdict — `[medium]`
2. `Repo.Digest`'s result is never tied to what `Repo.Append` writes, and no obligation is stated — `[medium]`
3. `Mark`'s contents and rendering are unspecified — `[medium]`
4. `ResolveSpec.Store` has no stated purpose — `[medium]`
5. §INV-110 and §INV-112 state call orders rather than falsifiable properties — `[medium]`
6. ES-09: the `SnapshotFilter` composition rule (study nuance ES-09/3) has no answer in D-145 — `[medium]`
7. ES-09: «оператор может отбросить snapshot» has no invariant — `[medium]`
8. ES-09: "a snapshot decode that panics falls back" assumes a `recover` nothing authorises — `[medium]`
9. `Visibility.Behind` is an `event.Position` used as a distance, again — `[medium]`
10. A wait against a generation with no checkpoint rows at all is unstated — `[low]`
11. §0 says "moves three kernel names" and lists four — `[low]`
