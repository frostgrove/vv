# EVENTSOURCE — phase 3 (projections, checkpoints, catch-up, snapshots) usecases — GAPS

## Round 1 — spec auditor (coverage / invariants / DX, non-monotone visibility) — 2026-09-08

Audited: [`EVENTSOURCE_P3_USECASES.md`](../usecases/EVENTSOURCE_P3_USECASES.md) against
[`EVENTSOURCE_P1_USECASES.md`](../usecases/EVENTSOURCE_P1_USECASES.md) (§UC-036, §UC-037, §UC-053,
§INV-008/009/013/021/025/027/034/035/036/042/045),
[`EVENTSOURCE_P2_USECASES.md`](../usecases/EVENTSOURCE_P2_USECASES.md) (§2.2–2.7, §UC-090–098,
§INV-046–065), the frozen code (`event/store.go`, `event/reader.go`, `event/fact.go`,
`event/eventpg/read.go`, `event/eventpg/cursor.go`, `event/eventpg/watermark_integration_test.go`),
`runtime/{runner,supervisor,loop,periodic}.go`, `jobs/{policy,handler_error,attempt,disposition}.go`,
`scripts/event_test.go`, and [`EVENTSOURCE_BACKLOG.md`](EVENTSOURCE_BACKLOG.md) `## P2` §5, §59.

**The central question — verified against the code, not against the spec's account of it.**
I walked the parent's interleaving through `ReadAll` (`event/eventpg/read.go:131`) by hand. Writer A
draws position 5 and stalls; writer B draws 6 and commits; the projector reads from a checkpoint at
`from = 4`:

- `readCursor` → `walk{from:4}`; `fetch` → rows `[6]`, `floor = pg_snapshot_xmin`.
- `deliverable([6], from=4, settled=settledAt(floor)=4)` → `6 != 5 && 6 > 5` → **0 delivered**.
- `spent` is true (`bound == 0`), so a bound `B = pg_current_xact_id()` is minted, `reach = 6`, and
  because `count == 0` the page and the floor are re-fetched (step 8). A is still running and — by
  §INV-065's `events_position_needs_xid` trigger — held an xid **before** it drew 5, so
  `floor ≤ A.xid < B`, `settledAt` returns `from`, and the second pass delivers nothing either.
- The returned cursor is `(from=4, bound=B, reach=6)`; §INV-067 forbids saving it, so the
  **checkpoint does not move**. After A commits, `floor > B` → `settled = 6` → `[5,6]` are delivered
  in that order. Had A rolled back, `6 ≤ settled+1` → 6 is delivered and the gap is burnt.

**Event 5 is delivered. §1's checkpoint shape is correct and §UC-113 is a real case.** The
"checkpoint = the store's own cursor + a fence" decision is the right one and is not a finding
anywhere below. Nor is any spec-level hardcode: the defaults (`Idle`, `Backoff`, `Attempts`,
`Tolerate`), the snapshot trigger (§5.2, expressed as a p99 a deployment measures in its own
environment, with the link-latency term stated), and the illustrative `orders` / `orderPlaced` names
are all parameters or examples, never a fixed shape — **`universality.md`: clean, nothing found**.
`MaxCursorBytes` being non-configurable is argued and correct.

What is wrong is everything hanging **off** that cursor: what `InUnit` actually promises, what the
checkpoint may pass, what `Progress.Highest` is a statement about, who owns the page across a retry,
what a halted loop does, and what the projection is allowed to believe from a checkpoint store.

Seven blocking findings follow. `[medium]` and `[low]` go to
[`EVENTSOURCE_BACKLOG.md`](EVENTSOURCE_BACKLOG.md) `## P3` under the 2026-09-08 policy and are
listed there, not here.

---

### GAP-1 [high][immediate] `InUnit`'s atomicity promise has an unstated precondition, and the check the spec defines cannot see it

- **Where:** §3.4 ("In `InUnit` mode a crash before the commit rolls back the handler's writes *and*
  the advance together"), §3.1 ("The two may be one resource … or two"), §3.2(3), §INV-070,
  §UC-105, §UC-107, §12.7.
- **What:** The property that makes `InUnit` worth anything is that **the handler's writes, the
  `Unit`'s transaction and the checkpoint store's transaction are one transactional resource**. The
  spec never states that as a requirement. The three things it does check —
  `Unit != nil`, `Checkpoints.Capabilities().Transactions == Supported`, and a valid authority from
  `Checkpoints.Transaction(ctx)` inside the unit — are all satisfied by a wiring in which the
  handler writes somewhere else entirely: `Unit` opens a transaction on database X, the checkpoint
  table is in X, the read model is in Y. Nothing in §3, §9.2 or §UC-107 refuses it, nothing observes
  it, and §3.4 tells that consumer in as many words that a crash before the commit leaves the read
  model "exactly as it was". It does not: Y keeps the rows, X rolls the advance back, and the
  redelivered page double-applies — against a consumer who was told they did not need idempotency
  for the transactional part.
  §12.7 shows the spec is aware of the case ("a read model elsewhere … gets `AfterApply` semantics")
  and files it as a plan tension. That is precisely the **silent downgrade** §INV-070 says is
  impossible. An invariant and a named tension cannot both be right.
- **Why this severity:** This is the worst thing a consumer can write that compiles, runs, returns
  no error and silently double-applies. A non-idempotent read-model handler (a balance increment, an
  append to a ledger table, a counter) configured `InUnit` against a checkpoint store that is not
  its own resource corrupts the read model on every crash, and every test over a process that does
  not crash is green — §UC-107's own words for why the downgrade must be refused.
- **Why this timing:** It decides the shape of `Spec` and of `New`'s refusal set. If the framework
  is to refuse or to declare the alignment, that is a construction-time contract other sections
  (§UC-105, §UC-107, the conformance `transactions` section, the module page's delivery row) are
  written against. Retrofitting it after `InUnit` ships is a breaking change to a promise consumers
  will already have relied on.
- **Close criteria:**
  - [x] §3.4's atomicity sentence carries its precondition explicitly: the handler's writes must be
        made inside the same transactional resource the `Unit` opens and the checkpoint store joins,
        and `InUnit` says nothing about any write outside it.
  - [x] The obligation is written where §5.4 already writes its unenforceable ones — beside key
        injectivity (§UC-050) and the changed-fold rule — as a load-bearing promise the framework
        cannot check, **or** a check is specified (e.g. the projection compares
        `Checkpoints.Backing()` with a backing the `Spec` names for the read model) and §INV-070 is
        rewritten to cover it.
  - [x] §INV-070's statement is amended so it is not falsified by §12.7's own case, and its
        "Falsified by" gains a case: an `InUnit` projection whose handler writes to a second
        resource, asserting the specified outcome (refusal at `New`, or a documented
        `AfterApply`-equivalent guarantee) rather than the atomicity §3.4 currently promises.
  - [x] A use case exists for "a projection writes to a database this framework does not know",
        which the parent's universality list names and which no case in Groups T–Y covers.
- **Status:** closed 2026-09-08 — §3.4 gains an explicit precondition and a stated,
  unenforceable obligation in §UC-050's own words; §3.2(3) grows from three checks to
  four, the fourth resolving the new `Spec.Destination` **inside the unit and before the
  handler runs** and refusing the pass when what is bound for it is not a transaction;
  `projection.Unchecked` is the only way to opt out and it cannot be reached by leaving a
  field zero, so `InUnit` with no `Destination` is `ErrSpec` at `New` (§UC-107(c)).
  §INV-070 is restated as "checked as far as it can be seen, downgraded never silently"
  with its boundary written into the invariant, and §UC-128 is the new case — a read model
  in a database this framework does not know, three wirings, three asserted outcomes,
  live at §10(4). §12.7 is no longer a tension: what remains for the plan is only the
  worked example.

### GAP-2 [high][immediate] §INV-067 and `Quarantine` cannot both be implemented

- **Where:** §INV-067 ("A checkpoint is saved **if and only if** the page it names was applied … A
  page that failed issues none"), §3.4's failure table (`Permanent` → `Quarantine` when a sink is
  supplied), §UC-110 ("the page's envelopes are recorded and **the checkpoint advances past them**"),
  §INV-073.
- **What:** Two normative statements in one document that contradict each other. Under
  `OnPermanentFailure: Quarantine` the checkpoint advances past a page that was **not** applied,
  which is exactly what §INV-067 forbids and exactly what §INV-073 names as the one permitted
  exception. §INV-067's scope is simply wrong — it says "applied" where the loop has three
  terminal outcomes (applied, quarantined, halted) and two of them save.
- **Why this severity:** The invariants are what the tests assert. An implementer who writes
  §INV-067's falsification literally ("the stored advance is unchanged across a failed page") makes
  `Quarantine` unimplementable and the projection stalls in the one mode that exists to keep going;
  an implementer who writes §UC-110 makes §INV-067 red. One of the two ships broken and the review
  round that catches it is a round spent on a contradiction that was written down.
- **Why this timing:** It is the save rule. Every other statement about the checkpoint — §1.3(3),
  §3.2(4), §6's comparison, the `eventtest` `fence` section — is stated over "the page was applied",
  and the fix changes what that phrase means everywhere it appears.
- **Close criteria:**
  - [x] §INV-067's statement names all three outcomes: a page that was applied saves once, a page
        that was quarantined in full saves once, a page that failed under `Halt` saves none, an
        empty page saves none.
  - [x] Its "Falsified by" gains the `Quarantine` case, asserting the stored advance moved **and**
        the sink holds exactly the envelopes the advance passed (§INV-073 already asks for the
        second half).
  - [x] §UC-110 states what happens to a page that is **partly** quarantined — the handler failed on
        one envelope and the rest applied — or says the granularity is the page and the sink
        receives all of it (§12.4 leaves this to the plan; §INV-067 cannot be closed without the
        answer).
- **Status:** closed 2026-09-08 — §INV-067 is restated over **four** ends of a page
  (applied, quarantined, failed under `Halt`, empty) and there is no partial save.
  Granularity is decided rather than deferred: quarantine is **envelope-granular**, bought
  by re-delivering the failed page one envelope at a time (§3.4), so one corrupt payload
  no longer drops up to `MaxRead` applicable events; a retryable failure in the isolation
  pass returns the whole page to `Retrying` rather than quarantining anything. §INV-073
  now reads "records every envelope it passes **without applying**", §UC-110 asserts the
  sink holds one envelope and `Progress.Quarantined` rose by one rather than by the page's
  length, and §12.4 keeps only the narrow residue (whether the sink joins the unit).

### GAP-3 [high][immediate] The cutover rests on a comparison that is not a statement about content, and nothing compares the two read models

- **Where:** §6(2) ("`new.Highest ≥ old.Highest` means the new projection has **applied** every
  committed event the old one has"), §1.3(3) (the theorem, stated over what a walk has **delivered**),
  §9.1 (`Progress.Highest` — "The highest position this walk has **delivered**"), §UC-121's Control,
  §10(11).
- **What:** Three defects in one mechanism.
  1. **Delivered ≠ applied.** §1.3(3) is a theorem about delivery. §6 upgrades it to a statement
     about application without saying what closes the gap. The spec never says at which point
     `Progress.Highest` is computed — at the read, after `Apply`, or at the `Save` — so a
     projection that is retrying or halted on the page it just read may publish a `Highest` that
     covers events it never applied.
  2. **`Quarantine` breaks it outright.** In quarantine mode `Highest` advances over events that
     were deliberately not applied, so `new.Highest ≥ old.Highest` can hold for a read model that is
     missing rows. With GAP-4's `SkipUnknown` default it can also hold for a projection that never
     handled a whole event type.
  3. **Nothing compares the destinations.** §UC-120, §UC-121 and §10(11) assert phases,
     non-interference and `Highest` — and never that the rebuilt read model equals the live one.
     §5.4, for a feature that is **deferred**, demands a full-replay-equivalence obligation; §6, the
     feature that ships, has none.
- **Why this severity:** This comparison is the framework's only contribution to an irreversible
  production decision. An operator reads two `State()` values, sees `new.Highest ≥ old.Highest`,
  flips the reads, and serves a read model that is silently missing every quarantined or unrouted
  event. That is a secondary-scenario break with data-visible consequences, and §6 is one of the
  five in-scope mechanisms.
- **Why this timing:** `Progress` is a persisted struct with persisted columns (`highest`,
  `applied`) in the v2 schema. Redefining what `Highest` counts after the migration ships is a
  schema and a contract change, and §6 is documented in a usage guide rather than encoded in an API,
  so the words are the whole of the mechanism.
- **Close criteria:**
  - [x] §9.1 and §6 use one word. `Progress.Highest` is defined at exactly one point in the pass and
        the point is stated (recommendation: it is what the **saved** checkpoint carries, so it is a
        statement about applied-or-quarantined and never about delivered).
  - [x] §6(2) states the comparison with its exclusions: it is sound only between two projections
        that are both under `Halt`, and it says what an operator compares when either is running
        `Quarantine` or `SkipUnknown`.
  - [x] §UC-120 or a new case asserts **content**: with both projections in `PhaseFollowing` over a
        quiescent log, the two destinations hold the same rows, and §10(11) drives it live.
  - [x] `State`/`Progress` carries the counts that make the comparison honest — quarantined and
        skipped — or §6 says in as many words that `Highest` is not a completeness claim.
- **Status:** closed 2026-09-08 — one word, and it is `Progress`. §1.2 fixes the point of
  computation: a `Progress` is written **only at the save**, so `Highest` is
  applied-or-quarantined and never delivered, and §1.3(3) carries the theorem across that
  bridge explicitly. `Progress` gains a persisted `Quarantined` count and the v2 table a
  `quarantined` column. §6(2) is rewritten as a **consumption** comparison with its three
  exclusions written out (not completeness, not routes, one instant of two moving values),
  and §6 gains a third contribution: what decides a cutover is the **content** of the two
  destinations. §UC-120 now asserts the rows agree over a quiescent log and carries a
  deliberately-wrong control that reaches the same `Highest` with different rows; §10(11)
  drives it live. `SkipUnknown` no longer breaks the comparison because it no longer
  exists in the shape that did (GAP-4).

### GAP-4 [high][immediate] The typed reading seam has no use case, and its default silently drops events forever

- **Where:** §2 in-scope item 4, §3.3 (`projection.Router`, `On`, `TryOn`, `SkipUnknown` as the
  zero value), §9.2, §9.1 (`Fact.Read`), Groups T–Y, §10.
- **What:** `Router`, `NewRouter`, `On`, `TryOn`, `Unknown`, `SkipUnknown`, `RefuseUnknown`,
  `Fact.Family` and `Fact.Read` are nine exported names and one of the five in-scope mechanisms, and
  **not one use case exercises them on the happy path**. §UC-111 covers `Fact.Read` refusing; no
  case covers it succeeding, no case covers a projection over two aggregates (the parent's
  universality item), no case covers `RefuseUnknown`, and §10's live list never mentions the router.
  Worse, the default is `SkipUnknown` **and nothing observes a skip**: `State` carries a quarantined
  count but no skipped count, and no `Observer` transition is specified for it. A handler author who
  forgets one `projection.On` registration, or renames a wire type, gets a projection that compiles,
  runs, returns no error, reaches `PhaseFollowing`, advances its checkpoint and silently drops every
  event of that type for the life of the deployment — and by GAP-3 it then passes the cutover
  comparison.
- **Why this severity:** The document's own rule is "`Halt` is the default and skipping is never the
  default … skipping one event makes [the state] a function of 'the log minus whatever failed',
  silently and forever". `SkipUnknown` is that sentence's exception and it is unobserved. A missing
  registration is a far likelier mistake than a corrupt payload, and the framework's answer to the
  corrupt payload is a halt while its answer to the missing registration is silence.
- **Why this timing:** It is the zero value of an exported enum on the primary handler seam and the
  shape of `State`. Both are public contract, and `SkipUnknown` being `iota` is the thing every
  caller gets by writing nothing.
- **Close criteria:**
  - [x] A happy use case for the router: two aggregates, several types, `Fact.Read` decoding through
        a declared chain, an event of an unhandled type — asserting what the read model holds and
        what was reported.
  - [x] A use case for `RefuseUnknown` and a stated interaction with the classifier (is an unknown
        type under `RefuseUnknown` a history-class `Permanent`, i.e. a halt?).
  - [x] A skip is observable: a counter on `State`, or an `Observer` transition, or — if the answer
        is that a skip is invisible by design — a sentence in §3.3 saying so and an invariant
        pinning that a projection's state is a function of the log **and its declared routes**.
  - [x] §10 gains a live item that drives a router with a deliberately missing registration and
        shows the difference is visible from outside.
- **Status:** closed 2026-09-08 — by a change of shape rather than a counter. The router's
  covered families are now **inferred** from the facts registered on it, and inside a
  covered family an unclaimed type is `ErrUnrouted`, permanent by the default `Classify`,
  and a halt that no policy relaxes — so the forgotten `On`, the renamed wire type and the
  type a newer deploy started writing are all loud. `Ignore(router, family, types...)`
  declares a covered family's type as deliberately not this projection's, by name so a
  type with no local fact can be named and a rename goes stale in the safe direction.
  `Unknown`/`SkipUnknown`/`RefuseUnknown` become `Foreign`/`SkipForeign`/`RefuseForeign`
  and govern **only** families the router covers no route of, where skipping is the honest
  default; `Router.Skipped()` counts those and §3.3 says in as many words why they are not
  on `State`. §INV-081 pins the pair, §UC-130 is the happy case (two aggregates, five
  facts, an upcast, an `Ignore`, a foreign family) with the missing-registration control
  on both sides of the line, and §10(12) drives it live.

### GAP-5 [high][immediate] `Halted` is the terminus of three use cases and the spec never says what the loop does there

- **Where:** §3.2's diagram and §3.2(6), §3.4's failure table (`ErrClosed`, `ErrRefused`,
  `ErrCursor`, `ErrConflict`, `Permanent` all → halt), §3.5, §UC-101, §UC-110, §UC-116, §UC-119.
- **What:** The spec says exactly one thing about the halted state — that `Run` must not return —
  and nothing about what the loop *does*. It does not say whether the projection keeps reading the
  log (a poll against the database forever, for a projection that will never apply anything again),
  keeps re-applying the failing page (a retry loop the classifier said was pointless), sleeps on the
  ticker, or blocks on `ctx.Done()`. It does not say whether `Drain` returns promptly from a halted
  loop, whether `State()` keeps publishing, whether a transition is published once or per pass, or
  whether a halt can ever clear without a restart (§UC-111's control assumes a restart).
- **Why this severity:** Three use cases and five rows of the failure table end here, so it is not
  an edge state — it is the failure mode of the whole package. The spec argues elsewhere that
  retrying a closed store is "a busy loop that never clears" and refuses it, which shows the
  document knows the cost; leaving the halted branch undefined re-opens exactly that. The plausible
  wrong implementations are a hot `for` loop burning a core, and a poll that keeps a dead
  projection's read traffic on the database indefinitely — both invisible to a unit test.
- **Why this timing:** It is the loop's structure, and `Drain`/`Ready` behaviour in that state is
  host-facing contract that §UC-118 and §UC-119 are written against.
- **Close criteria:**
  - [x] §3.2 states the halted branch: what it waits on, that it issues no read and no save, and
        that it publishes its transition once.
  - [x] §UC-118's Given is extended to a halted projection: `Drain` returns without waiting, and
        `Run` returns on cancellation.
  - [x] §UC-110 or §UC-119 asserts, over a bounded window, **zero** reads and **zero** saves after
        the halt — the same observable-count shape §UC-114 already uses for idle polls.
  - [x] The spec says whether a halt is terminal for the process's life, and §UC-111's control names
        the restart as the only exit (or specifies the other one).
- **Status:** closed 2026-09-08 — §3.2(6) now states the halted branch in full: the
  transition is published **once**, the loop then waits on the context and the drain signal
  alone and issues no read, no save and no handler call ever again; `Drain` returns at
  once; `State` and `Ready` keep answering; `Run` returns `ctx.Err()` and never
  `ErrHalted`. A halt is **terminal for that value's life** — §9.2's absent-API table gains
  a `Resume`/`Retry`/`Clear` row, and §UC-111's control names the restart as the only exit.
  §UC-118 gains the halted `Drain`, and §UC-119 and §UC-110 assert **zero** reads and zero
  saves over a bounded window with one transition — the observable-count shape §UC-114
  already uses.

### GAP-6 [high][immediate] The projection trusts everything a checkpoint store hands back

- **Where:** §3.1 (`Load`), §3.2's resume step, §UC-099, §UC-100, §UC-123's mutation harness, and
  the absence of any check in §3 or §9.
- **What:** Phase 1 and 2 built one discipline and stated it twice — "a store is trusted exactly as
  far as its numbers are, which is not at all" (`event/store.go:83`): `Bind` and `Read` re-check
  capabilities and limits, `Reader.checkPage` re-checks the page, `Store.promised` re-checks every
  scanned row. Phase 3 introduces a second store interface, invites third-party implementations
  ("so a third is provable"), and specifies **no check at all** on what `Load` returns. The
  projection is required to re-check `Checkpoints.Transaction(ctx)` inside the unit — the spec even
  argues why ("what makes the `InUnit` claim checkable rather than assumed") — and then accepts a
  `Checkpoint` on faith. A `Load` that ignores its `projection` argument, or whose query is
  mis-parameterised, hands back another projection's cursor: it is well-formed, it is of the right
  log, `ErrCursor` never fires, and the projection resumes hundreds of thousands of positions ahead
  and silently skips everything in between, forever, with no error on any path.
- **Why this severity:** Silent event loss through a supported extension point, closed by one
  comparison. The conformance suite catches it only for an implementer who runs it, which is exactly
  the argument the kernel already rejected for `Store`.
- **Why this timing:** It decides whether `event.Checkpoints` has a kernel door (a small wrapper, as
  `Reader` is for `Log`) or is called raw by `event/projection`. That is structural: adding the door
  later moves the type every consumer names.
- **Close criteria:**
  - [x] §3.1 or §3.2 states what the caller re-checks on `Load`: `Projection` equals the name that
        was asked for; `Advance == 0` iff the cursor is empty; the cursor is within
        `MaxCursorBytes`; and what the refusal is when it does not (a wiring-class refusal, not a
        halt with a store error).
  - [x] Where the check lives is decided — in `projection`, or in a kernel helper beside
        `event.Read` so a second consumer of `Checkpoints` inherits it.
  - [x] An invariant states it and is falsified by a checkpoint store that answers a foreign row,
        paired with the honest store as the control, so the check is discriminating rather than
        universal.
  - [x] §UC-101's "Must not … must not overwrite the checkpoint it could not read" is extended to
        this case, so a mis-answered `Load` is not overwritten either.
- **Status:** closed 2026-09-08 — with a kernel door, which is the structural answer the
  finding asked to have decided. `event.Track(checkpoints, name)` answers a `*Tracker` that
  is to a `Checkpoints` what `*Reader` is to a `Log`: nothing calls a `Checkpoints` raw. It
  re-checks every `Load` — the answer is about the name that was asked for, absence is
  total, the cursor is inside `MaxCursorBytes` — and refuses with `ErrWrongStore`, the
  existing wiring class, terminal, no new sentinel (§INV-076). It also **owns the fence**:
  `Save(ctx, cursor, progress)` takes no advance, so no caller can present a wrong one
  (§INV-069). §UC-129 is the case with the honest store as its control, §INV-082 is the
  invariant, §UC-101's "must not overwrite" is extended to a refused row, and
  §UC-123/§9.4 keep the defect in the suite as well so an implementer is still told.

### GAP-7 [high][immediate] The page is given away and then reused by the retry

- **Where:** §3.3 ("The page belongs to the handler from the moment it is handed over, indefinitely,
  from any goroutine (§INV-021's hand-off rule)"), §9.2 (`Batch` — "Yours from the moment it is
  handed over, including the slice's capacity"), §3.2(5) ("A retry re-applies **the page it holds**,
  and does not re-read"), §UC-108, §UC-109.
- **What:** The spec grants the handler full ownership of `Batch.Envelopes` — under §INV-021 that
  explicitly includes writing into it — and then hands the **same** slice back on attempt 2, 3 and
  10. Both cannot hold. Two concrete failures follow:
  1. A handler that sorts, filters, redacts or compacts the page in place (all legal under the
     ownership grant) and then returns a retryable error is re-applied over a **mutated** page. The
     checkpoint then advances as if the true page had been applied, and the events the handler
     dropped from the slice are never delivered again. Compiles, runs, returns no error, loses
     events.
  2. §3.3 grants "from any goroutine, indefinitely". A handler that fans the page out to workers and
     returns a retryable error on a partial failure now has attempt 2 reading the same backing array
     while attempt 1's workers still write it — the framework has handed one mutable array to two
     parties, which is the one thing §INV-021 exists to forbid.
- **Why this severity:** It is silent event loss and a data race, produced by a handler doing
  something the spec's own hand-off rule permits, on the framework's primary consumer seam.
- **Why this timing:** It is the ownership contract of the exported `Batch` type and it decides the
  retry's implementation (re-clone per attempt, or narrow the grant). Both are public contract, and
  a comment on an exported struct is the thing a consumer reads.
- **Close criteria:**
  - [x] §3.3 and §9.2 state one rule for the page across attempts: either the grant is narrowed to
        "yours for the duration of this call, and the same envelopes are handed back on a retry", or
        the framework hands each attempt its own slice and says so.
  - [x] §UC-109's Then names what the second attempt receives, in the words the chosen rule uses.
  - [x] A case in which the handler writes into `Batch.Envelopes` and then fails retryably asserts
        the second attempt applies the original page — the control that a re-clone actually happened
        (or that the narrowed grant is what the module page says).
  - [x] §INV-021's hand-off table (phase 1) is cited with the hand-off it actually corresponds to,
        rather than to hand-off 4/7, which are about a page the framework will never touch again.
- **Status:** closed 2026-09-08 — the grant is kept and the framework pays. §3.3 states
  that **each attempt receives its own page**: a fresh `[]Envelope` and a fresh copy of
  every payload, the first attempt included, with the pristine page kept by the loop. The
  handler's ownership is therefore §INV-021's unnarrowed — keep it, read it from any
  goroutine, write into it — and the narrowed alternative is argued and rejected in the
  document, because a rule a caller breaks by doing the legal thing is the shape phase 1
  refused for the store's own hand-offs. The price is stated (one ~30 KB copy per page
  against ~360 µs of work at §5.1's measured rate). It is recorded as §INV-021's
  **eighth** hand-off — framework → handler, the first whose sender re-reads what it
  handed over — appended rather than folded into 4 or 7, and §11.2 owes the row. §UC-109
  gains the control: a handler that truncates, reorders and overwrites the payload bytes
  and then fails retryably, asserting the second attempt gets the log's page unchanged.

---

**Checked and found sound, recorded so a later round does not re-derive them:**

- The checkpoint shape (`Cursor` + `Advance` fence + `Progress` as observation), §1.1's three
  arguments against a position, and §1.5's rejection of the gap-tracking checkpoint — all correct
  against `event/eventpg/{read,cursor}.go`.
- The fenced `Save` statement (§3.1) is right on all four paths: advance 1 with no row inserts;
  advance 1 with a row conflicts (`c.advance = 0` is never true); advance > 1 with no row inserts
  nothing (`EXISTS` is false) and answers zero rows; two racers at one advance produce one winner.
- §INV-071's argument (the read is outside the unit) matches `read.go` step 7 and §UC-092 exactly:
  `!on.joined` is what refuses to mint a bound on a caller's transaction.
- No exactly-once claim anywhere, including §3.4's `InUnit` paragraph, which disclaims it in the
  same breath — subject to GAP-1, which is about an unstated precondition and not about wording.
- Every cross-reference into phases 1 and 2 that I sampled resolves and says what it is cited for,
  including the easily-wrong §INV-053 ("never on the pool" really is part of that invariant),
  §INV-046, §UC-091, §UC-092, §2.6's `pg_snapshot_xmin = pg_snapshot_xmax` falsification, and the
  numbering hand-off (P2 ends at UC-098/INV-065; P3 starts at UC-099/INV-066).
- Nothing in the spec starts a goroutine outside a `runtime.Runner`; `New` is I/O-free; `Ticks` and
  `Drainer`/`Readier` are the runtime's own seams and are used rather than re-implemented.
- §11.3's claim about `scripts/event_test.go` is accurate: `charged` really is
  `map[string]string`, one pattern per package, so the new row needs no change to the check's shape.
