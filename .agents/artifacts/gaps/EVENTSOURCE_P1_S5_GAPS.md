# EVENTSOURCE_P1 — implementation S5 — GAPS

## Round 1 — econv-impl-reviewer (clean context) — 2026-09-07

Reviewed against the **code**, not the plan's prose. Read in full:
`.agents/artifacts/plans/EVENTSOURCE_P1_PLAN.md` (§ Carried gaps C1–C10, § Microkernel
classification, § Contracts `event/eventtest/`, § S5, § Architecture metrics, § Disagreements),
`CLAUDE.md`, `/home/user/.claude/skills/econv/references/{microkernel,architecture,
building-blocks,data-integrity,universality,readability,restrictions,gaps}.md`, and every
file of `event/eventtest/`, `event/eventmemory/conformance_test.go`, plus the kernel files
the suite asserts against (`event/store.go`, `event/repo.go`, `event/reader.go`,
`event/errors.go`, `event/bounds.go`).

### Checkpoints, executed by the reviewer

| Command | Result |
|---|---|
| `go build ./... && go vet ./event/... && test -z "$(gofmt -l event)"` | clean |
| `go test -race -count=1 ./event/...` (twice) | `ok` event 1.272s / 1.266s, eventmemory 1.040s / 1.037s, eventtest 1.057s / 1.054s — no flake |
| `go test -list … ./event/eventmemory/ \| grep -c '^Test'` = 1 | ok |
| `go test -list … ./event/eventtest/ \| grep -c '^Test'` = 4 | ok |
| `go test -race -v -run TestTheMemoryStoreSatisfiesTheContract ./event/eventmemory/` | 20 sections reported, **18 passed / 2 not certified**, none skipped — byte-for-byte the list pasted into the plan |
| `go test -race -v -run TestATransactionCapableStoreSatisfiesTheContract ./event/eventtest/` | **19 passed / 1 not certified** (`durability`) — matches the plan |
| `make check` | `check-deps/tiers/utils/triplets/todo/replaces/tidy/otel-schema/workspace: ok` |

**The checkpoint output pasted into the plan is real.** So are the two capability rows
§UC-044 predicts by name.

### Metrics counted (not eyeballed)

| Metric | Threshold | Measured |
|---|---|---|
| Exported symbols, `event/eventtest` | 7 | **6** (`Tx`, `Factory`, `Run`, `RoundTrip`, `Keys`, `Families`) — exactly the plan's contract, no silent extra surface |
| Lines per non-test file | 400 | max **383** (`sections_read.go`); 13 files, 2411 lines total |
| Functions over 50 lines | 50 | **4**: `refusalClassesSection` 62, `streamIdentitySection` 58, `payloadOwnershipSection` 57, `concurrencySection` 55 |
| Function parameters | 4 | **4 breaches**: `probe.wroteWhatItSaid` 7, `probe.injected` 7, `probe.classified` 6, `probe.from` 5 |
| Nesting depth | 3 | **0 breaches** over 133 functions |
| Internal imports per file | 5 | max **2** (`fixtures_test.go`); every non-test file imports `event` and nothing else |
| Import cycles | 0 | **0** — `go list -deps ./event/eventtest \| grep -c eventmemory` = **0** |
| Kernel imports of a concrete extension | 0 | `grep -rn "eventmemory\|eventtest" event/*.go` (non-test) = **0 lines** |
| Package-level mutable state | 0 | **0** — three `errors.New` sentinels, never reassigned |
| Comment density, non-test | — | 163 / 2411 = **6%**, consistent with the tree's voice |
| Fakes a unit test of `probe` needs | 2 | **1** (a `Factory` over one `event.Store`) |
| `t.Skip` call sites | 0 | **0** — three words, never two, held |
| Statement coverage, `event/eventtest` | — | 83.5%; `RoundTrip` **60.0%**, `Keys` **69.2%**, `Families` **72.7%** (see GAP-8) |

### Microkernel verdict for this section

`event/` names neither extension (0 grep hits). `eventtest` does not import `eventmemory`
(0 deps). The extension point is complete on all four counts `microkernel.md` requires: a
contract (`Factory`, `Tx`), a registration mechanism (the store's own package calls `Run`),
a failure policy (`admit` fatals on a claimed capability with a missing hook; `needs` reports
*not certified*; a store's panic is not recovered), and a compatibility note (`event/store.go`
carries the store-seam contract). Phase 2's `eventpg` needs **zero diffs to `event/`**.
It needs **zero diffs to `event/eventtest/` too — except for GAP-2 and GAP-3**, both of which
are properties of a store the sample does not contain (a persistent backing and a log longer
than the sample's).

---

## Round 1 — dispositions, 2026-09-07

Thirteen of the sixteen closed in the code, three moved to the plan's `## Debt`.
Nothing rejected — every finding here was reproducible, and two of them
(GAP-1's packed-buffer store, GAP-3's over-long log) were reproduced as fixtures
that now ship with the suite.

| Finding | Disposition |
|---|---|
| GAP-1 `[high]` | closed — `probe.spilled`, plus the thirteenth defect `packedPages` |
| GAP-2 `[high]` | closed — `Factory.New`'s and `Factory.Begin`'s docs rewritten, in code and in the plan |
| GAP-3 `[high]` | closed — no bare `1024`; the walk starts at a cursor the section mints and ends on the contract; identity 4 → 16 bytes; a 6 000-event foreign-log fixture |
| GAP-4 `[medium]` | closed — the read door reads through the value the append door writes through |
| GAP-5 `[medium]` | closed — `probe.begin` registers the rollback; the fixture reports an unresolved transaction |
| GAP-6 `[medium]` | closed — `contended` reordered to what its comment claims; every section on a 20-second window |
| GAP-7 `[medium]` | closed — the zero verdict is `unreported`, and `Run` reports it |
| GAP-8 `[medium]` | closed — the three proxies answer errors; all nine reporting branches driven |
| GAP-9 `[medium]` | closed — a `failureCase` value and two named doors; the substring assertion replaced by an equality |
| GAP-10 `[high]` | **deferred** — recorded in the plan's Breaches table as unjustified and in `## Debt` with the split named |
| GAP-11 `[low]` | closed — `probe.refuses` deleted |
| GAP-12 `[low]` | closed — thirteen and six; C7's sentence rewritten |
| GAP-13 `[low]` | closed — S5's third file-level change names `event/store.go` |
| GAP-14 `[low]` | closed — the `§UC-034` citation dropped from the shipped text |
| GAP-15 `[low]` | **deferred** — to `## Debt`, with the finding's own argument |
| GAP-16 `[low]` | **deferred** — to `## Debt`, all three parts |

Every behavioural closure was verified by putting the defect back and watching
the new assertion turn red; each mutation is quoted in the finding's disposition
and in the plan's § S5 mutation table, and each was restored with the tree
rebuilt clean afterwards.

**Checkpoint after the closures**

```
$ go build ./... && go vet ./event/... && test -z "$(gofmt -l .)" && go test -race -count=1 ./event/...
ok  	github.com/frostgrove/vv/event	1.286s
ok  	github.com/frostgrove/vv/event/eventmemory	1.038s
ok  	github.com/frostgrove/vv/event/eventtest	1.409s

$ go test -race -count=1 ./event/...      # again, no flake
ok  	github.com/frostgrove/vv/event	1.272s
ok  	github.com/frostgrove/vv/event/eventmemory	1.041s
ok  	github.com/frostgrove/vv/event/eventtest	1.399s

$ go test -race -count=1 -v -run 'TestTheMemoryStoreSatisfiesTheContract' ./event/eventmemory/
20 sections reported, 18 passed, 2 not certified (durability; store failure
classification), none skipped — unchanged from round 1's observation

$ make check
check-deps: ok   check-tiers: ok   check-utils: ok   check-triplets: ok
check-todo: ok   check-replaces: ok   check-tidy: ok   check-otel-schema: ok
check-workspace: ok
```

---

### GAP-1 [high][immediate] `payload ownership` certifies a store that violates the capacity clause it was written for

- **Where:** `event/eventtest/sections_ownership.go:38-46`; the obligation is
  `event/store.go:96-99` ("Yours to give away, **including its capacity**"); the plan claims
  it delivered at `.agents/artifacts/plans/EVENTSOURCE_P1_PLAN.md` § C7 — *"`payload ownership`
  appends **one byte** to page one's **first** payload and asserts the **second** envelope's
  payload is unchanged; and the same for the `[]Envelope`"*.
- **What:** the code does not do that. It walks every envelope of the page, overwrites all of
  that envelope's bytes with `0xff` **and then** appends one byte — so the byte an `append`
  spills into the next row is immediately overwritten by the next iteration's own `0xff` fill,
  and the only assertion afterwards (`samePayloads(again, written)`) is against a **fresh read
  of the store**, never against a sibling envelope of the same page. The `[]Envelope` half of
  C7 is likewise absent: lines 52-64 assert that a store does not **reuse** a buffer across two
  reads, which is a different property. `grep -rn "cap(" event/eventtest event/eventmemory`
  returns nothing outside `defects.go`'s own pool bookkeeping; the only `cap == len` assertions
  in the tree (`event/fold_test.go:412`, `event/fuzz_test.go:154`) are about `Fact.New`'s
  freeze, one level out, and say nothing about what a store returns.
- **Why this severity:** demonstrated, not inferred. I built the missing fixture — a decorator
  that packs each page into **one freshly allocated buffer** and hands out `buf[at:at+n]` with
  no third index, so every payload's spare capacity runs into the next row's bytes, and which
  pools nothing across reads — ran the whole suite against it, and got
  **`19 of 20 sections passed, 0 failed`** (`durability` was the one *not certified*, for the
  unrelated capability reason). A phase-2 `eventpg` that scans a page into one `[]byte` and
  sub-slices it — the ordinary shape for a driver that gives you a row buffer — is certified
  green today. The consumer symptom is silent: a projector that does
  `batch = append(batch, envelope.Payload...)`, or any `append` to a payload it was handed,
  corrupts the **next event's** payload in the same page, and the corruption surfaces as a
  decode failure or a wrong folded state attributed to the wrong event.
- **Why this timing:** it is a missing test for a stated invariant on a **frozen contract**
  (`event/store.go`'s ownership clause), and it is the artefact phase 2 runs verbatim to decide
  whether `eventpg` is correct. Certifying `eventpg` against a suite that cannot see this makes
  the clause unenforceable for the first real store, and adding the case after `eventpg` ships
  means a store that was called conformant is not.
- **Close criteria:**
  - [ ] `payload ownership` appends one byte to page[0].Payload and asserts page[1].Payload is
        byte-identical to the clone taken before it, **within the same page value**
  - [ ] the same for the `[]Envelope`: one `append` to the returned slice, then assert the next
        read's page is unaffected and the retained page is unchanged
  - [ ] a thirteenth row in `defects()` — a decorator that sub-slices one **per-read** buffer
        without cutting capacity, and reuses no buffer across reads — and
        `TestTheSuiteStillDetectsEveryDefectItWasBuiltToDetect` reports its section *failed*
        with the un-decorated store *passed*
  - [ ] `eventmemory` still passes the enlarged section (its `handOut` already cuts capacity)
- **Status: closed** (round 1 disposition). `payload ownership` now calls
  `probe.spilled` (`event/eventtest/sections_ownership.go`), which reads a page,
  clones it, appends one byte to `page[0].Payload` and asserts `page[1:]` is
  byte-identical to `kept[1:]` **within that page value**; then reads a second
  page, appends one envelope to the returned `[]Envelope`, and asserts both that
  the page it kept is unchanged and that the store's next read answers the bytes
  it answered before. The write-every-byte loop no longer appends, so the fill
  cannot mask the spill. The thirteenth defect is
  `packedPages` (`event/eventtest/defects.go`) — a decorator that packs each page
  into one freshly allocated buffer and hands out `buffer[at:]` with no third
  index, pooling nothing across reads, which is the fixture the reviewer built.
  **Mutation:** with the `spilled` call removed,
  `TestTheSuiteStillDetectsEveryDefectItWasBuiltToDetect` reports *"the suite no
  longer detects a store that hands out sub-slices of one fresh page buffer
  without cutting their capacity: its payload ownership section was reported
  `passed`"*; restored, it is green, and `eventmemory` and the staging fixture both
  pass the enlarged section. C7 and C8 in the plan are rewritten to match.

### GAP-2 [high][immediate] `Factory.New`'s documented contract contradicts the code, and a store author who believes it breaks three sections

- **Where:** `event/eventtest/suite.go:29-31` — *"Called once per section, so one section's
  history is never another's."* Call sites: `suite.go:208` (`open`), `sections_read.go:204`
  (`foreignCursor`), `sections_transactions.go:196` (`chained`, twice),
  `sections_lifecycle.go:245` (`durability`), `sections_lifecycle.go:282` (`classified`),
  plus `suite.go:101` (`admit`).
- **What:** `New` is not called once per section. `store failure classification` calls it
  **16 times** (`classified` runs 8 outcome rows × 2 `through` values, each opening a fresh
  store); `transactions` calls it **3 times**; `resumption` and `durability` call it **twice**,
  and the second call happens **mid-section, after the data the section is asserting about has
  been written**. The doc sentence licenses exactly the implementation that breaks this — a
  `New` that resets or truncates the backing so one section's history is never another's — and
  the doc never says whether two `New` calls may share a backing, which is the question
  `chained`, `foreignCursor` and `durability` all branch on.
- **Why this severity:** concrete input, wrong output. A `eventpg` factory that reads
  "called once per section" and implements `New` as *truncate the events table, return a store
  over the same pool*: `resumptionSection` writes 4 events, persists a cursor, then
  `foreignCursor` calls `New` at `sections_read.go:204` — the table is emptied, the backings
  compare `Equal` so the foreign-cursor half reports *unable*, and the `walkLog` at
  `sections_read.go:186` then returns **0 events** against a `seen` that holds 4, so
  `sameEvents` fails and the section is reported **failed** for a store that is correct.
  `durabilitySection` gets the mirror image: `this.store()` at line 245 wipes the row it just
  wrote, `Backing().Equal` is true, and the section refuses with *"a store value built after
  the one that wrote folds the stream to 0"*. Neither failure names the factory.
- **Why this timing:** this is the public contract of the extension point phase 2 is written
  against, and it is the first thing a store author reads. A wrong sentence here costs the
  `eventpg` author a debugging session against a suite that reports the store's fault for the
  factory's, and the fix is a contract change once other stores exist.
- **Close criteria:**
  - [ ] `Factory.New`'s doc states what is actually true: called **one or more times per
        section**, that the suite may hold several of its stores live at once, and that a store
        it builds must **not** destroy or reset what an earlier one wrote in the same section
  - [ ] the doc states the one property the sections branch on — whether two `New` values share
        a backing is the factory's choice, and `Backing().Equal` is how the suite finds out
  - [ ] a section that calls `New` mid-run states why it may (`foreignCursor`, `chained`,
        `durability`), or takes the second store from a hook of its own
- **Status: closed** (round 1 disposition). `Factory.New`'s doc
  (`event/eventtest/suite.go`) now states that it is called **one or more times
  per section**, that the suite holds several of its stores live at once, that a
  store it builds must not destroy or reset what an earlier one wrote in the same
  section, and that whether two of its stores share a backing is the factory's own
  choice — asked through `Backing().Equal`, with a section that needs the other
  kind reported *not certified*. `Factory.Begin`'s doc states the disposal
  obligation GAP-5 added. `foreignCursor` and `durabilitySection` each say in their
  own comment why they ask the factory for a store mid-section; `chained` already
  did. The plan's § Contracts block carries the same two paragraphs, so the
  contract and its statement changed together.

### GAP-3 [high][immediate] An uncalibrated `1024`-page cap over a whole-log walk makes the suite wrong on any store whose log is longer than the sample's

- **Where:** `event/eventtest/suite.go:255` and `:263` (`drain`), `:278` and `:290`
  (`walkLog`); the run identity that scopes it is `suite.go:135-142` (`runIdentity`, **4 bytes**
  from `crypto/rand`) and `suite.go:298-301` (`owns`).
- **What:** `walkLog` pages through the **entire global log of the store**, filtering to this
  run's own streams afterwards, and gives up with a refusal after a hardcoded `1024` pages.
  The number has no derivation, no config entry, no calibration note and no relationship to any
  published limit; it is the classic magic threshold `universality.md` names. `walkLog` is
  called five times per run (`globalOrderSection` twice, `conservationSection`,
  `globalPagingSection`, `resumptionSection`), and `from()` walks the tail again — so the suite
  performs O(entire log) scans per section against a store whose log it does not own.
- **Why this severity:** wrong verdict on unseen input. A store whose log holds more than
  `1024 × Limits().MaxRead` events — any Postgres database that has been used for anything,
  including a previous run of this suite, since nothing truncates between runs — reports
  **`failed`** with *"a walk over this store did not reach the end of the log in 1024 pages"*,
  which names the suite's own budget as if it were the store's defect. With `MaxRead` at the
  in-tree fixture value of 5, that ceiling is 5 120 events; one run of this suite writes on the
  order of 200. Second input: the 32-bit run identity gives a birthday collision at roughly
  77 000 runs against one long-lived backing, and a collision is not a crash — `owns` then
  counts a previous run's events as this run's, so `conservation` refuses with
  *"this run wrote N events, the global read returned M"* and `global order` refuses on a
  position it never wrote. Both are false accusations against a correct store.
- **Why this timing:** it is a hardcode / fit-to-the-sample finding, which `gaps.md` says is
  never deferred; and it is the first thing phase 2 hits, because `eventmemory` is the only
  store whose log is empty at the start of every section and is therefore the only store the
  constant was ever exercised against.
- **Close criteria:**
  - [ ] the walk's bound is derived from what the run itself wrote plus the store's published
        `MaxRead` (e.g. pages until no envelope of this run's families has been seen for a
        stated number of consecutive pages), or the suite reads only from a cursor it minted at
        the start of the section rather than from `""`
  - [ ] no bare `1024` remains in `event/eventtest/`; whatever bound survives carries a
        one-line derivation beside it
  - [ ] the run identity's width is stated with the collision argument, or the suite scopes a
        run by a value the factory supplies rather than by randomness
  - [ ] a fixture whose log holds an unrelated prefix longer than the bound is added to
        `defects()`' sibling inventory or to `suite_test.go`, and the suite still reports the
        real verdicts against it
- **Status: closed** (round 1 disposition). No bare `1024` remains
  (`grep -c 1024 event/eventtest/` = 0). `drain` ends on a page shorter than the
  one the store publishes and refuses one longer than it — a full page moves the
  version it continues from, so the walk takes as many pages as the stream is
  long. `walkLog` ends on an empty page and refuses a page that comes back beside
  the cursor it was read from, which is the one answer that would not end a walk.
  Every walking section mints its start cursor with `probe.tail` **before** it
  writes anything, and `global paging`, `resumption` and `monotone visibility`
  read through `event.Read(store, from)` rather than from `""`, so what a section
  walks is its own tail. The run identity is **16 bytes**, with the collision
  argument beside it. The fixture the criterion asks for is
  `prefilledFactory(6000)` and the test is
  `TestASectionWalksItsOwnTailOfALogSomebodyElseFilled`, which asserts no section
  is *failed* and that the certification count matches the same store with an
  empty log. **Mutation:** restoring the bounded from-`""` walk reports
  `global order`, `conservation`, `global paging` and `resumption` **failed** with
  *"a walk over this store did not reach the end of the log in 1024 pages"* — the
  reviewer's predicted false accusation, reproduced and then removed.

### GAP-4 [medium][immediate] Two of the sixteen `store failure classification` cases assert nothing new — the read door never goes through the decorator

- **Where:** `event/eventtest/sections_lifecycle.go:281-311` (`classified`) and `:313-324`
  (`injected`). `classified` builds `repo = this.bind(wrapping{over{store}}, held)` when
  `through != "the store itself"`, then calls `this.injected(ctx, store, repo, …, door)`;
  `injected` uses `repo` only on the `append` branch and does `event.Read(store, "")` —
  the **raw, undecorated** store — for the read branch.
- **What:** for the two `read` rows (`BadCursor → ErrCursor`, `Unclassified → ErrBackend`) the
  "through a decorator that wraps the store's own error" iteration executes exactly the same
  code as "the store itself". The `wrapping` value is constructed and discarded. The plan's
  §S5 and the `Fail` contract both claim the read door is exercised through a wrapping
  decorator; it is not.
- **Why this severity:** the property is not currently lost, because `refuseAppend` and
  `refuseRead` share one classifier (`event/errors.go:185-187` → `refuse(err, door)`), so the
  append door proves *"the classification is found through the wrapping and never by a type
  assertion"* for both. But a conformance suite's whole value is that a *passed* line means the
  named thing was asked, and this one was not: the moment the door split into two classifiers —
  the exact refactor `event/errors.go:196-208` invites, since the door already decides one
  thing — these two rows would go on reporting *passed* over a read path nobody wrapped.
- **Why this timing:** it is two characters of change now (`injected` takes the log to read
  through) and a silent hole in an artefact phase 2 treats as the definition of correct later.
- **Close criteria:**
  - [ ] `injected` reads through the same value the append door writes through, so the
        `wrapping` iteration exercises `wrapping.ReadAll`
  - [ ] a mutation check recorded in the report: make `wrapping.ReadAll` drop the
        classification and confirm the two read rows turn *failed*
- **Status: closed** (round 1 disposition). `classified` builds the store the case
  runs through once (`one.build(store)`), binds the repository to it **and** hands
  it to the door as the `log` field of `failing`, so `injectedAtRead` calls
  `event.Read(on.log, "")` and the `wrapping` iteration exercises
  `wrapping.ReadAll`. **Mutation:** making `wrapping.ReadAll` return its own error
  instead of the store's classification turns the read row red —
  *"[outcome bad cursor] injected at the read door through a decorator that wraps
  the store's own error answered event: the store failed where the kernel maps
  that classification to event: this cursor was not minted over this backing"* —
  which it could not have done before, because the raw store was being read.

### GAP-5 [medium][immediate] `lifecycle` abandons a transaction it never finishes, and `Factory` declares no disposal obligation

- **Where:** `event/eventtest/sections_lifecycle.go:121` —
  `inside, _ := this.begin(ctx, store)` — the `Tx` is discarded; `stage` returns and the
  transaction is never committed or rolled back, on any path. `Factory.New`'s doc
  (`suite.go:29-31`) promises cleanup for what *the store* holds open, and `Factory.Begin`'s
  doc (`suite.go:33-35`) says nothing about who finishes a transaction the suite leaves live.
- **What:** the section deliberately leaves a transaction unresolved across `Close()` — which
  is the right assertion, because *close neither commits nor rolls back* — and then never
  disposes of it. `refuse` panics through `abort`, so several other paths abandon transactions
  too (`contended`, `crossed`, `chained` all finish theirs only on the success path).
- **Why this severity:** `eventmemory` garbage-collects it, so the sample shows nothing. A
  `eventpg` transaction holds a pooled connection and every row lock it took until the pool is
  closed at the end of the **test binary**, not the section. The next section's `New` draws
  from the same pool; with a default pool of 4 and 20 sections, a handful of abandoned
  transactions exhausts it, and the symptom is that a later section blocks forever rather than
  reporting a verdict — the suite hangs and `go test` panics on its 10-minute timeout with a
  stack trace instead of a section name.
- **Why this timing:** it is a contract obligation on `Factory` (who disposes of an abandoned
  `Tx`) that phase 2 must be able to satisfy, and adding the obligation later changes the
  extension point after a store was written against it.
- **Close criteria:**
  - [ ] the suite rolls back every transaction it began, including the deliberately abandoned
        one, after the assertion that needed it abandoned has been made — or `Factory.Begin`'s
        doc states that the factory must dispose of every `Tx` it returns through `t.Cleanup`,
        and the in-tree fixtures do
  - [ ] `probe.begin` registers the disposal so a `refuse` panic on any later line still
        releases it
- **Status: closed** (round 1 disposition). `probe.begin` registers
  `t.Cleanup(func() { _ = tx.Rollback(context.Background()) })` at the moment it
  begins, so the transaction `lifecycle` abandons on purpose and every transaction
  a `refuse` panic walks past is released when that **section** ends rather than
  when the binary does. `Factory.Begin`'s doc states it. The staging fixture's
  `New` cleanup now reports any transaction still unresolved at the end of a
  section. **Mutation:** with that one `t.Cleanup` line removed,
  `TestATransactionCapableStoreSatisfiesTheContract/lifecycle` reports *"1
  transaction(s) this section began were still unresolved when it ended"*.

### GAP-6 [medium][immediate] `contended`'s stated safety property is not the one the code has, and no operation in the suite carries a deadline

- **Where:** `event/eventtest/sections_transactions.go:89-112`. The comment claims *"the case
  is built so that nothing here can be waited for: the winner is finished before the loser
  issues anything"*. The code issues `this.load(losing, repo, id)` at line 100 — inside the
  losing transaction — **before** the winner's `append` at line 102.
- **What:** the loser has issued a statement inside its own transaction before the winner
  writes. Every context in every section is `context.Background()`
  (`grep -c "context.Background()" event/eventtest/*.go` = one per section); nothing anywhere
  carries a deadline.
- **Why this severity:** `event/store.go:174-178` explicitly permits a store to **wait** for a
  competing transaction. A store whose `ReadStream` inside a transaction takes a share lock on
  the stream row — a reasonable way to make its own optimistic check safe — blocks the winner's
  `Append` on the loser's lock, and the loser is never finished, so the two deadlock. Because
  no context has a deadline, the outcome is not a *failed* verdict naming the store; it is a
  hung `go test`. The comment is what a future maintainer will trust when adding a case here.
- **Why this timing:** the comment is load-bearing for anyone extending the section, and the
  missing deadline is a suite-wide policy question that is cheap now and a contract change once
  `eventpg` runs it.
- **Close criteria:**
  - [ ] either the loser issues nothing until the winner has committed (which is what the
        comment claims), or the comment states the real property and the case is documented as
        requiring a store that does not lock on read
  - [ ] every store call the suite makes runs under a context with a stated, generous deadline,
        so a store that blocks is reported *failed* with the section's name rather than hanging
        the binary
- **Status: closed** (round 1 disposition). `contended` is reordered: both
  transactions are begun before either issues anything, the losing decision is
  loaded **outside** both of them, and the loser's first statement is its `Append`
  after the winner has committed — which is what the comment claimed and now also
  what the code does; the comment states it in those terms. Every section runs
  under `probe.context()`, a 20-second window whose derivation (go test's own
  ten-minute panic, twenty sections) is beside the constant, and every store call
  a section makes inherits it. The control is
  `TestEveryStoreCallASectionMakesCarriesADeadline`, which watches the four
  context-taking doors through a decorator: **548 store calls, 0 without a
  deadline**. **Mutation:** one section put back on `context.Background()` reports
  *"2 of 548 store calls a run made carried no deadline"*.

### GAP-7 [medium][immediate] The zero value of a verdict is *passed*, so a section that never ran is reported as one that did

- **Where:** `event/eventtest/report.go:9-13` — `passed word = iota` — and
  `event/eventtest/suite.go:64-78`: `given := verdict{section: held.name}` is initialised
  **before** `t.Run` and appended **after** it, and `given = certify(...)` inside the closure is
  the only thing that ever overwrites it.
- **What:** any factory hook that calls `t.Fatal`/`t.Fatalf` — `fixtures_test.go:565, 584,
  592, 599`, `eventmemory/conformance_test.go:23, 52` all do — unwinds the subtest goroutine
  through `runtime.Goexit`. `probe.walk`'s deferred `recover()` sees `nil`, `certify` never
  returns, and `sweep` appends the untouched zero value: `{section, passed, ""}`. `tell` is not
  called either, so there is no log line and no `t.Error` from the suite.
- **Why this severity:** `Run` is still red, because the subtest's own failure propagates — so
  this is not a silent green run today. But `Run`'s anti-vacuity rule 2 counts that phantom row
  as certified (`suite.go:55`, `report.go:40-48`), and every one of the suite's own four
  `Certify`-based tests reads it as a genuine *passed*: `TestEverySectionInTheInventoryWasReported`
  counts it as reported, `TestARunThatCertifiedNothingFails` counts it toward a non-zero
  certification, `TestTheSuiteStillDetectsEveryDefectItWasBuiltToDetect` would read a fataling
  fixture as "the defect is no longer detected... reported passed". The safe default for a
  verdict that was never computed is not the only word that means the store is correct.
- **Why this timing:** one line, and it is the default every future section and every future
  `Certify` caller inherits.
- **Close criteria:**
  - [ ] the zero value of `word` is not `passed` — either `failed`, or a fourth `unreported`
        value that `Run` treats as a failure and the three-word renderer never emits
  - [ ] a test drives a factory hook that fatals and asserts the section is not reported
        *passed*
- **Status: closed** (round 1 disposition). `word`'s zero value is `unreported`
  (`event/eventtest/report.go`); `String()` renders it *"not reported"*, which the
  three-word renderer never reaches on a run that finished; `Run` reports every
  `unreported` row with `t.Error`; and `certified` counts only `passed`, so a
  phantom row no longer satisfies anti-vacuity rule 2. The test is
  `TestASectionThatNeverReturnedIsNotReportedPassed`, which drives a `Begin` hook
  that leaves the subtest through `runtime.Goexit` — `t.SkipNow`, the one of the
  two doors out that a watching test can observe without the failure propagating
  to itself, and the same unwinding as `t.Fatal`. **Mutation:** putting
  `passed word = iota` back turns it red with *"a section whose factory left the
  run through the goroutine that section was dispatched on was reported
  passed"*.

### GAP-8 [medium][immediate] The three exported proxies have no control: their detection branches are never executed

- **Where:** `event/eventtest/proxies.go:9-64`; the tests are
  `event/eventtest/proxies_test.go:22-77`. Measured coverage from
  `go test -coverprofile`: `RoundTrip` **60.0%**, `Keys` **69.2%**, `Families` **72.7%**.
- **What:** `TestTheProxiesCompareSomethingThatCanDiffer` asserts that a **collision is
  constructible** — that a concatenating mapper renders one key for two identities, and that
  `Compose` renders it differently — but it never runs `eventtest.Keys` over the colliding pair,
  never runs `eventtest.Families` over two declarations naming one family, and never runs
  `eventtest.RoundTrip` over a fact that fails. The `if held, taken := rendered[key]; taken`
  branch at `proxies.go:37-39`, the `if at, taken := held[family]; taken` branch at `:59-61` and
  the `if _, err := fact.RoundTrip(...)` error arm at `:14-16` are dead in the test run.
- **Why this severity:** the section's own fifth rule is *"every section carries a control that
  a refuse-everything store fails"*, and `TestEverySectionFailsAgainstAStoreThatRefusesEverything`
  computes it for all twenty sections. The three proxies — the part of the suite an
  **application** runs over **its own** declaration, and the only mechanism for the two
  obligations `event` cannot check itself — get no such treatment. Delete the collision `if`
  from `Keys` and every test in the repository stays green; an application would then be told
  its identity mapper is injective when two of its identities share a history and fold each
  other's facts, which is the failure `proxies.go:20-22` says the proxy exists to prevent.
- **Why this timing:** these are three of the six exported symbols of the package; a control
  added later is a control that never guarded the version applications first used.
- **Close criteria:**
  - [ ] each of `Keys`, `Families` and `RoundTrip` is driven through its failing branch against
        a recording `testing.TB`-shaped seam (or an internal function the exported wrapper calls,
        exposed through `export_test.go`), and asserted to report
  - [ ] `go tool cover -func` shows 100% for `proxies.go`, or the residue is named
- **Status: closed** (round 1 disposition). Each proxy is split in two: an
  unexported function that **answers an error** (`roundTrips`, `keysRender`,
  `familiesDiffer`) and the exported wrapper that turns an answer into a
  `t.Fatal`. `export_test.go` re-exports the three answering functions, and
  `TestEachProxyReportsTheThingItExistsToFind` drives all **nine** reporting
  branches — the colliding key pair, an identity that renders no key, one
  identity, no aggregate, a fact whose sample does not survive its codec, no fact,
  two declarations naming one family, a nil declaration, no declarations — with
  three silent controls beside them. `go tool cover -func`: `roundTrips`,
  `keysRender` and `familiesDiffer` **100.0 %**; the three wrappers sit at 66.7 %,
  and the residue is named — each one's uncovered statement is its `t.Fatal`,
  which no test that stays green can reach.

### GAP-9 [medium][immediate] Stringly-typed flag parameters and a substring assertion on a message defined 27 lines away

- **Where:** `event/eventtest/sections_lifecycle.go:283`
  (`if through != "the store itself"`), `:314` (`if door == "append"`), and the six call-site
  literals that feed them at `:264-273`; `event/eventtest/sections_write.go` has the same shape
  in `dishonestStores`' prose `what` but only for rendering. Separately
  `event/eventtest/sections_lifecycle.go:189` — `strings.Contains(err.Error(), "quota")` —
  against the message minted at `:162`.
- **What:** `door` and `through` are flag parameters whose values are display prose;
  `readability.md` §5 forbids a boolean/flag parameter that switches what a function means, and
  these switch which door is opened and which store is bound. The `"quota"` literal is a
  four-character proxy for the whole of
  `"eventtest: over the quota this decorator polices"`.
- **Why this severity:** the two flags are how GAP-4 became invisible — `injected` reads its
  own branch condition out of a sentence written for a human. The `"quota"` substring is a
  weakened assertion that fails **open**: reword the error at `:162` to drop the word "quota"
  and line 189 stops asserting that the kernel suppresses a store's text, silently, with the
  section still reporting *passed*.
- **Why this timing:** both are inside the two sections that carry C2's and C3's proofs, and
  every future case in those sections copies the shape.
- **Close criteria:**
  - [ ] `door` is a small typed value (or two named functions, `injectedAtAppend` /
        `injectedAtRead`) and `through` is a decorator-builder rather than a sentence
  - [ ] line 189 asserts against `quota.Error()` rather than a substring chosen by hand
- **Status: closed** (round 1 disposition). `door` and `through` are gone as flag
  parameters: a case is a `failureCase` value carrying `ask func(*probe,
  context.Context, failing) error` — `injectedAtAppend` or `injectedAtRead` — and
  `build func(event.Store) event.Store`, the decorator-builder. `classified` takes
  two parameters where it took six, and `injected`'s seven are gone entirely. The
  `strings.Contains(err.Error(), "quota")` assertion is replaced by
  `err.Error() != policy.is.Error()`, applied to **all five** rows rather than the
  three that carried an error, which is strictly stronger and cannot fail open:
  the refusal must render its own sentinel and nothing else.

### GAP-10 [high][deferred] `probe` is a shared mutable blob with 8 fields and 49 methods spread across 7 files, and the plan's Breaches table does not justify it

- **Where:** `event/eventtest/suite.go:148-157` (the type) and its methods in `suite.go` (18),
  `sections_transactions.go` (10), `sections_read.go` (8), `sections_lifecycle.go` (7),
  `sections_write.go` (3), `sections_ownership.go` (2), `report.go` (1) — **49**. The plan's
  § Architecture metrics → Breaches table justifies `Store`'s 8 public methods and `event`'s
  ~82 public symbols and says nothing about `probe`.
- **What:** measured breaches of `architecture.md`'s thresholds, all unjustified in writing:
  **fields per class 8 > 7**; **lines per class** — the type plus its methods is well over
  1 000 against a threshold of 200; **function parameters** 7, 7, 6, 5 against a threshold of 4
  (`wroteWhatItSaid`, `injected`, `classified`, `from`); **functions over 50 lines** 62, 58, 57,
  55. `probe` also matches two of the distributed-god-object signals verbatim: a mutable
  `state` blob (`unmet`, `broke`) written from seven files, and functions that take the blob
  while using two of its eight fields.
- **Why this severity:** `architecture.md` states it plainly — *"An unjustified breach is a gap
  of severity high."* The practical consequence is the relocation test: `probe` cannot be moved
  to its own package without exporting 49 methods, and the section bodies cannot be read or
  reasoned about without it, so the twenty sections are not twenty objects but one object with
  twenty entry points. `wroteWhatItSaid(ctx, store, repo, held, at, id, stream)` takes seven
  parameters five of which `open()` produced two lines earlier — the signal that the value
  object is missing.
- **Why this timing:** **deferred**, and deliberately: it changes no public contract (all 49
  methods are unexported), it does not block S6, and phase 2 adds no sections. It is here so it
  is not dropped — the cost lands the first time someone adds a twenty-first section.
- **Close criteria:**
  - [ ] either the breach is justified in the plan's Breaches table with the argument for why a
        section runner is not a class in `architecture.md`'s sense, or
  - [ ] the fixture-building half (`store`, `declared`, `bind`, `open`, `account`, `streamOf`,
        `keyAtCap`) and the store-operation façade (`load`, `append`, `readStream`, `drain`,
        `readAll`, `walkLog`) are separated from the verdict sink (`refuse`, `unable`,
        `verdict`, `walk`), each with a name and its own sentence
  - [ ] no function in `event/eventtest/` takes more than four parameters
- **Status: deferred, moved to the plan's `## Debt`** (round 1 disposition). The
  breach is recorded in the plan's § Architecture metrics → Breaches table as
  **not justified, carried as debt**, with the split named — fixture builder,
  store façade, verdict sink — and the trigger stated (the twenty-first section).
  Round 1 narrowed the parameter half as a side effect of closing GAP-4 and GAP-9:
  `classified` six → two, `injected` seven → gone, leaving **one** function over
  four parameters (`wroteWhatItSaid`, 7) where there were four.

### GAP-11 [low][immediate] Dead code on the assertion seam

- **Where:** `event/eventtest/suite.go:303-307` — `func (this *probe) refuses(err, sentinel,
  doing)`. `grep -rn "\.refuses(" event/eventtest/` returns nothing.
- **What:** an unused method on the type every section is written against, sitting one line
  away from the `partitioned` helper that does the job it was meant for.
- **Why this severity:** cosmetic, but `restrictions.md` §1 forbids public-or-not API written
  for later, and a dead assertion helper is the one thing a section author is most likely to
  reach for and then wonder why nothing else uses.
- **Why this timing:** deleting it is free; leaving it means the next section uses it and there
  are then two spellings of "assert this refusal is that sentinel".
- **Close criteria:**
  - [ ] `probe.refuses` is deleted, or it is used where `partitioned`'s first check is written
        by hand
- **Status: closed** (round 1 disposition). `probe.refuses` is deleted, and
  `errors` left `suite.go`'s import list with it.

### GAP-12 [low][immediate] Plan/code drift in the S5 file counts and the C7 test description

- **Where:** `.agents/artifacts/plans/EVENTSOURCE_P1_PLAN.md` § S5 — *"Eleven files of suite
  and five of test"*; and § C7's *Test:* sentence.
- **What:** there are **13** non-test files in `event/eventtest/` (`declaration.go`,
  `defects.go`, `doc.go`, `inventory.go`, `proxies.go`, `report.go`, `sections_lifecycle.go`,
  `sections_ownership.go`, `sections_read.go`, `sections_transactions.go`, `sections_write.go`,
  `stores.go`, `suite.go`) and **6** test files (`export_test.go`, `inventory_test.go`,
  `fixtures_test.go`, `suite_test.go`, `defects_test.go`, `proxies_test.go`). The S5 "Files"
  list itself names all 13 and all 6, so only the prose sentence is wrong. C7's test sentence
  describes an assertion the code does not contain (GAP-1).
- **Why this severity:** the plan is what the next agent reads as the record of what shipped;
  a count that disagrees with the tree is the same class of defect as a stale doc.
- **Why this timing:** it is the artefact S6 will consolidate into `docs/`.
- **Close criteria:**
  - [ ] the sentence reads thirteen and six, or is removed
  - [ ] C7's *Test:* sentence matches what the section asserts once GAP-1 is closed
- **Status: closed** (round 1 disposition). The S5 prose reads *"Thirteen files of
  suite and six of test"*, and C7's *Test:* sentence describes `probe.spilled` —
  what the code now asserts — with a correction note saying what the first version
  did instead. The defect count is **thirteen** in C8, in S5's prose, and in
  `defects.go`'s own comment.

### GAP-13 [low][immediate] A kernel file outside S5's declared file list was edited during S5, unrecorded

- **Where:** `event/store.go:147-155` (`git diff event/store.go`: 6 insertions, 4 deletions,
  comment-only) and `event/repo.go`, both with mtime 17:22 — after every S4-era file
  (16:06–16:41) and inside the S5 window (16:58–17:29). S5's declared file list is
  `event/eventtest/*` plus `event/eventmemory/conformance_test.go`.
- **What:** the `Store.Transaction` contract sentence changed from *"A closed store answers the
  second"* to *"A closed store answers exactly what an open one would and never reports closure
  here"* — the clause `lifecycleSection.closedWithin` (`sections_lifecycle.go:127-134`) asserts.
  It is an alignment with C10 row 5, which had already decided it, so nothing is wrong with the
  content. The plan's S5 entry records two file-level deviations and neither is this one.
- **Why this severity:** the content is right and the change is a comment. `restrictions.md`
  §1 nonetheless requires an out-of-list edit to be a recorded plan change, and this one alters
  a **port contract sentence** that phase 2 implements against.
- **Why this timing:** the record has to exist before S6 freezes the contract into `docs/`.
- **Close criteria:**
  - [ ] S5's "file-level changes" note names `event/store.go` (and `event/repo.go` if it
        changed), with the one-line reason and the C10.5 citation
- **Status: closed** (round 1 disposition). S5's file-level changes note is now
  three rather than two, and the third names `event/store.go`: the
  `Store.Transaction` sentence, why it changed, that it is a comment and nothing
  else, and the C10 row 5 citation. `event/repo.go` has an mtime in the window and
  `git diff` shows no change; the note says so.

### GAP-14 [low][immediate] An internal spec reference is rendered into a shipped package's failure text

- **Where:** `event/eventtest/sections_lifecycle.go:37` — *"…and a caller that reads one issues
  the single retry **§UC-034** describes over a decision nothing ever wrote"*.
- **What:** `§UC-034` names `.agents/artifacts/usecases/EVENTSOURCE_P1_USECASES.md`, which is a
  process artifact and is not published. This string is in a non-test file of an exported
  package; the party who reads it is a store author outside this repository.
- **Why this severity:** the message is otherwise excellent and self-contained without the
  citation; the citation is the only part of it the reader cannot act on.
- **Why this timing:** S6 writes the docs, and this is the moment to decide whether refusal
  texts cite `[[UC-nnn]]` (the tree's public numbering) or nothing.
- **Close criteria:**
  - [ ] the reference is dropped or replaced with the published decision/use-case id S6 assigns
  - [ ] `grep -rn "§UC-\|§INV-\|§D\." event/ --include=*.go | grep -v _test.go` is empty
- **Status: closed** (round 1 disposition). The `§UC-034` citation is gone from
  `sections_lifecycle.go`; the sentence now says what the caller does — *"a caller
  that reads one re-reads the stream and retries over a decision nothing ever
  wrote"* — which is what the reader can act on.
  `grep -rn "§UC-\|§INV-\|§D\." event/ --include=*.go | grep -v _test.go` is
  empty.

### GAP-15 [low][deferred] The uncertainty half of `cancellation` is driven with one context sentinel, so half of C3's assertion cannot fire

- **Where:** `event/eventtest/stores.go:77-82` (`unconfirming` returns
  `event.Failure(event.Unconfirmed, context.Canceled)` — always `Canceled`) and the assertion
  it drives at `event/eventtest/sections_lifecycle.go:49-51`, which checks both
  `context.Canceled` **and** `context.DeadlineExceeded`.
- **What:** the plan's C3 says *"§D.9's `cancellation` section asserts both halves in both
  windows"*. The bare-cancellation window is driven twice (cancelled and expired contexts,
  `sections_lifecycle.go:24-42`) — correct. The uncertainty window is driven once, with
  `Canceled` as the cause, so the `DeadlineExceeded` arm of line 49 can never be true and is
  vacuous.
- **Why this severity:** GAP-135 widened the cause suppression to the **whole** cause, so both
  sentinels go through one mechanism (`event/errors.go`) and the untested arm is very unlikely
  to differ. It is a coverage residue, not a hole.
- **Why this timing:** module-internal, no contract impact — deferred.
- **Close criteria:**
  - [ ] the uncertainty window is driven with a `DeadlineExceeded` cause as well as a
        `Canceled` one, or the redundant arm is noted as such
- **Status: deferred, moved to the plan's `## Debt`** (round 1 disposition), with
  the argument the finding itself makes: both sentinels go through one branch of
  `event/errors.go` after GAP-135, so the untested arm cannot differ from the
  tested one.

### GAP-16 [low][deferred] Three small duplications and two dead struct fields

- **Where:** (a) `event/eventtest/defects_test.go:59-64` dispatches the two store-shaped defects
  on their **prose names** (`case "ignores AppendRequest.Expected and admits every append":`),
  while `defects()` carries `over: nil` for exactly those rows; (b)
  `event/eventtest/report.go:40-48` (`certified`) and `event/eventtest/export_test.go:75-83`
  (`Certified`) are two parallel bodies for one question; (c)
  `event/eventtest/suite.go:120-123` gives the `Persistence` and `MonotoneVisibility` rows a
  `hook` of `""` and an `absent` of `false`, so the `Supported && absent` branch is unreachable
  for them.
- **What:** (a) is a name-keyed dispatch that `universality.md` names (`if id == "..."`), though
  it fails **loudly** — `t.Fatalf` at `defects_test.go:65` when a name stops matching — so it
  cannot silently drift; (b) is DRY at its smallest scale; (c) is two dead fields on a table.
- **Why this severity:** low on each count and none of them can produce a wrong verdict.
- **Why this timing:** module-internal, no contract impact — deferred.
- **Close criteria:**
  - [ ] a `defect` row carries what builds its store (`plain`, `broken`) rather than the test
        matching on its name
  - [ ] one implementation answers "how many of these verdicts are passes"
  - [ ] the two capability rows without a hook say so in the table's shape rather than with
        zero values
- **Status: deferred, moved to the plan's `## Debt`** (round 1 disposition). All
  three parts are recorded there by name, with the note that (a) fails loudly and
  none of them can produce a wrong verdict.

---

### What is genuinely clean, with the numbers

- **Microkernel.** `grep -rn "eventmemory\|eventtest" event/*.go` over non-test files: **0**.
  `go list -deps ./event/eventtest | grep -c eventmemory`: **0**. `event/eventtest` imports
  exactly one internal package (`event`) in every file. Adding `eventpg` costs **zero** diffs
  to `event/`.
- **Contract conformance.** All six exported symbols match the plan's § Contracts block
  character for character (`Tx` with two methods, `Factory` with four hooks, `Run`,
  `RoundTrip[S, ID, E]`, `Keys[S, ID]`, `Families`). No extra public surface: `Defect`,
  `Verdict`, `Certify`, `SectionNames`, `Certified` and `Missing` are all in `export_test.go`,
  a test file, and are on nothing's surface — §INV-019's shape held.
- **The three words.** `grep -c "t.Skip" event/eventtest/`: **0**, against `cachetest`'s seven.
  All twenty inventory names appear verbatim in `inventory.go:17-38` in the plan's order, and
  `TestEverySectionInTheInventoryWasReported` runs with a genuine shortened-inventory control.
- **Anti-vacuity rules 1, 2, 3 and 4 are real and executed.** `TestEverySectionFailsAgainstAStoreThatRefusesEverything`
  computes rule 4 over all twenty and passes; `TestAClaimedCapabilityWithAMissingHookIsRefused`
  drives both doors; `TestARunThatCertifiedNothingFails` has a non-zero control;
  `TestTheSuiteStillDetectsEveryDefectItWasBuiltToDetect` runs all **12** defects with the
  un-decorated store as each one's control, and both its uniqueness and section-existence
  assertions are present.
- **Purity.** Rehydration and upcasting touch no clock, no randomness, no I/O and no telemetry:
  `event/repo.go:197-269` is a `ReadStream` loop, a bounds check and a fold; `probe`'s
  randomness is one 4-byte draw at run start (GAP-3 covers its width).
- **Error discipline.** Every comparison in `event/eventtest/` is `errors.Is` against an
  exported sentinel; `grep` finds no error compared by string. The one `Error()` comparison,
  `sections_lifecycle.go:302`, asserts the **opposite** — that a store's text does *not* travel
  — which is the right use.
- **Concurrency.** `go test -race -count=1 ./event/...` green twice. Reading
  `event/eventmemory` and the fixtures for what `-race` did not happen to hit: `held.live`,
  `held.streams`, `held.global` and `held.position` are all written under `held.mutex`;
  `handOut` (`fixtures_test.go:123-127`) clones with a **full-slice expression**, so the fixture
  itself honours the clause GAP-1 says the suite cannot check; `concurrencySection`'s eight
  goroutines write to disjoint slice indices behind a `WaitGroup` and a gate. The one
  unsynchronised read I found is `stagingStore.Transaction` reading `tx.done` outside the mutex
  (`fixtures_test.go:344`), which no section reaches concurrently.
- **No global mutable state, no import cycles, no `helpers`/`utils`/`common`/`misc` file.**

---

## Round 2 — econv-impl-reviewer (clean context, re-audit after round 1's fixes) — 2026-09-07

Re-audit of `event/eventtest/`, `event/eventmemory/conformance_test.go` and the kernel
files S5 asserts against, read against the **code**. Read in full: `.agents/artifacts/plans/EVENTSOURCE_P1_PLAN.md`
(§ Carried gaps, § Microkernel classification, § Contracts `event/eventtest/`, § S5, §
Architecture metrics), `CLAUDE.md`,
`/home/user/.claude/skills/econv/references/{microkernel,architecture,building-blocks,
data-integrity,universality,readability,restrictions,gaps}.md`, every non-test and test file
of `event/eventtest/`, `event/eventmemory/{store,log}.go`, `event/{store,repo,binding,bounds,
identity}.go`, and round 1 of this file in full.

### Round 1's findings, re-verified against the code

| Finding | Verified closed? | Evidence |
|---|---|---|
| GAP-1 `[high]` | **yes** | `probe.spilled` (`sections_ownership.go:83-100`) clones the page, appends one byte to `page[0].Payload` and compares `page[1:]` with `kept[1:]`; `packedPages` (`defects.go:201-232`) is the thirteenth defect and `TestTheSuiteStillDetectsEveryDefectItWasBuiltToDetect` runs it. The `[]Envelope` half is closed only in letter — see GAP-21 |
| GAP-2 `[high]` | **yes** | `Factory.New`'s doc (`suite.go:29-38`) states "one or more times per section", the several-live rule, the no-reset rule and the `Backing().Equal` branch; `Factory.Begin`'s states the disposal obligation. `foreignCursor` (`sections_read.go:202-208`) and `durabilitySection` (`:234-238`) say why they build mid-section |
| GAP-3 `[high]` | **partly** | `grep -c 1024 event/eventtest/*.go` = **0**; `drain` ends on a short page and refuses a long one; `walkLog` ends on an empty page and `advanced` refuses a page beside its own cursor; every walking section mints a start cursor. But that cursor is minted by `probe.tail`, which walks the **whole log from `""`** — see GAP-18 — and the widened 16-byte run identity is one of the three literals GAP-17 measures |
| GAP-4 `[medium]` | **yes** | `classified` builds `through := one.build(store)` once and passes it as `failing.log`; `injectedAtRead` reads `event.Read(on.log, "")` (`sections_lifecycle.go:311-322, 345-352`) |
| GAP-5 `[medium]` | **yes** | `probe.begin` registers `t.Cleanup(func(){ _ = tx.Rollback(...) })` on the section's own `t` (`sections_transactions.go:237`); the staging fixture reports unresolved transactions at section end (`fixtures_test.go:631-636`) |
| GAP-6 `[medium]` | **yes** | `contended` opens both transactions first, loads the loser's token outside both, and issues the loser's first statement after the winner's commit (`sections_transactions.go:96-113`); every section opens with `this.context()` and `TestEveryStoreCallASectionMakesCarriesADeadline` passes |
| GAP-7 `[medium]` | **yes** | `unreported word = iota` (`report.go:15-20`), `Run` `t.Error`s every `unreported` row (`suite.go:66-70`), `certified` counts only `passed` |
| GAP-8 `[medium]` | **yes** | `roundTrips`/`keysRender`/`familiesDiffer` answer errors; `export_test.go:93-103` re-exports them; `go tool cover -func` puts all three at 100.0 % and the wrappers at 66.7 % (the `t.Fatal`) |
| GAP-9 `[medium]` | **yes** | `failureCase{ask, build}` replaces both flag parameters; `refusalClassesSection:185` asserts `err.Error() != policy.is.Error()` over all five rows |
| GAP-10 `[high][deferred]` | **still open, and larger** | `probe` is **8 fields / 51 methods across 7 files**; **two** functions still exceed four parameters, not the one the plan records — see GAP-22 |
| GAP-11, 12, 13, 14 `[low]` | **yes / GAP-13 on non-evidence** | `probe.refuses` gone; the S5 prose reads thirteen and six; `grep -rn "§UC-\|§INV-\|§D\." event/ --include=*.go \| grep -v _test.go` is empty. GAP-13's `event/repo.go` half rests on a `git diff` that cannot report — see GAP-23 |
| GAP-15, 16 `[low][deferred]` | recorded in `## Debt` | unchanged |

### Checkpoints, executed by the reviewer

| Command | Result |
|---|---|
| `go build ./... && go vet ./event/... && test -z "$(gofmt -l event)"` | clean |
| `go test -race -count=1 ./event/...` (twice) | `ok` event 1.276 / 1.283 s, eventmemory 1.041 / 1.039 s, eventtest 1.405 / 1.401 s — no flake |
| `go test -list … ./event/eventmemory/ \| grep -c '^Test'` = 1 | ok |
| `go test -list … ./event/eventtest/ \| grep -c '^Test'` = 4 | ok |
| `go test -race -v -run TestTheMemoryStoreSatisfiesTheContract ./event/eventmemory/` | 20 sections, **18 passed / 2 not certified**, none skipped — byte-for-byte the list pasted into the plan |
| `go test -race -count=1 -v ./event/eventtest/` | 13 top-level tests, all PASS — the plan's list |
| `go test -race -v -run TestATransactionCapableStoreSatisfiesTheContract ./event/eventtest/` | **19 passed / 1 not certified** (`durability`) — matches the plan |
| `make check` | `check-deps/tiers/utils/triplets/todo/replaces/tidy/otel-schema/workspace: ok` |

**The checkpoint output pasted into the plan is real**, and so are the two capability rows
§UC-044 predicts by name.

### Metrics counted (not eyeballed)

| Metric | Threshold | Measured |
|---|---|---|
| Exported symbols, `event/eventtest` | 7 | **6** (`go doc -all`: `Tx`, `Factory`, `Run`, `RoundTrip`, `Keys`, `Families`) — exactly the plan's contract, no silent surface |
| Lines per non-test file | 400 | max **390** (`sections_read.go`), then 375 / 352 / 331 / 251 / 243 / 213; 13 files, 3814 lines with tests, 2606 without |
| Functions over 50 lines | 50 | **4** of 143: `refusalClassesSection` 60, `payloadOwnershipSection` 59, `streamIdentitySection` 58, `concurrencySection` 55 |
| Function parameters | 4 | **2 breaches**: `wroteWhatItSaid` 7, `from` 5 (the plan records one) |
| `probe` fields / methods | 7 / — | **8 fields**, **51 methods** over 7 files (suite 20, transactions 10, read 8, lifecycle 6, write 3, ownership 3, report 1) |
| Internal imports per non-test file | 5 | max **1** — every file imports `event` and nothing else; `inventory.go`, `report.go`, `doc.go` import none |
| Import cycles | 0 | **0**; `go list -deps ./event/eventtest \| grep -c eventmemory` = **0** |
| Kernel imports of a concrete extension | 0 | `grep -rn "eventmemory\|eventtest" event/*.go` over non-test files = **0 lines** |
| Package-level mutable state | 0 | **0** — three `errors.New` sentinels, never reassigned |
| Dead unexported symbols | 0 | **0** (every declared name has ≥ 2 references) |
| `t.Skip` in the suite's own files | 0 | **0** |
| Statement coverage, `event/eventtest` | — | **85.0 %**; `durabilitySection` **35.0 %** (GAP-20), the three proxy wrappers 66.7 % (named residue) |
| Fakes a unit test of `probe` needs | 2 | **1** (a `Factory` over one `event.Store`) |

### Microkernel verdict for this section

`event/` names neither extension (0 grep hits over non-test files). `eventtest` does not depend
on `eventmemory` (0 deps). The extension point has a contract (`Factory`, and `Tx` — which has
none, GAP-19), a registration mechanism (the store's own package calls `Run`), a failure policy
(`admit` fatals on a claimed capability with a missing hook or an `Unstated` one, `needs` reports
*not certified*, a store's panic is not recovered) and a compatibility note (`event/store.go`).
Phase 2's `eventpg` needs **zero diffs to `event/`**. It does **not** run `event/eventtest/`
verbatim: GAP-17 and GAP-18 are properties of a store the sample does not contain.

---

## Round 2 — dispositions, 2026-09-07

All seven closed in the code. Nothing deferred, nothing rejected: every finding
was reproducible, and the two the reviewer measured (GAP-17's narrowed store,
GAP-18's growing log) are now fixtures that ship with the suite and tests that
run on every `make unit`.

| Finding | Disposition |
|---|---|
| GAP-17 `[critical]` | closed — every count derived from `Limits()`, the identity sized against `MaxKey`, a fatal refusal at the door when there is no room, and two tests (`eventtest` at the narrowest legal numbers, `eventmemory` at a hundredth of its defaults) |
| GAP-18 `[high]` | closed — `Factory.Tail` and `Factory.Window`, walks bounded by what the run wrote, and a growing-log fixture that reaches a verdict |
| GAP-19 `[medium]` | closed — `eventtest.Tx` states its four rules and `event/store.go` states the concurrency one |
| GAP-20 `[medium]` | closed — `persistentFactory`, the fifteenth defect beside it, and `durabilitySection` at 85.0 % |
| GAP-21 `[medium]` | closed — the `[]Envelope` case reads a proper prefix and the fourteenth defect breaks it; the unreachable check is gone |
| GAP-22 `[low]` | closed by removing the breach rather than recounting it: **0** functions over four parameters |
| GAP-23 `[low]` | closed — the S5 note says what `git` can establish for an untracked file, which is nothing |

**Checkpoint after the closures**

```
$ go build ./... && go vet ./event/... && test -z "$(gofmt -l .)" && go test -race -count=1 ./event/...
ok  	github.com/frostgrove/vv/event	1.279s
ok  	github.com/frostgrove/vv/event/eventmemory	1.052s
ok  	github.com/frostgrove/vv/event/eventtest	1.448s

$ go test -race -count=1 ./event/...      # again, no flake
ok  	github.com/frostgrove/vv/event	1.292s
ok  	github.com/frostgrove/vv/event/eventmemory	1.042s
ok  	github.com/frostgrove/vv/event/eventtest	1.464s

$ go test -race -count=1 -v -run 'TestTheMemoryStoreSatisfiesTheContract' ./event/eventmemory/
20 sections reported, 18 passed, 2 not certified (durability; store failure
classification), none skipped — and the same twenty, verdict for verdict, from
TestTheMemoryStoreSatisfiesTheContractAtNarrowerLimits

$ go test -race -count=1 -v ./event/eventtest/
16 top-level tests, all PASS

$ make unit    # every module, no FAIL
$ make check
check-deps: ok   check-tiers: ok   check-utils: ok   check-triplets: ok
check-todo: ok   check-replaces: ok   check-tidy: ok   check-otel-schema: ok
check-workspace: ok
```

**Metrics after round 2**, counted with `go/ast` over `event/eventtest`:
`probe` **7 fields** (was 8), **52 methods** (was 51), **0** functions over four
parameters (was 2), 4 functions over 50 lines (was 4), largest non-test file
**399** (`sections_read.go`, was 390 with `suite.go` at 473 mid-round, split into
`suite.go` 224 + `probe.go` 259), statement coverage **86.1 %** (was 85.0 %),
`durabilitySection` **85.0 %** (was 35.0 %), exported symbols **6**, unchanged.

---

### GAP-17 [critical][immediate] The suite is fitted to the in-tree fixture's `Limits`, and reports a correct store *failed* when its `MaxKey`, `MaxBatch` or `StreamPage` is legal but smaller

- **Where:** `event/eventtest/sections_read.go:113` (`for credit := range 7`), `:121`
  (`[]int{1, 2, 7}`), `event/eventtest/sections_ownership.go:23` (`for note := range 4`) and
  `:28` (`if len(page) != 4`), `event/eventtest/sections_read.go:346-359` (`spread`, two
  changes per append), `event/eventtest/sections_write.go:202-209` (`denseVersions`, a batch of
  three then a batch of two), `event/eventtest/suite.go:156-163` (`runIdentity`, 16 bytes → 32
  hex characters) and `:248-250` (`account`). The legal ranges these are measured against are
  `event/binding.go:84-117` and `event/bounds.go:9-16`: `MaxKey` 1…2048, `MaxBatch` 1…1024,
  `StreamPage` 1…4096 (subject only to the resident product).
- **What:** every quantity the suite writes is a literal chosen to fit the in-tree fixture's
  `limits()` (`fixtures_test.go:25-31`: `MaxKey 256`, `MaxBatch 16`, `StreamPage 8`). None is
  derived from `store.Limits()`. `keyAtCap` and `boundsSection` derive theirs correctly; the
  seven cited sites do not. `narrowed` (`stores.go:21-38`) is built as `narrowed{over{store}, 7}`
  unconditionally, so on a store publishing `StreamPage` 5 the *suite itself* constructs the
  dishonest store its own `bounds` defect exists to catch — it publishes 7 and returns 5.
- **Why this severity:** measured, not inferred. Running the whole suite against the staging
  fixture with one limit narrowed by a decorator that is otherwise honest gives, per setting,
  the number of sections reported **failed** for a store that is correct:

  | Setting | Sections *failed* | First message |
  |---|---|---|
  | `MaxKey` 16 / 32 / 36 | **19 of 20** | *loading {…} answered event: the identity does not render a legal stream key* |
  | `MaxKey` 40 | 4 (`stream identity`, `resumption`, `lifecycle`, `transactions`) | the same |
  | `MaxKey` 44 / 48 / 64 | 0 | — |
  | `MaxBatch` 1 | 7 | *appending 2 change(s) at version 0 answered event: the value is over a declared bound* |
  | `MaxBatch` 2 | 1 (`dense versions`) | *appending 3 change(s) at version 0 …* |
  | `StreamPage` 1 / 2 / 3 | 2 (`stream paging`, `payload ownership`) | *a stream of four events answered a page of 3, so the hand-offs below were never under test* |
  | `StreamPage` 5 / 6 | 1 (`stream paging`) | *the same stream read in pages of 7 folds to a balance of 21 at version 6 where its own store's pages fold to 28 at 7* |

  The failing store need not be hypothetical. `eventmemory` — the store this repository ships —
  accepts `LogSpec{MaxKey: 40}` and `Spec{StreamPage: 4, MaxBatch: 2}` (`eventmemory/log.go:41-74`,
  `eventmemory/store.go:44-76` bound each against the kernel ceiling and nothing else). Run
  through `eventtest.Run` it is reported **failed in 6 of 20 sections**: `stream identity`,
  `dense versions`, `stream paging`, `resumption`, `lifecycle`, `transactions`. Every message
  names the store; none names the suite. A store author's only signal is a red section, and the
  advice it implies — raise `MaxKey`, raise `MaxBatch`, raise `StreamPage` — is the suite's
  requirement wearing the store's name.
- **Why this timing:** it is a hardcode / fit-to-one-sample finding, which `gaps.md` says is
  never deferred, and it is the whole of what phase 2 runs to decide whether `eventpg` is
  correct. `eventpg` will publish its own numbers — a `StreamPage` derived from a row size, a
  `MaxKey` derived from a `varchar` — and the first thing it will meet is six red sections it
  did not cause. Changing the suite after a store has been certified against it means a store
  that was called conformant was measured by a different artefact.
- **Close criteria:**
  - [ ] every count the suite writes is derived from `store.Limits()`: the stream `payload
        ownership` reads is `min(4, StreamPage)` or longer with a paged read, `stream paging`
        narrows to values `≤ store.Limits().StreamPage`, `spread` and `denseVersions` split
        their batches at `MaxBatch`, and no bare `4`, `7` or `{1, 2, 7}` survives
  - [ ] the run identity and the section mark are sized against `Limits().MaxKey` — either the
        identity narrows to what fits with the collision argument restated, or the suite refuses
        at the door
  - [ ] a store whose `MaxKey` leaves no room for the suite's own identities is refused in
        `admit` with a message naming **the suite's** requirement and the store's number, never
        reported as a failed section
  - [ ] a test runs the whole suite against the staging fixture at `MaxKey` 40, `MaxBatch` 1 and
        `StreamPage` 1 and asserts **no section is reported failed**, with the un-narrowed store
        as its control
  - [x] `eventmemory` at `LogSpec{MaxKey: 40}`, `Spec{StreamPage: 4, MaxBatch: 2}` is reported
        with the same verdicts as at its defaults
- **Status: closed** (round 2 disposition). Every count the suite writes is now
  derived from what the store publishes, read **once at the door** and carried on
  the probe as an embedded `opening{capabilities, limits, run}`: `stream paging`
  writes `min(StreamPage, 4) + 1` events and narrows to `1, 2, min(StreamPage, 4)`
  — never past the store's own page, which would have made the suite the
  dishonest store its `bounds` defect exists to catch; `payload ownership` writes
  `max(StreamPage, MaxRead) + 1` and asserts the first read answers exactly one
  published page; `spread` and `dense versions` split their decisions at
  `MaxBatch` through `probe.batched`, so a store admitting one change at a time
  reaches the same five events in five appends; `boundsSection`, `keyAtCap` and
  `lifecycle` read `this.limits` rather than calling a store. The run identity is
  `min(16, (MaxKey - reserve)/2)` bytes, where the reserve is the separator
  `Compose` writes, the widest section mark and the widest label `account`
  admits — and a `MaxKey` with no room for even the four-byte narrowest is
  **fatal at the door**, naming the suite's requirement and the store's number,
  which is the third close criterion. `probe.store` refuses a factory whose later
  stores publish other numbers, so `Factory.New`'s new clause is checked rather
  than assumed. **Mutations:** `widest := widestNarrowing` reports *"the same
  stream read in pages of 2 folds to a balance of 1 at version 1 where its own
  store's pages fold to 15 at 5"*; a fixed 16-byte identity reports `stream
  identity`, `resumption`, `lifecycle` and `transactions` failed with *"the
  identity does not render a legal stream key"* — the four sections and the
  message GAP-17's own table predicted for `MaxKey` 40. The tests are
  `TestEverySectionIsCertifiedAtTheNarrowestLimitsAStoreMayPublish` (the whole
  suite at `MaxBatch` 1, `StreamPage` 1, `MaxRead` 1, `MaxKey` 40, with the
  ordinary fixture as its control) and
  `TestTheMemoryStoreSatisfiesTheContractAtNarrowerLimits` (`LogSpec{MaxKey: 40}`,
  `Spec{StreamPage: 4, MaxBatch: 2, MaxRead: 3}` — twenty verdicts identical to
  the defaults).

### GAP-18 [high][immediate] `probe.tail` walks the store's entire log from `""` in six sections, under a fixed 20-second budget, and requires a log nobody else is writing to

- **Where:** `event/eventtest/suite.go:321-331` (`tail`), called from
  `sections_read.go:16, 71, 134, 161, 316` and `sections_ownership.go:19` — six sections. The
  budget it runs under is `event/eventtest/suite.go:199` (`const sectionWindow = 20 * time.Second`),
  applied once per section at `:201-205`.
- **What:** round 1 closed GAP-3 by having each walking section mint its own start cursor
  before it writes. Minting that cursor is `tail`, and `tail` is an unbounded page-by-page walk
  of the whole log from the empty cursor — the same O(entire log) scan GAP-3 named, now six
  times per run instead of five, with the page cap replaced by a wall-clock one. Two
  preconditions are therefore assumed and neither is stated anywhere a store author reads:
  (1) the store's whole log can be paged through in 20 seconds, and (2) nothing else is
  appending to it while a section runs — `tail` terminates only on an empty page, so a
  concurrent writer keeps it going.
- **Why this severity:** wrong verdict on unseen input, and no way for the factory to widen the
  budget. `TestASectionWalksItsOwnTailOfALogSomebodyElseFilled` establishes the constant: 6 000
  events at `MaxRead` 5 costs 0.33 s in memory, i.e. ≈ 7 200 `ReadAll` calls per run. Put those
  calls on a network at 1 ms and the same fixture costs 7 s of the 120 s the six sections
  between them have; at 60 000 events it is 72 s and four sections report **failed** with
  *reading the log answered context deadline exceeded* — a message that names the store for the
  suite's own scan. A store whose `MaxRead` is small makes it arrive sooner, and `MaxRead` down
  to 1 is legal (`event/binding.go:94`). Precondition (2) is worse because it is silent: point
  the suite at a shared development database that another process is writing to and `tail`
  never returns; the section is reported *failed* at the deadline, and the next run against a
  quiet database passes, so the artefact that decides whether a store is correct is
  intermittent.
- **Why this timing:** it is the same fit-to-the-sample class `gaps.md` says is never deferred,
  and it is the first thing phase 2 meets: `eventmemory` starts every section against an empty
  or 6 000-event in-memory log, which is the only shape the mechanism was exercised against.
  Adding a `Factory` hook later changes the extension point after a store was written to it.
- **Close criteria:**
  - [ ] the suite stops scanning a log it does not own to find its end: either `Factory` grows a
        documented optional hook that answers a cursor at the current end of the log (with a
        section reported *not certified* when it is absent), or the tail is taken **once per
        run** rather than once per section and every section filters by its own family, or the
        walk is bounded by what the run itself wrote with the bound derived and stated
  - [ ] `sectionWindow` is either derived from the store's own published numbers or settable by
        the factory, and the derivation beside it states what a store must do to fit it
  - [ ] the requirement that the log be quiescent for the duration of a section is either
        removed or written into `Factory`'s doc as a precondition a store author must arrange
  - [x] a fixture whose log grows while a section walks it reaches a real verdict rather than
        the deadline
- **Status: closed** (round 2 disposition). Three changes, and the first is a new
  optional hook: **`Factory.Tail`** answers a cursor at the current end of this
  store's log. It gates nothing — without it every section still runs and the end
  is found by reading to it — and the refusal when that read does not finish names
  `Factory.Tail` and the remedy instead of the store: *"finding the end of this
  store's log answered context deadline exceeded, and a store whose log holds more
  than this run wrote answers a cursor at its end through Factory.Tail rather than
  being read to it"*. Second, **every walk is bounded by what the run itself
  wrote**: `walkLog` and `from` take a `walking{from, held, want}` and stop at
  `want` of this section's own events, so a walk ends at its own last event
  rather than at an empty page a second writer never leaves — which is the third
  option the criterion offers, with the bound derived from the section's own
  `spread` and stated on the type. `monotone visibility` no longer drains the log
  through a reader at all: it takes a tail, appends, and walks forward for one
  event of its own. Third, **`Factory.Window`** makes the section deadline the
  factory's to set, zero meaning the twenty seconds whose derivation is beside the
  constant. The quiescence precondition is written into `Factory.Tail`'s doc as
  the reason the hook exists. **Mutations:** with `walkLog` unbounded again the
  growing-log fixture reports `global order`, `conservation`, `global paging` and
  `resumption` *"reading the log answered context deadline exceeded"*; with
  `Factory.Tail` removed from that fixture the same four report the refusal that
  names the hook. The test is
  `TestASectionReachesAVerdictOverALogSomebodyElseIsStillWritingTo` — a store that
  publishes one event of somebody else's on **every** `ReadAll` — and it asserts
  no section is *failed* and that the run certifies exactly what the quiet store
  certifies.

### GAP-19 [medium][immediate] `eventtest.Tx` is an exported extension-point type with no contract, and the suite asserts an obligation on it that is written down only in the plan

- **Where:** `event/eventtest/suite.go:15-18` — `type Tx interface { Commit; Rollback }`, with
  no doc comment at all. The obligations the suite actually places on it are at
  `sections_transactions.go:67-69` (a second `Commit` must answer a **non-nil** error),
  `:241-245` (`commit` refuses the section on any non-nil `Commit`), `:247-251` (`rollback`
  refuses on any non-nil `Rollback`) and `:237` (the suite calls `Rollback` on every
  transaction it began, at section end, including committed ones, and ignores that error).
  The plan states the contract at `.agents/artifacts/plans/EVENTSOURCE_P1_PLAN.md:2209-2215`;
  the shipped package does not.
- **What:** four unstated rules on a type a store author must implement. The sharpest is the
  double-commit one: `staged` reports the `transactions` section **failed** —
  *"committing a transaction that had already been committed answered no error at all"* — for a
  `Tx` whose `Commit` is idempotent. `database/sql` and `pgx` both answer an error there, so the
  rule is a de-facto truth of two libraries rather than a stated obligation. Separately,
  `concurrencySection` (`sections_write.go:226-280`) runs eight goroutines against one store
  value and requires every `Store` method to be safe for that; `event/store.go` states this
  nowhere — the nearest sentence is `event/repo.go:9` ("share it between every request
  goroutine"), which is about `Repo`.
- **Why this severity:** a store author implements against what the package says. A `Tx` that
  wraps a driver handle and returns `nil` on a second `Commit` — a reasonable idempotent
  wrapper — is reported as a broken store by a section named `transactions`, and the reason
  text does not say the rule is the suite's. `microkernel.md` requires an extension point to
  declare a contract; this one declares half of one, and the half that is missing is the half
  a section fails on.
- **Why this timing:** it is the public contract of the extension point phase 2 is written
  against, and both the sentence and the assertion are cheap now. Adding the rule after
  `eventpg` is written is a contract change under a store.
- **Close criteria:**
  - [ ] `Tx` carries a doc stating what the suite requires: a `Commit` that answers an error
        when the transaction is already finished, a `Rollback` that answers nil on the path the
        suite takes, and that the suite calls `Rollback` on every transaction it began at
        section end and ignores that answer
  - [ ] `event/store.go`'s `Store` doc states whether the eight methods must be safe for
        concurrent use, or `concurrencySection` says which value it is entitled to share
  - [x] no obligation the suite asserts on `Tx` or `Store` exists only in the plan
- **Status: closed** (round 2 disposition). `eventtest.Tx` carries the four rules:
  a `Commit` that answers an error when the transaction has already been committed
  or rolled back (`staged` commits twice on purpose), a `Rollback` that answers nil
  wherever the suite calls it, and the disposal the suite makes on its own — it
  rolls back every transaction it began when the section ends, committed ones
  included, and ignores that answer. `event/store.go`'s `Store` doc states the
  fifth, which was never `Tx`'s: *"All eight are safe for concurrent use. One store
  value is bound once and the Repo over it is one handle every request goroutine
  shares, so a store that needs a lock takes its own."* That is the clause
  `concurrencySection` runs eight goroutines against. Both are recorded in the
  plan's § Contracts and in S5's file-level changes, because the second is a port
  contract sentence phase 2 implements against.

### GAP-20 [medium][immediate] `durability` has a control and no positive demonstration: no in-tree fixture claims persistence, so 65 % of its statements have never run on a passing path

- **Where:** `event/eventtest/sections_lifecycle.go:239-257` (`durabilitySection`);
  `go tool cover -func` puts it at **35.0 %**, the lowest figure in the package. The gate is
  `inventory.go:48-53` (`needsPersistence`). The three fixtures that could satisfy it declare
  `Persistence: event.Unsupported` (`fixtures_test.go:184-191, 313-320`) and `eventmemory`
  declares the same (`eventmemory/store.go:93-100`).
- **What:** the only store in the tree that claims `Persistence: Supported` is
  `refusingStore` (`fixtures_test.go:537-544`), which exists to fail every section. So the
  section's assertion path — the second store value, the `Backing().Equal` branch and the
  balance-of-12 reload — executes only against a store that refuses at the first append. The
  section has a control and no case: nothing in the tree shows it can report *passed*, and
  nothing shows it can distinguish a store that persists from one that does not.
- **Why this severity:** it is the one section whose first real exercise is `eventpg`, the one
  store that will claim persistence. A defect in the section — an inverted comparison, a
  `restarted` that is never actually a restart — reads as a defect in `eventpg`, in the phase
  where nobody yet trusts `eventpg`. This is the anti-vacuity rule the suite applies to stores,
  not applied to the suite's own section: rule 4 gives it a failing control, and no rule gives
  it a passing one.
- **Why this timing:** the fixture is a few lines now (a staging store whose `New` reuses one
  `held` and claims `Persistence: Supported`) and is a change to the artefact phase 2 is
  certified by later.
- **Close criteria:**
  - [ ] a fixture that claims `Persistence: Supported` and whose second store value genuinely
        reads what the first wrote runs `durability` and is asserted **passed**
  - [ ] a defect row beside it — a store that claims persistence and whose second value folds
        the stream to zero — is asserted **failed** by `TestTheSuiteStillDetectsEveryDefectItWasBuiltToDetect`
  - [x] `go tool cover -func` shows `durabilitySection` above 80 %, or the residue is named
- **Status: closed** (round 2 disposition). `persistentFactory` is the first store
  in this tree to claim `Persistence: Supported`: every `New` answers another value
  over one log, so the second value a section takes is a restart. Beside it is the
  fifteenth defect — `persistentFactory(true)`, which claims the same and answers a
  `Sibling` over the first's backing with nothing in it — and
  `TestTheSuiteStillDetectsEveryDefectItWasBuiltToDetect` runs it. The test is
  `TestAStoreThatKeepsWhatItWroteIsCertifiedForDurability`, which asserts *passed*
  for the honest store and *failed* for the defect. **Mutation:** widening the
  balance check to admit zero reports the defect *passed*. `go tool cover -func`:
  `durabilitySection` **85.0 %**, up from 35.0 %; the residue is the *not certified*
  arm, which a store that claims persistence does not take.

### GAP-21 [medium][immediate] The `[]Envelope` half of the payload-ownership clause is asserted with a check that cannot fail

- **Where:** `event/eventtest/sections_ownership.go:91-99` (`spilled`'s second half) and
  `:61-66` (the global-read pooling check). The obligation is `event/store.go:94-99` and the
  plan's C7.
- **What:** the assertion is
  `batch := append(beside, seen[0]); if !samePage(batch[:len(beside)], seen)`. `append` never
  writes below `len(beside)`, and `seen` is a deep clone of `beside` taken one line earlier, so
  the two are equal on every input — the refusal at `:95` is unreachable. The check that could
  fail is the one after it (`again` against `written`), and it can only fire if the store serves,
  out of the same array, an event at index ≥ `len(page)`; the section reads a four-event stream
  in one page (`:27-30` refuses otherwise), so there is no such event and the realistic defect —
  a store returning `whole[after:after+n]` out of its own retained slice, appended to by a
  consumer — writes past the end of what the store will ever serve. The global pooling check at
  `:61-66` has the same shape: `this.readAll(ctx, store, cursor)` is issued from a cursor past
  everything this run wrote, so the second page is empty in every in-tree fixture and cannot
  disturb the first.
- **Why this severity:** GAP-1's second close criterion is met in letter and empty in effect,
  and the suite's own promise is that a *passed* line means the named thing was asked. It is
  not a wrong verdict today — the payload half of the clause does the work and `packedPages`
  proves it — so this is a hole rather than a false accusation.
- **Why this timing:** the fix is the same one GAP-17 needs (a stream longer than one of the
  store's own pages), so the two close together or the second becomes a second edit of the same
  section.
- **Close criteria:**
  - [ ] the `[]Envelope` case reads a **proper prefix** of a longer stream — a page the store
        publishes rather than the whole history — so an append to the returned slice lands on an
        event the store still serves, and a fixture that sub-slices its own retained array is
        added to `defects()` and asserted *failed*
  - [ ] the unreachable `samePage(batch[:len(beside)], seen)` check is removed or replaced by
        one that a defect can break
  - [x] the global-read pooling check is issued where a second page exists, or it says why it
        cannot be
- **Status: closed** (round 2 disposition). The section writes
  `max(StreamPage, MaxRead) + 1` events, so what a read answers is a **page** and
  not a history: `spilled` reads page one, clones the page after it, appends one
  envelope to page one's slice and asserts the page the store still serves is
  unchanged — an assertion a defect breaks, where `samePage(batch[:len(beside)],
  seen)` could not be false on any input and is gone. The fourteenth defect is
  `retainedPages`, a store that answers every page of a stream out of one array it
  keeps, cut without a third index; it is reported *failed* for the payload
  ownership section, and the message is the new assertion's — *"appending a 9th
  envelope to a page of 8 changed the page the read after it answers"*. The
  global-read check is now issued from a cursor with a page after it, because the
  stream is longer than one `MaxRead`. **Mutation, and it is reported as it
  happened rather than better than it happened:** removing the `[]Envelope`
  assertion does **not** turn
  `TestTheSuiteStillDetectsEveryDefectItWasBuiltToDetect` red — the corruption is
  caught three assertions later by the kernel's own page check (*"answered a page
  that does not begin at the version it was read from and rise by one"*). So the
  new assertion is what makes the failure name the hand-off and arrive at it,
  rather than what makes the defect visible at all.

### GAP-22 [low][immediate] The plan's Breaches table and round 1's disposition undercount the parameter breach, and `probe` grew rather than shrank

- **Where:** `.agents/artifacts/plans/EVENTSOURCE_P1_PLAN.md` § Architecture metrics →
  Breaches, last row ("one 7-parameter method (`wroteWhatItSaid`)" … "which leaves **one**
  function over four parameters where there were four"), and this file's GAP-10 disposition
  ("leaving **one** function over four parameters").
- **What:** there are **two**: `wroteWhatItSaid` at 7 (`sections_lifecycle.go:202`) and
  `probe.from` at 5 (`sections_read.go:224`). GAP-10's own text listed `from` at 5 among the
  four original breaches and nothing in round 1 touched it. The method count in the same row
  (51) and the field count (8) are correct.
- **Why this severity:** cosmetic against the code, but the Breaches table is the record a
  later agent reads instead of counting, and this is a counted metric that disagrees with the
  tree.
- **Why this timing:** S6 consolidates these numbers into `docs/`.
- **Close criteria:**
  - [x] the Breaches row and the GAP-10 disposition read **two** functions over four
        parameters, naming `wroteWhatItSaid` (7) and `from` (5)
- **Status: closed** (round 2 disposition), by removing the breach rather than
  recording it: `wroteWhatItSaid` takes the `fixture` value the failure cases
  already used (7 → 2) and `from` takes a `walking` value (5 → 3). A `go/ast` walk
  over the package counts **0** functions of more than four parameters. The
  Breaches row and the `## Debt` entry both say so, with the field count corrected
  to **7** and the method count to **52**, all three counted rather than
  remembered.

### GAP-23 [low][immediate] GAP-13's closure rests on a `git diff` that structurally cannot report anything

- **Where:** `.agents/artifacts/plans/EVENTSOURCE_P1_PLAN.md` § S5, third file-level change —
  *"`event/repo.go` has an mtime inside the same window and **no diff**"*; the same sentence is
  this file's GAP-13 disposition.
- **What:** `event/repo.go` is **untracked** (`git status --porcelain event/` reports `??
  event/repo.go`; the whole `event/` tree except `store.go` and the S1–S2 files is new work).
  `git diff` reports nothing for an untracked file whatever its content, so the evidence quoted
  proves only that the file is new. Its mtime, `17:22:54`, is the newest of every file under
  `event/` outside `event/eventtest/` and 38 seconds after `store.go`'s comment edit — squarely
  inside the S5 window and after every S4-era file (16:06–16:41).
- **Why this severity:** the content of `repo.go` is right and nothing in it contradicts S4, so
  no behaviour is at risk. What is wrong is the audit trail: `restrictions.md` §1 requires an
  out-of-list edit to be a recorded plan change, and the record cites a check that cannot fail.
- **Why this timing:** S6 freezes these files into `docs/`, and a second reviewer will re-derive
  the same non-answer.
- **Close criteria:**
  - [ ] the S5 note states what actually establishes `repo.go`'s content — a diff against the
        S4 checkpoint's copy, a hash recorded at the end of S4, or the admission that the file
        is untracked and the claim cannot be made from `git`
  - [x] if `repo.go` did change during S5, the change is described with its reason as
        `store.go`'s is
- **Status: closed** (round 2 disposition). S5's note now withdraws the claim
  rather than repairing it: the whole of `event/` except `store.go` is untracked,
  so `git diff` reports nothing for `repo.go` whatever its content and the check
  cited could not have failed. What holds the file is S4's own suite — unchanged
  and green on every `go test ./event/` — and no S5 change to it is recorded
  because none was made. The note says the claim cannot be made from `git` until
  there is a commit to diff against.

---

### What is genuinely clean this round, with the numbers

- **Microkernel.** `grep -rn "eventmemory\|eventtest" event/*.go` over non-test files: **0**.
  `go list -deps ./event/eventtest | grep -c eventmemory`: **0**. Every non-test file of
  `event/eventtest` imports at most one internal package. Adding `eventpg` costs **zero** diffs
  to `event/`.
- **Contract conformance.** `go doc -all ./event/eventtest` lists exactly the plan's six
  symbols with the plan's signatures. `Defect`, `Verdict`, `Certify`, `SectionNames`,
  `Certified`, `Missing` and the three `*Answers` functions are all in `export_test.go` — a test
  file, on nothing's surface, which is §INV-019's shape held.
- **The three words.** `t.Skip` in the suite's own files: **0**. All twenty inventory names
  appear verbatim in `inventory.go:17-38` in the plan's order, and
  `TestEverySectionInTheInventoryWasReported` runs with a genuine shortened-inventory control.
- **Anti-vacuity.** All four rules execute: `TestEverySectionFailsAgainstAStoreThatRefusesEverything`
  computes rule 4 over all twenty; `TestAClaimedCapabilityWithAMissingHookIsRefused` drives
  both doors of rules 1 and 3; `TestARunThatCertifiedNothingFails` has a non-zero control;
  `TestTheSuiteStillDetectsEveryDefectItWasBuiltToDetect` runs **13** defects with the
  un-decorated store as each one's control and asserts name-uniqueness and section existence.
- **Purity.** `event/repo.go:197-269` rehydrates with a `ReadStream` loop, a page check and a
  fold — no clock, no randomness, no I/O, no telemetry. The suite's only randomness is one
  16-byte draw at run start; the only clock reads are in the fixtures.
- **Error discipline.** Every comparison in `event/eventtest/` is `errors.Is` against an
  exported sentinel. The two `Error()` comparisons (`sections_lifecycle.go:185, 329`) assert the
  opposite — that a store's or a decorator's own text does *not* travel — which is the right
  use, and round 1 strengthened the first from a `"quota"` substring to an equality.
- **Concurrency.** `go test -race -count=1 ./event/...` green twice, no flake. Reading for what
  `-race` did not hit: `held.streams`, `held.global`, `held.position` and `held.live` are all
  written under `held.mutex`; `remember`/`forget` are only reached from `Append`/`finish`, which
  hold it; `handOut` clones with a full-slice expression; `deadlines` and `armed` each carry
  their own mutex; `concurrencySection`'s eight goroutines write disjoint indices behind a
  `WaitGroup` and a gate.
- **No global mutable state, no import cycles, no `helpers`/`utils`/`common`/`misc` file**, and
  no dead unexported symbol in the package.

---

## Round 3 — econv-impl-reviewer (clean context, re-audit after round 2's fixes) — 2026-09-07

Re-audit of `event/eventtest/`, `event/eventmemory/conformance_test.go` and the kernel files
S5 asserts against, read against the **code**. Read in full: `.agents/artifacts/plans/EVENTSOURCE_P1_PLAN.md`
(§ Carried gaps, § Microkernel classification, § Contracts `event/eventtest/`, § S5,
§ Architecture metrics, § Debt), `.agents/artifacts/usecases/EVENTSOURCE_P1_USECASES.md` § D.9
and its division-of-labour table, `CLAUDE.md`,
`/home/user/.claude/skills/econv/references/{microkernel,architecture,building-blocks,
data-integrity,universality,readability,restrictions,gaps}.md`, every non-test and test file of
`event/eventtest/`, `event/{store,repo,reader,bounds,binding,identity}.go`,
`event/eventmemory/{store,log}.go`, and rounds 1 and 2 of this file in full.

### Round 2's findings, re-verified against the code

| Finding | Verified closed? | Evidence |
|---|---|---|
| GAP-17 `[critical]` | **yes, and re-measured independently** | every count derives from `this.limits` (the embedded `opening`, `suite.go:134-138`). I ran the whole suite from a scratch module outside the tree against `eventmemory` at four further legal limit sets — `MaxKey 2048 / MaxPayload 1 MiB / StreamPage 64 / MaxRead 64 / MaxBatch 1024`; `MaxKey 21 / StreamPage 3 / MaxRead 2 / MaxBatch 1`; `StreamPage 4096 / MaxRead 1`; `StreamPage 1 / MaxRead 4096` — and every one reported **18 passed / 2 not certified**, verdict for verdict identical to the defaults. `MaxKey 20` is **fatal at the door** with *"this store publishes a MaxKey of 20 and this suite needs 21 of it"*, which is the third close criterion demonstrated rather than claimed |
| GAP-18 `[high]` | **yes** | `Factory.Tail` and `Factory.Window` exist and are documented (`suite.go:67-79`); `walking{from, held, want}` (`probe.go:214-218`) bounds `walkLog` and `from`; the fallback's refusal names `Factory.Tail` (`probe.go:200`); `TestASectionReachesAVerdictOverALogSomebodyElseIsStillWritingTo` green |
| GAP-19 `[medium]` | **yes** | `Tx`'s doc carries the four rules (`suite.go:14-21`); `event/store.go:129-131` carries the concurrency clause |
| GAP-20 `[medium]` | **yes** | `persistentFactory` (`fixtures_test.go:755-785`), the fifteenth defect beside it, `durabilitySection` **85.0 %** measured with `go tool cover -func` |
| GAP-21 `[medium]` | **yes** | `spilled` (`sections_ownership.go:92-117`) reads a proper prefix and asserts the page after it; `grep 'batch\[:len('` over the package is empty, so the unreachable check is gone; `retainedPages` is the fourteenth defect |
| GAP-22 `[low]` | **yes** | `go/ast` over the package: **0** functions of more than four parameters out of **149** |
| GAP-23 `[low]` | **yes** | `git ls-files event/` still does not list `event/eventtest/` or `event/eventmemory/`; the note's withdrawal is the accurate statement |
| GAP-10, 15, 16, 25 `[deferred]` | recorded in `## Debt` | unchanged, and GAP-10's numbers in the Breaches row still match the tree |

Nothing round 2 closed was reworded rather than fixed, and no closure introduced a new
blocking defect. The three findings below that touch round 2's own work are all `[low]`.

### Checkpoints, executed by the reviewer

| Command | Result |
|---|---|
| `go build ./... && go vet ./event/... && test -z "$(gofmt -l event)"` | clean |
| `go test -race -count=1 ./event/...` (twice) | `ok` event 1.278 / 1.277 s, eventmemory 1.054 / 1.049 s, eventtest 1.436 / 1.443 s — no flake |
| `go test -list … ./event/eventmemory/ \| grep -c '^Test'` = 1 | ok |
| `go test -list … ./event/eventtest/ \| grep -c '^Test'` = 4 | ok |
| `go test -race -v -run TestTheMemoryStoreSatisfiesTheContract ./event/eventmemory/` | 20 sections, **18 passed / 2 not certified**, none skipped — byte-for-byte the list pasted into the plan, including the two reasons §UC-044 predicts by name |
| `go test -race -count=1 -v ./event/eventtest/` | **16** top-level tests, all PASS — the plan's list, name for name |
| `make unit` | 0 `FAIL` |
| `make check` | `check-deps/tiers/utils/triplets/todo/replaces/tidy/otel-schema/workspace: ok` |

**The checkpoint output pasted into the plan is real.**

### Metrics counted (`go/ast` and `wc`, not eyeballed)

| Metric | Threshold | Measured |
|---|---|---|
| Exported symbols, `event/eventtest` | 7 | **6** (`go doc -all`: `Tx`, `Factory`, `Run`, `RoundTrip`, `Keys`, `Families`) — the plan's contract exactly, no silent surface |
| Lines per non-test file | 400 | max **399** (`sections_read.go`); 14 files, **2 803** lines; test files 6, 1 383 lines |
| Functions over 50 lines | 50 | **4** of 149: `payloadOwnershipSection` **62**, `refusalClassesSection` 60, `streamIdentitySection` 58, `concurrencySection` 55 |
| Function parameters | 4 | **0** breaches of 149 |
| Nesting depth | 3 | max **3** over 149 functions (recursive walk, not an `ast.Inspect` counter) |
| `probe` fields / methods | 7 / — | **7** fields (one is the embedded `opening`, three more inside it), **52** methods over 7 files — GAP-10, in `## Debt` |
| Internal imports per non-test file | 5 | max **1**; `inventory.go`, `report.go`, `doc.go` import none |
| Import cycles | 0 | **0**; `go list -deps ./event/eventtest \| grep -c eventmemory` = **0** |
| Kernel imports of / branches on a concrete extension | 0 | `grep -rn "eventmemory\|eventtest" event/*.go \| grep -v _test.go` = **0 lines** |
| Package-level mutable state | 0 | **0** — two `errors.New` sentinels and five `const` blocks, nothing reassigned |
| Comment lines, non-test | — | 303 / 2 803 = **10.8 %**; every one I read states a *why* the code cannot show |
| `t.Skip` in the suite's own files | 0 | **0** (the one in the package is `suite_test.go`'s `SkipNow` inside a factory hook) |
| Statement coverage, `event/eventtest` | — | **86.1 %**; lowest: `advanced` 50.0 %, `commit`/`rollback` 50.0 % (their refusal arms), `foreignCursor` 55.6 %, the three exported wrappers 66.7 % (their `t.Fatal`) |
| Fakes a unit test of `probe` needs | 2 | **1** (a `Factory` over one `event.Store`) |
| Defects detected, each with its own control | — | **15** of 15, green |

### The adversarial pass, run rather than reasoned

I built a scratch module outside the tree (`replace github.com/frostgrove/vv => …`) so nothing
was written into `src/`, and drove the shipped suite against stores the tree does not contain:

- **A complete store hand-written in a third package out of the exported vocabulary alone**
  — §INV-019 reproduced independently of `fixtures_test.go` — compiled and ran.
- **That store serving one envelope per `ReadAll` while publishing `MaxRead` 32** (legal: *at
  most* `MaxRead`): no false failure. The one section it did fail, `resumption`, was a genuine
  defect of my store — it accepted a cursor another backing minted — which is the suite doing
  its job, and I fixed my store and it went green.
- **The shape `eventpg` will have** — one backing, every `New` another value over it,
  `Persistence` and `SharedBacking` claimed, `Factory.Tail` supplied: `durability` and
  `shared backing` **passed**, `resumption` reported *not certified* for the one half a
  single-database factory has no elsewhere for, and **no section failed**. Phase 2 needs
  **zero diffs to `event/` and zero to `event/eventtest/`**.

## Round 3 — dispositions, 2026-09-07

Phase 5. Every `[immediate]` finding is closed in the code; the four `[deferred]`
ones are in the plan's `## Debt` with their own arguments, and GAP-33's first half
is closed rather than carried because this round extended the fixture it names.

| Finding | Disposition |
|---|---|
| GAP-24 `[high]` | closed — `probe.instants` in `dense versions` (non-zero, and the same instant on the read after), the sixteenth defect beside it, and `TestAStoreThatMintsTheRecordedInstantWhenAnEventIsReadIsNotCertified` for the read-time store the defect row cannot be |
| GAP-25 `[medium]` | closed by the first of the two options — `Factory.Unparsable`, `probe.unparsableCursor` in `resumption`, *not certified* without the hook, the eighteenth defect, and `eventmemory` demonstrating the case |
| GAP-26 `[medium]` | closed — `probe.unchanged` in `binding` reads all three twice on one store value with an operation between, the backing through `Equal`; the seventeenth defect for the limits half and `TestAStoreWhoseCapabilitiesOrBackingChangeUnderOneValueIsNotCertified` for the other two |
| GAP-27 `[medium]` | **deferred** — two of three criteria met (the derivation states the cost and names `Factory.Window`; the plan's `## Debt` carries the row phase 2 reads); the refusal that names the hook is what is left |
| GAP-28 `[low]` | closed — `probe.store` compares `Capabilities()` as it compares `Limits()`, and `TestAFactoryWhoseLaterStoresPublishSomethingElseIsRefused` is the first control either half of that comparison has had |
| GAP-29 `[low]` | closed — the per-file and breaches rows recounted with `wc -l` and `go/ast`: six `sections_*.go` files at 364/354/306/251/238/127, five functions of 50–62 lines, 0 of 157 over four parameters, 55 `probe` methods |
| GAP-30, 31, 32 `[low][deferred]` | recorded in the plan's `## Debt`, each with the finding's own argument |
| GAP-33 `[low][deferred]` | first half **closed** — `stagingTx.done` is an `atomic.Bool` and its transition is a `CompareAndSwap` inside the lock that publishes, so the two unsynchronised reads are gone. The second half (`persistentFactory`'s captured `shared`) is in `## Debt` |

**What the round cost the tree.** `sections_read.go` reached 421 lines with the
cursor clause in it, over `architecture.md`'s 400, so `resumption` and the three
helpers only it uses moved to `sections_resumption.go` — no behaviour and no
symbol changed. `Factory` grew its seventh field, which is at
`architecture.md`'s per-class threshold and not over it. The defect inventory is
**18** rows; `eventmemory` still reports **18 passed / 2 not certified**, verdict
for verdict, and the staging fixture still reports 19 of 20.

**Mutation evidence, each restored and its sha256 compared afterwards.**

| Mutation | What turned red |
|---|---|
| `probe.instants` call removed from `dense versions` | *"the suite no longer detects a store that answers every envelope with no recorded instant at all: its dense versions section was reported passed"*, and *"a store that mints the recorded instant at read time was reported passed for dense versions"* |
| `probe.unchanged` made to compare the first reading with itself | the seventeenth defect *passed*, and both stores beside it — the wavering capabilities and the fresh backing — *passed* |
| `probe.unparsableCursor` call removed from `resumption` | the eighteenth defect *passed*, and the missing-hook run reported *passed* rather than *not certified* |
| the `Capabilities()` comparison removed from `probe.store` | *"a factory whose second store claims fewer capabilities than the store it was admitted on was reported passed for binding"* |
| `keysRender`'s duplicate-key branch deleted | the fuzz seeds and `TestEachProxyReportsTheThingItExistsToFind` both red — *"two identities this aggregate does not render two legal keys for were reported injective"* |
| `keysRender` widened to call every second identity a collision | the fuzz seeds red the other way — *"two identities rendering the two distinct keys … were reported to collide"* — so both arms of the equivalence are falsifiable |

---

### GAP-24 [high][immediate] The recorded instant is a store obligation with no case anywhere in the suite, so a store that never sets one is certified

- **Where:** the obligation is `event/store.go:171-176` — `Append` *"assigns dense versions and
  strictly increasing positions … **records the instant from its own injected clock**"* — and the
  field is `event/store.go:101` (`Envelope.RecordedAt`). [SPEC] §D.6's division-of-labour table
  puts *"the recorded instant, from its injected clock"* squarely in the **store's** column
  (`EVENTSOURCE_P1_USECASES.md:5152`). `grep -rn RecordedAt event/` returns **13 lines: one
  declaration, four fixture writes, four `eventmemory` private-test assertions
  (`event/eventmemory/admission_test.go:56-213`), one `event/store_test.go` shape row and two
  `event/recordingstore_test.go` writes — and not one line in `event/eventtest/`** outside
  `fixtures_test.go`.
- **What:** the conformance suite asserts nothing about `Envelope.RecordedAt`. Not that it is
  set, not that it is the same instant on two reads of one event, not that the envelopes of one
  atomic append carry one instant — all three of which `eventmemory` asserts **in its own
  package test**, which is precisely the sign that this is a store-side property that varies with
  the store and therefore belongs in the suite by §D.9's own stated rule (*"a property that does
  not vary with the store does not belong in it"* — this one does).
- **Why this severity:** `gaps.md` grades a missing test for a stated invariant `high`, and the
  concrete input is ordinary. A `eventpg` whose `SELECT` list omits `recorded_at`, or whose scan
  drops it, or whose migration added the column after the first insert path was written, returns
  `time.Time{}` on every envelope. The suite reports **20 sections, 18 passed / 2 not certified**
  — the same line `eventmemory` prints. Every consumer that displays, exports or audits the
  instant then reads `0001-01-01T00:00:00Z`, and because §INV-009 forbids the instant as an
  ordering key, nothing else in the system will ever notice. A second, subtler store: one that
  fills `RecordedAt` at **read** time from `time.Now()` because the column does not exist —
  every read of one event answers a different instant, and an audit trail that is regenerated on
  each read is worse than a missing one.
- **Why this timing:** this is the artefact phase 2 runs **verbatim** to decide whether `eventpg`
  is correct, and S6 freezes it into `docs/`. Adding the case after `eventpg` has been certified
  means a store that was called conformant was never measured on one of the four things §D.6
  says is its own. It is the same argument round 1's GAP-1 and round 2's GAP-20 were graded on,
  and both were closed rather than deferred. The case is two assertions in a section that
  already re-reads a page.
- **Close criteria:**
  - [ ] a section asserts that every envelope of an event **this run appended** answers a
        non-zero `RecordedAt` — the one property that is universal, because the contract promises
        the store's *own* clock and never the suite's
  - [ ] the same section asserts the instant is **stable across two reads of one event**, which
        is what separates a stored instant from one minted at read time
  - [ ] a sixteenth row in `defects()` — a decorator that blanks `RecordedAt` on every envelope
        it forwards — is reported *failed* for that section by
        `TestTheSuiteStillDetectsEveryDefectItWasBuiltToDetect`, with the undecorated store
        *passed*
  - [ ] `eventmemory` and the in-tree fixtures still pass the enlarged section, and the plan's
        S5 `Covers` list names the clause
- **Status: closed** — `probe.instants`, the sixteenth defect, and the read-time store beside it

### GAP-25 [medium][immediate] "A cursor this store cannot parse" is a contract clause the suite has no case for, and the two mechanisms that exist do not reach it for the store phase 2 is writing

- **Where:** the clause is `event/store.go:118-120` — *"A cursor minted over another backing,
  **one this store cannot parse**, and one in a format it no longer accepts are all
  `Failure(BadCursor, …)`"* — restated in [SPEC] §D.6
  (`EVENTSOURCE_P1_USECASES.md:5195-5197`). Every cursor the suite ever presents is one of
  three: `""` (`sections_write.go:50,54`, `sections_lifecycle.go:348`), one this store minted
  (`sections_read.go:166,187,250`), or one **another store minted**
  (`sections_read.go:240`, `foreignCursor`).
- **What:** the store's own parsing of a cursor it could not have minted is never exercised.
  `BadCursor` reaches the sections through exactly two doors, and neither is the store's parser:
  `Factory.Fail(t, s, event.BadCursor)` **injects** the classification
  (`sections_lifecycle.go:292`), which certifies the kernel's map and not the store's detection;
  and `foreignCursor` presents a cursor minted over a second backing — which is reported
  *not certified* on any factory whose stores share one backing (`sections_read.go:236-239`).
- **Why this severity:** the factory `eventpg` will supply builds every store over **one**
  database, which I verified is the shape the suite handles cleanly — and on that shape
  `foreignCursor` reports *"this factory builds no store over a second backing"*, so **the whole
  of the store's cursor validation is uncertified**. A `eventpg` whose `ReadAll` parses with
  `at, _ := strconv.ParseUint(string(after), 10, 64)` and ignores the error reads from position 0
  for any garbage cursor. The consumer symptom is the one §UC-053 exists to prevent: a projector
  whose persisted checkpoint was truncated or written by another deployment silently re-reads the
  whole log and re-applies every event, instead of being refused.
- **Why this timing:** the honest fix is a `Factory` hook — only the store knows what it cannot
  parse, so a literal the suite invents is not universal and a corrupted mint can still parse to a
  legal position for some stores. Growing `Factory` after `eventpg` is written changes the
  extension point under a store, which is the timing rule GAP-18's `Tail` was graded on.
- **Close criteria:**
  - [ ] either `Factory` grows a documented optional hook answering a cursor **this store cannot
        parse**, with the section reporting *not certified* — visibly, in the third word — when it
        is absent, so an uncertified clause is stated rather than silent
  - [ ] or the clause is recorded in the plan's § S5 and in `event/store.go`'s own comment as one
        the suite deliberately does not certify, with the reason, so `eventpg`'s author knows to
        pin it in `eventpg`'s own package test
  - [ ] whichever is chosen, `event/eventmemory` demonstrates the case, and a defect row —
        a store that answers a page for a cursor it did not mint — is reported *failed*
- **Status: closed** — `Factory.Unparsable`, `probe.unparsableCursor`, the eighteenth defect, and `eventmemory`'s own hook

### GAP-26 [medium][immediate] `Capabilities`, `Limits` and `Backing` are contracted to be constant for a store's life and nothing asserts it within one store value

- **Where:** the clause is `event/store.go:104-110` — *"Capabilities, Limits and Backing are pure
  and constant for the store's life: the same value on every call … and a decorator answers the
  wrapped store's values rather than its own."* The only comparison in the suite is **across two
  store values** (`probe.go:76-78`), which is `Factory.New`'s obligation and a different one. The
  suite reads all three repeatedly and never twice for the same question: `admit` once
  (`suite.go:155,159`), `dishonestStores` once (`sections_write.go:65,68`), `drain` on every page
  (`probe.go:161`), `Backing()` in four sections.
- **Why this severity:** the plausible store is not exotic. A store whose `Capabilities()`
  answers `Transactions: Supported` only while a transaction is bound — an easy thing to write
  when the capability is derived from the executor rather than from the store — is admitted at the
  door with the answer it gave then, and every section afterwards branches on `this.capabilities`
  while the kernel re-reads. A store whose `Limits().StreamPage` shrinks after a reconfiguration
  makes `probe.drain` (which re-reads it) and `event/repo.go:213` (which uses the value retained
  at `Bind`) stop the same walk at two different places, and a `Load` truncates a history at the
  old page boundary with no refusal anywhere. Neither is reported.
- **Why this timing:** it is the artefact that certifies every store, S6 freezes it, and the case
  is three comparisons at the top of a section that already holds the store.
- **Close criteria:**
  - [ ] `binding` reads `Capabilities()`, `Limits()` and `Backing()` **twice** on one store value,
        with a store operation between the two reads, and refuses when any of the three differs
  - [ ] the `Backing()` comparison uses `Equal` rather than `==`, since a backing is compared and
        never identical (`event/backing.go`)
  - [ ] a defect row — a decorator answering a different `Limits()` on its second call — is
        reported *failed* for `binding`, with the undecorated store *passed*
- **Status: closed** — `probe.unchanged` in `binding`, the seventeenth defect, and the two stores beside it

### GAP-27 [medium][deferred] `payload ownership` writes `max(StreamPage, MaxRead) + 1` events with no ceiling, where the section beside it caps its own count for exactly that reason

- **Where:** `event/eventtest/sections_ownership.go:28` (`notes := max(this.limits.StreamPage,
  this.limits.MaxRead) + 1`), against `event/eventtest/sections_read.go:109-116`, where
  `widestNarrowing = 4` carries the argument *"a store publishing a page of four thousand would
  otherwise be asked to write four thousand events to be certified on the kernel's paging rather
  than on its own."*
- **What:** the count is derived rather than fitted, and it is the genuine minimum for the
  property — a read must answer a **page** and not a history, and a global read must have a page
  after it. But nothing bounds it, and `StreamPage` and `MaxRead` are legal to 4 096
  (`event/bounds.go:14`). The identical cost argument is written down one file over and not
  applied here.
- **Why this severity:** a store publishing `StreamPage` 4 096 and `MaxBatch` 1 — both legal,
  both within the resident product at a small payload — is asked for 4 097 appends inside one
  `sectionWindow`. At a 5 ms network append that is 20 s, the whole window, and the section
  reports *failed* with *"appending 1 change(s) at version 0 answered context deadline
  exceeded"* — the suite's own volume wearing the store's name, which is GAP-18's shape at a
  different door. I measured the in-memory cost at `StreamPage` 4 096: 0.01 s, so the tree's own
  stores never meet it.
- **Why this timing:** **deferred**, and the argument is the finding's own: the remedy already
  exists and is the store's (`Factory.Window`), the count is derived rather than sampled, and
  narrowing it through a decorator is impossible here because the property under test is what the
  **store's own** slice does. What is missing is that nobody is told.
- **Close criteria:**
  - [ ] the derivation at `sections_ownership.go:28` states the cost the way
        `widestNarrowing`'s does, and names `Factory.Window` as the store's remedy
  - [ ] or the section refuses with a message naming `Factory.Window` when its own writes are
        what ran out of the window, rather than reporting the append
  - [ ] the plan's § S5 records the cost, so phase 2 reads it before meeting it
- **Status: deferred** — the derivation and the plan's `## Debt` state the cost; the refusal that names `Factory.Window` is what is left

### GAP-28 [low][immediate] `probe.store` checks half of the obligation `Factory.New` declares, and the plan says the whole of it is checked

- **Where:** `event/eventtest/probe.go:76-78` compares `store.Limits()`. The doc it enforces is
  `event/eventtest/suite.go:44-46` — *"Every store it builds publishes the same **Limits and the
  same Capabilities**"* — and the plan states at
  `.agents/artifacts/plans/EVENTSOURCE_P1_PLAN.md:2211-2216` that *"`probe.store` compares each
  later store's `Limits()` with the admitted one's and refuses the section when they differ, so
  the obligation is checked rather than assumed."* The capabilities half is assumed.
- **Why this severity:** low, because no in-tree factory does it and the half that is checked is
  the half every count derives from. The failure it leaves open: a factory whose later store
  answers `Transactions: Unsupported` after the door saw `Supported` — `stage`
  (`sections_lifecycle.go:116`), `lateWriter` (`sections_read.go:273`) and `closedWithin` all
  branch on `this.capabilities`, the door's copy, and call `factory.Begin` on a value that has no
  transactions. Whatever happens next is reported as the store's, and no message names the
  mismatch.
- **Why this timing:** one comparison beside the one already there, and the plan's sentence
  currently claims more than the code does — a record S6 consolidates into `docs/`.
- **Close criteria:**
  - [ ] `probe.store` compares `Capabilities()` as it compares `Limits()`, with a refusal naming
        both values
  - [ ] the plan's § Contracts sentence says which halves are checked
- **Status: closed** — `probe.store` compares both, and both halves now have a control

### GAP-29 [low][immediate] Two counted numbers in the plan's Architecture metrics no longer match the tree

- **Where:** `.agents/artifacts/plans/EVENTSOURCE_P1_PLAN.md` § Architecture metrics → Per file,
  the `sections_*.go` row (*"399 / 354 / 331 / 251 / **235** actual"*) and § Breaches, last row
  (*"four functions of **51–61** lines"*).
- **What:** `wc -l` gives `sections_ownership.go` **238**, not 235; `go/ast` gives the largest
  function as `payloadOwnershipSection` at **62** lines, not 61. Both drifted by round 2's own
  edits to that file (GAP-21's proper-prefix rewrite). The other eleven per-file numbers and every
  other counted figure — 0 parameters over four, 7 `probe` fields, 52 methods, 6 exported
  symbols, 399 largest file, 86.1 % coverage — match exactly.
- **Why this severity:** cosmetic against the code; the Breaches table is the record a later agent
  reads instead of counting, and this is round 2's own GAP-22 class arriving one round later.
- **Why this timing:** S6 consolidates these numbers into `docs/`.
- **Close criteria:**
  - [ ] the per-file row reads **238** and the Breaches row reads **51–62**, both recounted
        rather than remembered
- **Status: closed** — recounted

### GAP-30 [low][deferred] `Factory.Tail`'s answer is trusted, so a hook that answers the wrong cursor is reported as the store's defect

- **Where:** `event/eventtest/probe.go:192-195` — `if this.factory.Tail != nil { return
  this.factory.Tail(this.t, store) }`, with no check on what comes back.
- **What:** the hook takes `s event.Store` and must answer a cursor **that store's** `ReadAll`
  accepts. A factory that answers one minted over a different value's backing — the shape
  `busyFactory` (`fixtures_test.go:795-801`) has to reach through a decorator to get right — makes
  the section's first `readAll` answer `ErrCursor`, and `probe.readAll` refuses with *"reading the
  log answered event: this cursor was not minted over this backing"*, which names the store. A
  hook answering `""` degrades silently to the full scan the hook exists to avoid, and nothing
  says so.
- **Why this severity:** low — it is a factory defect, not a store one, and no in-tree factory has
  it. But `Factory.Tail` is a hook phase 2 must implement, and it is the one hook whose wrong
  answer is invisible.
- **Why this timing:** deferred; no contract changes, and the check is local to `probe.tail`.
- **Close criteria:**
  - [ ] `tail` refuses naming `Factory.Tail` when the cursor it was handed is not one this store
        will read from, or when it is `""` while the log is not empty
  - [ ] `Factory.Tail`'s doc states that the answer must be a cursor **this** store value minted
- **Status: deferred** — in the plan's `## Debt`

### GAP-31 [low][deferred] The suite's own key-width reserve is enforced at one door and bypassed at seven

- **Where:** `event/eventtest/probe.go:104-109` (`account` refuses a label over `widestLabel`)
  and `event/eventtest/suite.go:188-197` (`reserve`, which sizes the run identity against
  `Limits().MaxKey` on the assumption that every label obeys it). The seven identities built
  directly, bypassing it: `sections_write.go:87, 88, 92, 93, 113, 117, 141` and
  `sections_read.go:321`, `sections_lifecycle.go:83, 148`.
- **What:** nothing is wrong today — I ran the whole suite at `MaxKey` 21, one byte above the
  suite's own floor, and it certified the same twenty verdicts, because the widest direct suffix
  renders to 6 bytes against a reserve of 8. The trap is the next case added there: a direct
  identity whose rendered suffix exceeds 8 bytes overflows `MaxKey` on a narrow store only, and
  the section refuses with *"the identity does not render a legal stream key"* — the suite's own
  name reported as the store's defect, which is GAP-17's exact failure mode returning through the
  one door its fix did not close.
- **Why this severity:** low — no wrong verdict today, and the floor is demonstrated.
- **Why this timing:** deferred; module-internal, no contract impact.
- **Close criteria:**
  - [ ] the direct constructions go through a `probe` method that applies the same reserve, or
        one assertion over every identity a section mints checks the rendered key against
        `Limits().MaxKey` before it is used
- **Status: deferred** — in the plan's `## Debt`

### GAP-32 [low][deferred] One published number with two spellings, and a value object read one field deep

- **Where:** (a) `event/eventtest/probe.go:161` — `drain` reads `store.Limits().StreamPage` where
  every other site in the package reads `this.limits`, which is the same number admitted once at
  the door (`suite.go:159`). (b) `event/eventtest/sections_lifecycle.go:264-271` — the `fixture`
  value has six fields, and `injectedAtRead` (`:347-354`) uses **one** of them.
- **What:** (a) is `architecture.md`'s single-format rule at its smallest scale: two readings of
  one quantity that `probe.store` keeps equal only for stores the factory built, so a `drain`
  handed a decorator would compare a store's page against the decorator's. (b) is the
  god-blob signal `architecture.md` names — *"a function takes a context object from which it
  uses 2 fields while knowing 20"* — arriving as the side effect of round 1's parameter fix, at a
  scale of six rather than twenty.
- **Why this severity:** low on both; neither can produce a wrong verdict, and (b) is the honest
  price of taking four functions from seven parameters to two.
- **Why this timing:** deferred; module-internal, no contract impact.
- **Close criteria:**
  - [ ] `drain` reads `this.limits.StreamPage`, or the reason it reads the store's is written
        beside it
  - [ ] `injectedAtRead` takes the log rather than the fixture, or the two doors take one field
        each
- **Status: deferred** — in the plan's `## Debt`

### GAP-33 [low][deferred] Two unsynchronised reads of `stagingTx.done` in the fixture, which `-race` cannot reach

- **Where:** `event/eventtest/fixtures_test.go:377` (`stagingStore.Transaction` reads `tx.done`
  outside `held.mutex`) and `:451` (`ready` reads it likewise). `finish` writes it **under** that
  mutex (`:461-473`).
- **What:** a data race in the transaction-capable fixture, invisible to `-race` because no
  section commits or rolls back a transaction while another goroutine issues a statement on the
  same store — `concurrencySection` binds no transaction, so `inside(ctx)` answers nil and the
  read is not reached. It is the shape the reviewer brief asks for: shared state read outside the
  lock that writes it.
- **Why this severity:** low, and it is a fixture rather than shipped code. It becomes real the
  first time a case runs a transaction concurrently with anything, which is the obvious next
  `transactions` case.
- **Why this timing:** deferred; the fixture is a test file and no contract turns on it.
- **Close criteria:**
  - [ ] `done` is read under `held.mutex` at both sites, or it becomes an `atomic.Bool`
  - [ ] `persistentFactory`'s captured `shared` (`fixtures_test.go:756-766`) is read and written
        under something, or the doc says `New` is called from one goroutine
- **Status: first half closed** (`stagingTx.done` is an `atomic.Bool` with a `CompareAndSwap` transition); the second half is in the plan's `## Debt`

---

### What is genuinely clean this round, with the numbers

- **Microkernel.** `grep -rn "eventmemory\|eventtest" event/*.go` over non-test files: **0**.
  `go list -deps ./event/eventtest | grep -c eventmemory`: **0**. `go list -deps` of the package
  is `crud, errs, event, eventtest, utils` and nothing else. The extension point declares a
  contract (`Factory`'s six fields, each with its own paragraph; `Tx`'s four rules), a
  registration mechanism (the store's own package calls `Run`), a failure policy (`admit` fatals
  on a claimed capability with a missing hook and on an `Unstated` one; `needs` reports *not
  certified*; a store's panic is not recovered) and a compatibility note (`event/store.go`).
  Adding `eventpg` costs **zero** diffs to `event/` and — demonstrated by running the suite
  against a hand-built store of that shape — **zero** to `event/eventtest/`.
- **Contract conformance.** `go doc -all` lists the plan's six symbols with the plan's
  signatures and nothing else. `Defect`, `Verdict`, `Certify`, `SectionNames`, `Certified`,
  `Missing` and the three `*Answers` functions are all in `export_test.go`, on nothing's surface.
- **The three words.** `t.Skip` in the suite's own files: **0**. All twenty inventory names are
  in `inventory.go:17-38` verbatim and in the plan's order; `TestEverySectionInTheInventoryWasReported`
  runs with a real shortened-inventory control.
- **Anti-vacuity.** All five rules execute: `TestEverySectionFailsAgainstAStoreThatRefusesEverything`
  computes rule 4 over all twenty; `TestAClaimedCapabilityWithAMissingHookIsRefused` drives both
  doors of rules 1 and 3; `TestARunThatCertifiedNothingFails` has a non-zero control;
  `TestTheSuiteStillDetectsEveryDefectItWasBuiltToDetect` runs **15** defects each with the
  undecorated store as its control; `word`'s zero value is `unreported` and `Run` `t.Error`s it.
- **Universality.** I audited every string, numeric and derived literal in the fourteen non-test
  files. `widestNarrowing = 4`, `identityBytes = 16`, `narrowestRunName = 4`, `widestLabel = 8`
  and `sectionWindow = 20s` each carry a derivation beside them and the last is overridable by
  the factory; every other count is `Limits()`-derived or is the suite's own arithmetic
  (amounts, `writers = 8`, `dense = 1..5` for the five events two batches decide). Nothing is
  fitted to a store the tree contains — measured by running the suite at four other legal limit
  sets and against two stores the tree does not have.
- **Purity.** `event/repo.go:197-218` rehydrates with a `ReadStream` loop, a page check and a
  fold; `grep -rn "time.Now\|rand\.\|os\.\|net/http\|log.Print" event/*.go` over non-test files
  is **empty**. The suite's only randomness is one 16-byte draw at run start with the collision
  argument beside it; its only clock read is `time.Now().Add(-time.Minute)` to build an expired
  context, which is deterministic in effect.
- **Error discipline.** Every comparison is `errors.Is` against an exported sentinel. The two
  `Error()` comparisons (`sections_lifecycle.go:185, 331`) assert the **opposite** — that a
  store's or a decorator's own text does not travel — which is the right use, and both are
  equalities against a sentinel's own rendering rather than substrings. No SQL, no payload bytes
  and no `§UC-`/`§INV-` citation in any shipped string.
- **Concurrency.** `go test -race -count=1 ./event/...` green twice, no flake. Reading for what
  `-race` did not hit: `held.streams`, `held.global`, `held.position` and `held.live` are written
  under `held.mutex`; `remember`/`forget` are reached only from paths that hold it; `handOut`
  clones with a full-slice expression; `deadlines` and `armed` carry their own mutexes;
  `concurrencySection`'s eight goroutines write disjoint indices behind a `WaitGroup` and a gate
  and are joined before anything reads. The two unlocked reads I did find are GAP-33.
- **No global mutable state, no import cycles, no `helpers`/`utils`/`common`/`misc` file**, no
  `TODO`, no `nolint`, no fixture path referenced from a non-test file.
