# EVENTSOURCE_P1 — S4 (the caller seam: binding, repository, token, receipt, reader) — TEST GAPS

## Round 1 — econv test reviewer (clean context) — 2026-09-07

**What was graded.** The eight test files this section names —
`event/repo_test.go`, `binding_test.go`, `transaction_test.go`, `reader_test.go`,
`outcomes_test.go`, `status_test.go`, `recordingstore_test.go` (the fixture) and
`replay_test.go` — 2 158 test lines, 24 tests, 1 fuzz target, 25 subtests, against
the 567 implementation lines of `event/{repo,binding,marker,token,reader}.go`.
Read alongside: the plan's §S4, §Coverage matrix, §Carried gaps C4/C5/C10 and
§Architecture metrics; `.agents/artifacts/usecases/EVENTSOURCE_P1_USECASES.md`
§UC-002/006/007/009…021/024/025/027/028/030…036/046/049/051/055/057…062/064/066/067
and §INV-006/007/008/015/018/019/020/022/027/031/038/039/040/041/043/044/045;
`.agents/artifacts/gaps/EVENTSOURCE_P1_S4_GAPS.md`; `CLAUDE.md`; and
`~/.claude/skills/econv/references/{gaps,universality,architecture,building-blocks,data-integrity,microkernel,restrictions,readability}.md`.

**Runs, all in this worktree.**

```
go build ./...                                        EXIT=0
go vet ./event/...                                    EXIT=0
gofmt -l event                                        silent
go test -race -count=1 ./event/...                    ok event 1.266s · eventmemory 1.028s
go test -race -count=3 ./event/...                    ok event 1.726s · eventmemory 1.061s
go test -race -count=1 -shuffle=on ./event/ (x5)      ok 1.272 / 1.268 / 1.271 / 1.267 / 1.270s
go test -list '^(…the section's twenty-one…)$' | grep -c '^Test'   21   COUNT OK
go test -v -run '^(…those twenty-one plus the three uncounted…)$'  25 subtests, all PASS
go test -run '^$' -fuzz FuzzALoadFoldsAStoredStreamOrRefusesItWhole -fuzztime 25s
                                                      2 798 786 execs, PASS, no corpus written
go test -covermode=atomic -coverprofile ./event/      96.3 % of statements;
    binding.go / marker.go / token.go / reader.go 100 % per function,
    repo.go 100 % except streamOf 83.3 %
```

The section's own phase-5 checkpoint block reproduces exactly. No flake, no order
dependence, no leftover file: `sha256` of every file under `event/` is identical
before and after the whole mutation campaign and the fuzz run.

**Where this section is strong, said first because it changes what the findings
mean.** This is the most mutation-resistant suite in the phase so far. Thirty-nine
deliberate breakages were applied one at a time and **thirty-three died** — every
one of the section's own seventeen claimed mutations that I re-ran turned red
again, and so did fourteen I invented. The controls are the right kind almost
everywhere: `TestAppendRefusesInItsStatedOrder` breaks the step it names *and
every step after it*, so the sentinel it asserts is the fixed order's rather than
the shortest path's; `TestAnAppendWhoseActualBytesExceedTheResidentCeilingIsRefused`
asserts its own arithmetic before the rows run, so a batch that is not actually
over the ceiling fails the test instead of passing it;
`TestAStoreWhoseProductExceedsTheResidentCeilingIsRefused` chooses counts well
inside `MaxPageCount` so the refusal is provably the product's;
`TestRepoAuthorityIsTheComparisonTwoSubsystemsUse` drives two *live* transactions
so a store answering a constant fails; `TestWithinComposesForTwoBackings` pins the
mismatch as its control, without which "both proceed" would pass against a kernel
that never compares. `recordingStore` is a **fake, not a mock** — real
expected-version admission, real paging, real authority minting, a per-method call
count — and it is the only double in the section; `event` cannot import
`event/eventmemory` without a cycle, so a hand-written store here is structural
rather than a choice. Every refusal compares with `errors.Is` against an exported
sentinel; every message states the consequence in plain words; there is not one
`t.Skip`, `t.Log`, loosened assertion or `is not nil`-only check in the section.

The six survivors below are what is left.

**Dispositions of round 1 — 2026-09-07.** Six closed, two carried. Every status
line below says which, and the closure pass is written up at the end of this
file.

---

### GAP-1 [high][immediate] `Reader.Next`'s fail-safe default is asserted by nothing, and a one-token mutation walks through the suite
- **Where:** `event/reader.go:51` (`return false, refuseRead(err)`); the tests that
  would have to cover it are `event/reader_test.go:13-97`
  (`TestAConsumerReadsThroughPagesTheStorePublished`) and
  `event/outcomes_test.go:61-73`
  (`TestAnOutcomeOutsideTheVocabularyTakesTheFailSafeDefault`).
- **What:** The mutation `refuseRead(err)` → `refuseAppend(err)` at `reader.go:51`
  leaves `go test -race ./event/...` **green**. There are two read doors in this
  section's code — `Repo.replay` (`repo.go:203`) and `Reader.Next`
  (`reader.go:51`) — and only the first one is driven with a failure the store did
  not classify. `outcomes_test.go` reaches the read door exclusively through
  `repo.Load` (`store.failRead`, which only `ReadStream` consults); the fixture has
  no injection point on `ReadAll` at all, and `reader_test.go` drives `Next` with
  two page-shape defects and never with a store error. The only error `Next` is
  ever asserted on in the whole tree is `BadCursor` (`status_test.go:81-88`), which
  is a *classified* outcome and maps identically at both doors — so it cannot tell
  the two doors apart.
- **Why this severity:** §UC-060 and §INV-045 make the door's choice of default the
  one thing the kernel decides about a store failure: *from a read it is a backend
  failure, because a read wrote nothing there is anything to be uncertain about.*
  With the mutation in place, a projector draining a log whose driver answers an
  unclassified "connection reset" receives `ErrUncertain` — "the append was issued
  and its outcome was never confirmed" — from a call that wrote nothing, and loses
  the `crud.ErrUnavailable` wrap `backendRefusal` attaches to a retryable cause
  (`errors.go:249-255`). A consumer whose handler treats `ErrUncertain` as
  *a write may have landed: stop, reconcile, alert* takes a write-side recovery
  action on a pure read failure, and a retry loop keyed on retryability stops
  retrying a transient one. This is a missing test for a stated invariant on an
  already-shipped public API, in this section's own file.
- **Why this timing:** No later section can close it. S5's suite reaches store
  failures only through `Factory.Fail(t, s, outcome event.Outcome)` (plan §2140),
  whose parameter is an `Outcome` — an error the store classified as **nothing** is
  not expressible through that hook, so `store failure classification` will not
  drive this branch either. The section is marked `[x]` and its stated delivery is
  "every refusal a caller can reach"; one of the two read doors is not covered by
  this section and cannot be covered by the next.
- **Close criteria:**
  - [x] `recordingStore` gains an injection point on `ReadAll` (the shape of
        `failRead`), and a case asserts `reader.Next` on an unclassified store error
        is `ErrBackend` and **not** `ErrUncertain`, with the append door beside it as
        the control — the pairing `outcomes_test.go:61-64` already uses for `Load`.
  - [x] `refuseRead` → `refuseAppend` at `event/reader.go:51` turns that case, and
        only that case, red; restored, the suite is green.
  - [x] The retryable half is asserted: an unclassified retryable cause through
        `Next` satisfies `errors.Is(err, crud.ErrUnavailable)`.
- **Status:** closed. `recordingStore` gained `failWhole`, the `ReadAll` twin of `failRead`, and `TestAnOutcomeOutsideTheVocabularyTakesTheFailSafeDefault` now drives the second read door beside the append rows that are its control. `refuseRead` → `refuseAppend` at `event/reader.go:51` turns that case red and nothing else (`ErrUncertain` where `ErrBackend` was expected); `backendRefusal` never promoting a retryable cause turns the second row red. Restored, the suite is green.

### GAP-2 [high][deferred] §UC-055's `Bind` cost has no test, and the matrix names one that asserts nothing about it
- **Where:** plan §Coverage matrix line 4182 (`UC-055 … | S3 + S4 (Bind is O(1)) |
  S4 | TestAStoreIsBuiltPerRequestOverALogThatOutlivesIt, TestBothDoorsCheckTheStore`);
  `event/binding_test.go:13-83`; `event/binding.go:30-46`.
- **What:** §UC-055 states what a bind may cost in a sentence written to be held
  to — *§D.4's four constant-time checks and one allocation; **no codec walk**,
  because an O(retained readers) codec interrogation is the work a per-request
  store can least afford* — and adds three *must not happens*: a per-request store
  must not re-seal, must not mutate the declaration, must not take a lock on state
  shared between requests. `TestBothDoorsCheckTheStore`, the test the matrix names
  as the proof, asserts only that seventeen dishonest stores are refused at both
  doors. Nothing anywhere counts a codec call, a fold-table read or an allocation
  across `Bind`. `TestOneLoadAndOneAppendMakeExactlyTheStoreCallsTheContractNames`
  comes closest — `Limits` and `Capabilities` stay at zero after `Bind` — but that
  is about the *retained* limits, not about what `Bind` itself did to the
  declaration.
- **Why this severity:** A missing test for a use case this section claims to
  cover. Reintroducing exactly the walk GAP-35 deleted — iterating every declared
  fact's codec chain in `Bind` to pre-validate it — leaves every test in the phase
  green while making the per-request store of the delivered tenancy design pay an
  O(facts × revisions) interrogation on every request. The claim is cheaply
  falsifiable (a codec that counts `CanEncode`/`Encode`/`Decode` calls, asserted at
  zero across `Bind`), so its absence is a gap and not a limit.
- **Why this timing:** No contract changes and nothing later bends around it; the
  test can be added at any point. Noted for S6: the matrix's third column is copied
  verbatim into `docs/ai/flows/FL-036`, where `scripts/docs_test.go:49` checks only
  that a cited test *exists* — so this row will be baked into a flow doc as a proof
  that the named test does not actually give.
- **Close criteria:**
  - [x] A case binds an aggregate whose facts carry counting codecs and asserts
        `Bind` made zero codec calls, and that the declaration's seal state and fold
        table are unchanged by it.
  - [x] Inserting a codec walk into `Bind` turns that case red.
  - [x] The matrix row for UC-055 names that test rather than
        `TestBothDoorsCheckTheStore`.
- **Status:** closed rather than carried — see the disagreement below. `TestABindInterrogatesNoCodecAndMutatesNoDeclaration` binds a sealed declaration whose two facts carry three counting codecs three times over three per-request stores and asserts zero codec calls, an unchanged seal and an unchanged fold table; a decision through the same fact is the control that the counter counts. A fact-table walk inserted into `Bind` turns it red at 6 calls; `Bind` writing one entry into the fold table turns the second half red. The matrix row for UC-055 now names it.

### GAP-3 [medium][deferred] The recorded-payload cap has no at-the-bound control, and the write side does
- **Where:** `event/repo.go:265-267` (`apply`); `event/replay_test.go:255-256, 276-285`;
  compare `event/repo_test.go:467-473`, which *does* carry the write-side control.
- **What:** Mutating `len(envelope.Payload) > this.limits.MaxPayload` to `>=` leaves
  the suite green. Every stored-payload row uses a payload far over the bound
  (`oversized`, `bound + 12` bytes at a bound of 64), and no case loads a record of
  **exactly** `MaxPayload` bytes. The symmetric write-side check has precisely that
  control — "a payload of exactly the bound was refused with %v, so the case above
  passes against an append that refuses every payload".
- **Why this severity:** The two checks bound the same number from the two sides,
  and only one of them can be falsified. With the mutation, a payload of exactly
  `MaxPayload` bytes is **accepted at `Append`** (the existing control proves that
  path still works) and then refused with `ErrPayload` on every subsequent `Load`,
  forever: a stream that was written legally can never be read again. That is a
  history-class refusal a caller cannot discharge, on data the framework itself
  admitted — the exact shape §UC-017 exists to classify, reached by an off-by-one
  nobody would see.
- **Why this timing:** One row in an existing table; no contract impact; the
  read-path bound is not something a later section re-derives.
- **Close criteria:**
  - [x] `TestEveryHistoryClassRefusalIsRaisedByTheThingItNames` gains a control
        record whose payload is exactly `store.limits.MaxPayload` bytes and folds.
  - [x] `>` → `>=` at `event/repo.go:265` turns that control red.
- **Status:** closed rather than carried. `TestEveryHistoryClassRefusalIsRaisedByTheThingItNames` gained *a recorded payload of exactly the bound is read* — a 64-byte `creditedV2` record whose own length is asserted before the row runs, folding to balance 5 at version 1. `>` → `>=` at `event/repo.go:265` turns exactly that control red.

### GAP-4 [medium][deferred] Four of the five kernel ceilings are tested from one side only
- **Where:** `event/binding_test.go:24-28` (the ceiling rows) against
  `event/binding.go:90-102` (the `admitLimits` table).
- **What:** Three mutations survive the suite:
  `{"MaxPayload", limits.MaxPayload, MaxPayloadBytes}` → `MaxPayloadBytes - 1`;
  the same for `MaxKey`/`MaxKeyBytes`; the same for `StreamPage`/`MaxPageCount`.
  `TestBothDoorsCheckTheStore` drives `ceiling + 1` for all five bounds and asserts
  `ErrWrongStore`, but no store in the suite publishes a bound **equal** to its
  ceiling and is admitted. `MaxBatch` is the accidental exception — the byte-bound
  test sets `store.limits.MaxBatch = MaxBatchCount` and needs it admitted
  (`repo_test.go:479`), which is why a blanket `>` → `>=` over the whole table is
  caught and a per-row drift is not.
- **Why this severity:** `bounds.go`'s own comment says a deployment reads these
  ceilings to choose its numbers. A store that legally publishes
  `MaxPayload = MaxPayloadBytes` is then refused at `Bind` **and** at `Read` with
  `ErrWrongStore`, taking the whole composition root down at start-up, and every
  test in the phase stays green. One-sided boundary coverage on the numbers a store
  author is told to copy.
- **Why this timing:** Four rows in an existing table; the ceilings are frozen by
  S1 and nothing later depends on the control existing.
- **Close criteria:**
  - [x] `TestBothDoorsCheckTheStore` gains an at-the-ceiling control per bound — a
        store whose `MaxPayload`, `MaxKey`, `StreamPage` and `MaxRead` are exactly
        their ceilings (with the payload bound chosen so the resident product still
        admits the page) is bound and read.
  - [x] Lowering any one ceiling by one in `admitLimits` turns exactly that control
        red.
- **Status:** closed rather than carried. `TestBothDoorsCheckTheStore` gained a five-row at-the-ceiling table — a store publishing exactly `MaxPayloadBytes`, `MaxBatchCount`, `MaxKeyBytes` or `MaxPageCount` is bound **and** read, with the two page rows paired with a payload bound (`MaxResidentBytes / MaxPageCount`) that still admits the page, so the row is about the ceiling and not the product. Each of the five `admitLimits` ceilings lowered by one in turn turns exactly its own row red.

### GAP-5 [medium][deferred] The whole caller seam is proven against one hand-written fixture and no real store
- **Where:** every test in the section, via `newRecordingStore`
  (`event/recordingstore_test.go:43-62`); `event/eventmemory/*_test.go` (which never
  mentions `event.Bind`, `event.Open`, `event.Read` or `Repo`).
- **What:** `grep -rn "event.Bind\|event.Open\|event.Read(\|event.ReadOnly"` over the
  whole repository returns nothing outside `package event` itself. `Repo.Load`,
  `Repo.Append`, `Repo.Within`, `Repo.Authority`, `Bind` and `Read` have never been
  run against `event/eventmemory`, the one complete real implementation that ships
  in this phase — only against the fixture written in the same package as the code
  under test.
- **Why this severity:** Every S4 assertion about paging, admission, transaction
  answers and outcome classification rests on one author's reading of the store
  contract encoded twice — once in `repo.go` and once in `recordingStore`. Where the
  two agree wrongly, the suite is green and the real store disagrees at runtime:
  `recordingStore.ReadStream` returns a short page by construction at end of
  stream (`recordingstore_test.go:115-117`), which is exactly the condition
  `replay`'s termination rule depends on (`repo.go:214`), and no test has ever
  observed `eventmemory` doing the same through the repository.
- **Why this timing:** The plan assigns the crossing to S5 — the `binding`,
  `stream paging` and `expected version` conformance sections run over
  `eventmemory` through `event/eventmemory/conformance_test.go`. Recorded here so
  it is not lost if those sections end up driving the `Store` surface directly
  rather than the repository.
- **Close criteria:**
  - [ ] At least one S5 section drives `event.Open` → `Bind` → `Load` → `Append` →
        `Read` over `eventmemory` and is reported *passed*.
  - [ ] Deleting `Repo.checkPage`'s length check turns an S5 section red as well as
        `TestAnOverLongPageIsRefused`, proving the seam is exercised over the real
        store and not only over the fixture.
- **Status:** deferred, carried into the plan's `## Debt` under *From the S4 test review, round 1*, with the close criteria intact and S5 named as the owner.

### GAP-6 [medium][deferred] `*Repo` is documented safe for many goroutines and nothing exercises it
- **Where:** `event/repo.go:8-16` ("copy the pointer, share it between every request
  goroutine, and it keeps nothing between two calls"); §INV-038's `*Repo[S, ID]`
  row; `event/concurrency_test.go:13-77`, which covers the value types and not the
  handles.
- **What:** Adding a mutable field to `Repo` and writing it in `Load`
  (`lastVersion Version`; `this.lastVersion = version`) leaves
  `go test -race -count=1 ./event/...` green. No test in the section calls anything
  on one `*Repo` from two goroutines, so `-race` has nothing to observe.
  `TestTheKernelsValuesAnswerTheSameFromManyGoroutines` reads `Backing`,
  `Authority`, `Envelope`, `Stream`, `Compose` and the refusal mappers — none of the
  handle rows.
- **Why this severity:** §INV-038's own *falsified by* clause names the missing
  test: "a `-race` test with many goroutines loading and appending across several
  aggregates through one repository". The row it certifies is the one a consumer
  most relies on — a process-lifetime repository shared by every request — and the
  first memo anyone adds to `Repo` (a last-version cache, a per-stream fold memo)
  is a data race between two requests that this suite cannot see.
- **Why this timing:** The plan assigns it to S5's `concurrency` section, described
  as "many goroutines, one repository, `-race` clean" (plan line 4587), and S5 is
  not written. Carried so the assignment is checked rather than assumed.
- **Close criteria:**
  - [ ] S5's `concurrency` section drives one `*Repo` from many goroutines under
        `-race`, loading and appending across more than one stream.
  - [ ] Adding an unsynchronised field written by `Repo.Load` turns that section red.
- **Status:** deferred, carried into the plan's `## Debt` under *From the S4 test review, round 1*, with the close criteria intact and S5 named as the owner.

### GAP-7 [low][deferred] Three of the section's tests, including the only `Reader` test, sit outside its count clause
- **Where:** plan §S4 "Checkpoint S4 — phase 5" (the twenty-one-name `-list`
  clause) against `event/binding_test.go:132` (`TestABindWithNothingToBindIsRefused`),
  `:142` (`TestOneFamilyNamesOneAggregate`) and `event/reader_test.go:13`
  (`TestAConsumerReadsThroughPagesTheStorePublished`).
- **What:** The section's own table lists thirteen phase-4 tests, but the count
  clause enumerates twenty-one names and omits these three. `Reader` — `Read`,
  `Next`, `Events`, `Cursor`, `checkPage`, four of the five public symbols
  `reader.go` adds — is exercised by exactly one test, and deleting
  `reader_test.go` leaves the checkpoint command green and drops
  `Reader.checkPage`'s two branches to zero coverage.
- **Why this severity:** Bookkeeping; the tests exist and pass today. It is the
  count clause's job to make a deleted or renamed test visible, and for these three
  it does not.
- **Why this timing:** A plan edit and a checkpoint rerun; nothing depends on it.
- **Close criteria:**
  - [x] The S4 phase-5 `-list` clause names twenty-four tests and the count is
        asserted at 24.
- **Status:** closed rather than carried. The S4 phase-5 `-list` clause names twenty-six and asserts the count at 26: the twenty-one, the three this finding names, and the two tests written for GAP-2 and GAP-8. The section's *Fourteen tests ship with it* preamble now matches its own table.

### GAP-8 [low][deferred] `streamOf`'s mapper-refusal branch is unreachable from the suite
- **Where:** `event/repo.go:156-165`; coverage reports `streamOf` at 83.3 %, the one
  function in the five S4 files below 100 %.
- **What:** The uncovered branch is `this.aggregate.locate(id)` refusing — a mapper
  that renders a key breaking the kernel's own text rules or exceeding
  `MaxKeyBytes`. Every §UC-051 case in the section renders an over-long key against
  the **store's** `MaxKey` (`status_test.go:61-64`), which `checkKey` refuses one
  line later. Swallowing `locate`'s error is therefore masked for every input the
  suite can build, because a store's `MaxKey` is always at or below the kernel's.
- **Why this severity:** Defence in depth, and the redundancy is deliberate — the
  same class (`ErrKey`) is produced either way, so the observable is a message
  rather than a behaviour. Worth one row so the number is not mistaken for a
  missing branch elsewhere.
- **Why this timing:** No contract impact; closable whenever `Compose` grows a case
  that renders illegal text.
- **Close criteria:**
  - [x] One row drives a mapper that renders a key carrying a control character and
        asserts `Load` answers `ErrKey` naming *the rendered key*, distinguishing it
        from the store-bound refusal.
- **Status:** closed rather than carried. `TestTheRenderedKeyIsCheckedBeforeTheStoresBound` drives a mapper returning `Key(id.tenant)` over a tenant carrying a newline and asserts `ErrKey` naming *the rendered key*, beside the store-bound row asserting `ErrKey` naming *the stream key*, both with zero store calls and an admitted identity as the control. `streamOf` swallowing `locate`'s refusal turns the first row red — the key falls through to `checkKey`, which refuses the zero `Stream`'s empty key — and `checkKey` reading `MaxKeyBytes` turns the second red. `streamOf` is now at 100 %.

---

## Mutation log

Thirty-nine mutations, applied one at a time, `go test -count=1 ./event/` after
each, the file restored from a pre-campaign copy and `sha256`-compared afterwards.
**Thirty-three caught, six survived.** Every file under `event/` is byte-identical
to its pre-campaign copy, and `go build ./...`, `go vet ./event/...` and
`gofmt -l .` are clean at the end.

| # | File:line | Mutation | Verdict | Turned red |
|---|---|---|---|---|
| M1 | repo.go:235 | `checkPage`'s page-length check removed | caught | `TestAnOverLongPageIsRefused` |
| M2 | repo.go:243 | `checkPage`'s version-order check neutered (`if offset < 0`) | caught | `TestAMisPagedStreamIsRefusedBeforeItIsFolded` |
| M3 | repo.go:240 | `checkPage`'s foreign-stream check neutered | caught | `TestAMisPagedStreamIsRefusedBeforeItIsFolded` |
| M4 | repo.go:210 | `replay` hands back the accumulator on a mid-stream failure | **survived** | — (`Load` zeroes; see M4b) |
| M4b | repo.go:38-41 + 210 | the pair: `replay` **and** `Load` hand back what was folded | caught | `TestALoadThatFailsMidStreamReturnsNothing`, `TestEveryHistoryClassRefusalIsRaisedByTheThingItNames`, `FuzzALoadFoldsAStoredStreamOrRefusesItWhole` (11 seeds) |
| M5 | repo.go:68-73 | the empty-append short circuit moved above the key check | caught | `TestAForgedTokenIsRefusedBeforeAnyStatement`, `TestAnEmptyAppendChecksTheKeyAndNothingElse` |
| M6 | repo.go:168 | the key checked against `MaxKeyBytes` rather than the store's `MaxKey` | caught | `TestARequestClassRefusalRendersAClientStatusAndAHistoryClassOneDoesNot` |
| M7 | binding.go:77-80 | `admit` skips `admitCapabilities` | caught | `TestBothDoorsCheckTheStore` |
| M8 | binding.go:73-76 | `admit` skips `admitLimits` | caught | `TestBothDoorsCheckTheStore`, `TestAStoreWhoseProductExceedsTheResidentCeilingIsRefused` |
| M9 | reader.go:29-32 | `Read` ignores `admit`'s refusal | caught | `TestBothDoorsCheckTheStore`, `TestAStoreWhoseProductExceedsTheResidentCeilingIsRefused` |
| M10 | reader.go:19 | `ReadOnly` hands the store back | caught | `TestBothDoorsCheckTheStore` |
| M11 | reader.go:68-70 | `Reader.checkPage`'s length check removed | caught | `TestAConsumerReadsThroughPagesTheStorePublished` |
| M12 | reader.go:72 | `Reader.checkPage`'s ascent check neutered | caught | `TestAConsumerReadsThroughPagesTheStorePublished` |
| M13 | binding.go:111 | the resident-product check made unreachable | caught | `TestBothDoorsCheckTheStore`, `TestAStoreWhoseProductExceedsTheResidentCeilingIsRefused` |
| M14 | marker.go:34-38 | `markerFor` resolves innermost-first whatever its backing | caught | `TestWithinComposesForTwoBackings` |
| M15 | marker.go:28-29 | `withMarker` replaces rather than chains | caught | `TestWithinComposesForTwoBackings` |
| M16 | repo.go:111-113 | `Within` skips the capability | caught | `TestWithinAnswersTheStoresTransactionQuestion` |
| M17 | repo.go:118-120 | `Within` admits an invalid authority | caught | `TestWithinAnswersTheStoresTransactionQuestion` (2 subtests) |
| M18 | repo.go:131 | `Authority` answers the marker instead of the store | caught | `TestRepoAuthorityIsTheComparisonTwoSubsystemsUse` |
| M19 | repo.go:150-152 | `transaction` never compares the marker | caught | `TestRepoAuthorityIsTheComparisonTwoSubsystemsUse`, `TestWithinComposesForTwoBackings` |
| M20 | repo.go:175-177 | `records` drops the batch-count bound | caught | `TestAppendRefusesInItsStatedOrder` |
| M21 | repo.go:181-183 | `records` drops the per-payload bound | caught | `TestAnAppendWhoseActualBytesExceedTheResidentCeilingIsRefused` |
| M22 | repo.go:187-189 | `records` drops the resident-bytes bound | caught | `TestAnAppendWhoseActualBytesExceedTheResidentCeilingIsRefused` |
| M23 | repo.go:89-91 | `Append` drops the backing comparison | caught | `TestAForgedTokenIsRefusedBeforeAnyStatement`, `TestAppendRefusesInItsStatedOrder` |
| M24 | repo.go:79-83 | `Append` ignores a change's carried refusal | caught | `TestAppendRefusesInItsStatedOrder`, `TestAPanickingCodecBecomesTheRefusalItsErrorWouldHaveBeen`, `TestARequestClassRefusalRenders…` |
| M25 | repo.go:74-78 | `Append` drops `decidedFor` | caught | `TestAppendRefusesInItsStatedOrder` |
| M26 | repo.go:72 | the empty receipt forgets the token's version | caught | `TestAnEmptyAppendChecksTheKeyAndNothingElse` |
| M27 | repo.go:92 | the receipt carries the invalid authority | caught | `TestRepoAuthorityIsTheComparisonTwoSubsystemsUse`, `TestWithinAnswersTheStoresTransactionQuestion`, `TestWithinComposesForTwoBackings` |
| M28 | binding.go:52 | `Binding.hold` never refuses | caught | `TestOneFamilyNamesOneAggregate` |
| M29 | repo.go:35-37 | `Load` skips the transaction question | caught | `TestOneLoadAndOneAppend…`, `TestRepoAuthorityIs…`, `TestWithinAnswers…`, `TestWithinComposesForTwoBackings` |
| M30 | binding.go:90 | the `MaxPayload` kernel ceiling drifts by one | **survived** | — (GAP-4) |
| M31 | binding.go:92 | the `MaxKey` kernel ceiling drifts by one | **survived** | — (GAP-4) |
| M32 | binding.go:93 | the `StreamPage` kernel ceiling drifts by one | **survived** | — (GAP-4) |
| M33 | repo.go:258 | the stored type-name bound drifts by one | caught | `TestEveryHistoryClassRefusalIsRaisedByTheThingItNames` |
| M34 | repo.go:265 | the recorded-payload cap becomes `>=` | **survived** | — (GAP-3) |
| M35 | repo.go:184 | the resident bytes become the maximum, not the sum | caught | `TestAnAppendWhoseActualBytesExceedTheResidentCeilingIsRefused` |
| M36 | binding.go:37 | `Bind` does not seal the declaration | caught | `TestTheSealRefusesALateFact` |
| M37 | repo.go:101 | the receipt's first version is off by one | caught | `TestAnEmptyAppendChecksTheKeyAndNothingElse`, `TestOneAppendCarriesTwoIdenticalChanges` |
| M38 | repo.go:185 | a record is written at revision 1 whatever it was decided at | caught | `TestAFreshStreamLoadsAsZero`, `TestAStreamWithHistoryFoldsToItsCurrentState` |
| M39 | reader.go:33 | `Read` ignores the cursor it was given | caught | `TestARequestClassRefusalRendersAClientStatusAndAHistoryClassOneDoesNot` |
| M40 | repo.go:71-73 | no empty-append short circuit at all | caught | `TestAnEmptyAppendChecksTheKeyAndNothingElse` |
| M41 | repo.go:214 | the replay loop stops only on an empty page | caught | `TestOneLoadAndOneAppendMakeExactlyTheStoreCallsTheContractNames` |
| M42 | repo.go:42 | `Load`'s token drops its stream | caught | 30 tests |
| M43 | repo.go:100 | `Append`'s token drops its backing | caught | `TestOneAppendCarriesTwoIdenticalChanges`, `TestOneLoadAndOneAppend…`, `TestRepoAuthorityIs…`, `TestAStreamWithHistory…` |
| P1 | reader.go:56 | `Next` reuses the array it handed to the last caller | caught | `TestAConsumerReadsThroughPagesTheStorePublished` |
| P5 | repo.go:203 | `replay` maps a store failure at the **append** door | caught | `TestAnOutcomeOutsideTheVocabularyTakesTheFailSafeDefault` |
| P6 | reader.go:51 | `Next` maps a store failure at the **append** door | **survived** | — (GAP-1) |
| P9 | repo.go:121 | `Within` marks nothing | caught | `TestRepoAuthorityIs…`, `TestWithinComposesForTwoBackings` |
| P10 | repo.go:112 | a refused `Within` answers a context of its own | caught | `TestWithinAnswersTheStoresTransactionQuestion` |
| P11 | repo.go:72 | an empty receipt counts one | caught | `TestAnEmptyAppendChecksTheKeyAndNothingElse` |
| P12 | repo.go:265-267 | `apply` drops the recorded payload cap | caught | `TestEveryHistoryClassRefusalIsRaisedByTheThingItNames` |
| P13 | repo.go:259 | `apply` renders the stored type name it refuses | caught | `TestEveryHistoryClassRefusalIsRaisedByTheThingItNames` |
| R1 | reader.go:56 | `Next` never advances its cursor | caught | `TestAConsumerReadsThroughPagesTheStorePublished` |
| R2 | reader.go:57 | `Next` always answers *more* | caught, badly | the package **hangs**; only the 10-minute panic timeout ends it |
| R3 | reader.go:60 | `Events` copies the page it hands out | **survived** | benign — §INV-038 blesses either, not a defect |
| C1 | repo.go:11-16, 42 | the repository keeps mutable state between two calls | **survived** under `-race` | — (GAP-6) |

**Notes on the survivors that are not findings.** M4 alone is masked by `Load`'s
own zeroing, which is defence in depth and exactly what the section's phase-5 text
says ("both lines have to go for the property to be falsifiable"); the pair is
caught. R3 is a behaviour §INV-038 permits either way. R2 is caught only by a
timeout: `Reader.Next`'s loop termination is the caller's, and a store that always
answers a full page is an infinite drain by construction — worth knowing, not worth
a row.

## Rubric axes with nothing to report

- **Vacuous passes.** None found. Every table has more than one row; every refusal
  case has a control that fails if the door refuses everything; no assertion is on a
  value the test itself computed from the code under test; no `try`/`recover`
  swallows an assertion (the one `recover` is the subject of
  `TestAFoldPanicUnwindsOutOfLoad` and is asserted on).
- **Behaviour vs internals.** The per-method call counts (`recordingStore.exactly`)
  are assertions on a **declared seam**, not on internals: §INV-007 (never retried),
  §INV-027 (never opens a transaction) and §UC-055 (`Limits` read once) are
  statements about how often the kernel may call a store, so counting them is the
  only way to test them. No private method is patched and no unexported field of the
  code under test is read by an assertion.
- **Doubles discipline.** One fake, no mocks. `event` cannot import
  `event/eventmemory` (cycle), so a hand-written store in-package is structural.
  Its behaviour is real rather than scripted: real expected-version admission, real
  paging, real `NewAuthority`, `bytes.Clone` on the way in. See GAP-5 for the one
  consequence.
- **Universality.** No golden blob, no snapshot, no assertion fitted to one sample.
  The byte-bound cases derive their sizes from `MaxResidentBytes`/`MaxBatchCount` and
  assert the arithmetic before running; the product cases derive from
  `ResidentPage(maxPayload)`; the reader asserts against `store.limits.MaxRead`
  rather than a literal. Three state types are driven through the repository
  (`account` struct, `carrier` with a byte payload, `ledger` as a map), and the
  kernel branches on none of them. Adding a second aggregate needs no new branch in
  any test helper.
- **Determinism.** `-race -count=3` and `-shuffle=on` five times, all green. No
  sleep, no wall-clock assertion, no unseeded randomness, no network, no file left
  behind. The one map iteration (`transaction_test.go:302`) drives two independent
  repositories and its assertions are order-independent.
- **Honesty.** No `t.Skip`, no `t.Log` standing in for an assertion, no widened
  tolerance, no `is not nil`-only check. The section's claimed checkpoint block
  reproduces verbatim, its claimed count is 21, and every claimed mutation I re-ran
  behaved as claimed.
- **Speed and layering.** Unit only, no database: `go test -race ./event/` is
  **1.27 s**; `./event/...` including `eventmemory` is 1.6 s wall.


---

## Round 1 — closure pass — 2026-09-07

**Six closed, two carried.** GAP-1 was the section's one `[immediate]` finding and
is what this pass is answerable for. Five of the seven `[deferred]` ones were
closed rather than carried, which is the one place this pass disagrees with the
review, and the argument is below. GAP-5 and GAP-6 are S5's by the finding's own
assignment and are now rows in the plan's `## Debt`, under *From the S4 test
review, round 1*, with their close criteria intact.

**Where this pass disagrees with the review's timing, and why.** GAP-2, GAP-3,
GAP-4, GAP-7 and GAP-8 are all graded `[deferred]`, and every one of their
*Why this timing* paragraphs says the same thing: *one row in an existing table*,
*four rows in an existing table*, *a plan edit*, *the test can be added at any
point*. None of them names a later section as an owner, because there is no later
section that would naturally open these files — S5 writes `event/eventtest` and
S6 writes documentation and structural checks. A `[deferred]` finding whose owner
is "whoever opens this file next" and whose cost is a table row is a finding that
is never closed. Three of them are also exactly what `CLAUDE.md` demands out
loud: *put a control case next to any test that could pass vacuously*, and
GAP-3 and GAP-4 are one-sided boundary tables whose symmetric halves already have
their controls. So they were closed here, where the files were already open and
where a mutation campaign was already running to prove each one. GAP-5 and GAP-6
were not, because both need something this section does not have — a real store
across a package boundary, and a concurrency harness over one handle — and both
are already scheduled in S5 by name.

**Mutations run in this pass**, one at a time, `go test -count=1 ./event/...`
after each, every file restored from a pre-mutation copy and `diff`ed
byte-identical afterwards:

| # | File:line | Mutation | Turned red |
|---|---|---|---|
| P6 | `reader.go:51` | `Next` maps a store failure at the **append** door | `TestAnOutcomeOutsideTheVocabularyTakesTheFailSafeDefault` — `ErrUncertain` where the read door answers `ErrBackend` |
| P6b | `errors.go:251` | `backendRefusal` never promotes a retryable cause | the same test's retryable row |
| B1 | `binding.go:37` | `Bind` walks the fold table, applying every declared fact | `TestABindInterrogatesNoCodecAndMutatesNoDeclaration` — 6 codec calls across three binds |
| B2 | `binding.go:37` | `Bind` writes one entry into the fold table | the same test's declaration half |
| M34 | `repo.go:265` | the recorded-payload cap becomes `>=` | `TestEveryHistoryClassRefusalIsRaisedByTheThingItNames/a_recorded_payload_of_exactly_the_bound_is_read` |
| M30 | `binding.go:90` | the `MaxPayload` kernel ceiling drifts by one | `TestBothDoorsCheckTheStore`, the payload row |
| M30b | `binding.go:91` | the `MaxBatch` kernel ceiling drifts by one | `TestBothDoorsCheckTheStore`, the batch row (and `TestAnAppendWhoseActualBytesExceedTheResidentCeilingIsRefused`, which already read it) |
| M31 | `binding.go:92` | the `MaxKey` kernel ceiling drifts by one | `TestBothDoorsCheckTheStore`, the key row |
| M32 | `binding.go:93` | the `StreamPage` kernel ceiling drifts by one | `TestBothDoorsCheckTheStore`, the stream-page row |
| M32b | `binding.go:94` | the `MaxRead` kernel ceiling drifts by one | `TestBothDoorsCheckTheStore`, the read row |
| K1 | `repo.go:157-160` | `streamOf` swallows `locate`'s refusal | `TestTheRenderedKeyIsCheckedBeforeTheStoresBound`, the rendered-key row |
| K2 | `repo.go:168` | `checkKey` reads `MaxKeyBytes` rather than the store's `MaxKey` | `TestTheRenderedKeyIsCheckedBeforeTheStoresBound`, the store-bound row, and `TestARequestClassRefusalRendersAClientStatusAndAHistoryClassOneDoesNot` |

**Runs after the pass.**

```
gofmt -l .                                            silent, whole tree
go vet ./event/...                                    clean
go test -race -count=2 ./event/...                    ok event 1.490s · eventmemory 1.043s
go test -race -count=1 -shuffle=on ./event/ (x3)      ok 1.266 / 1.260 / 1.262s
go test -list '^(…the twenty-six…)$' | grep -c '^Test'   26   COUNT OK
go test -race -v -run '^(…the twenty-six…)$'          26 PASS, 0 FAIL
go test -run '^$' -fuzz FuzzALoadFoldsAStoredStreamOrRefusesItWhole -fuzztime 30s
                                                      3 389 819 execs, PASS, no corpus written
go tool cover -func                                   binding.go, marker.go, token.go,
    reader.go and repo.go 100 % per function — streamOf was the one below it at 83.3 %
make unit                                             exit 0, no FAIL
make check                                            all nine arms ok
```

---

## Round 2 — econv test reviewer (clean context, re-audit) — 2026-09-07

**What was graded.** The same eight files, re-read from scratch against the same
sources: the plan's §S4 (now claiming twenty-six tests and thirty-two mutations
across two blocks), §Coverage matrix, §Carried gaps C4/C5/C10, §Debt;
`.agents/artifacts/usecases/EVENTSOURCE_P1_USECASES.md` §UC-036/039/053,
§INV-015/016/022/036/038/043/045; `CLAUDE.md`; and
`~/.claude/skills/econv/references/{gaps,universality,architecture,building-blocks,data-integrity,microkernel,restrictions,readability}.md`.
Round 1's six closure claims were re-mutated rather than believed.

**Runs, all in this worktree.**

```
go build ./...                                        EXIT=0
go vet ./event/...                                    EXIT=0
gofmt -l .                                            silent, whole tree
go test -race -count=1 ./event/...                    ok event 1.258s · eventmemory 1.025s
go test -race -count=3 ./event/...                    ok event 1.723s · eventmemory 1.061s
go test -race -count=1 -shuffle=on ./event/ (x3)      ok 1.261 / 1.263 / 1.267s
go test -list '^(…the twenty-six…)$' | grep -c '^Test'   26   COUNT OK
go test -race -v -run '^(…the twenty-six…)$'          26 PASS, 0 FAIL
go test -run '^$' -fuzz FuzzALoadFolds… -fuzztime 45s  5 077 254 execs, PASS, no corpus written
go tool cover -func                                   binding.go, marker.go, token.go,
    reader.go and repo.go 100 % per function, all 34 functions; package total 96.4 %
```

The section's phase-5 checkpoint block reproduces verbatim. No flake, no order
dependence, no leftover file: every file under `event/` is byte-identical to its
pre-campaign copy and `git status --porcelain event/` is unchanged from the start
of the review. Section suite runtime: **1.26 s**, unit only, no database.

**Round 1's closures are real.** All six were re-mutated and each turned exactly
the test its status line names red, and only it: `refuseRead` → `refuseAppend` at
`reader.go:51` and `backendRefusal` never promoting a retryable cause (GAP-1); a
fold-table walk inserted into `Bind` (GAP-2); `>` → `>=` at `repo.go:265`
(GAP-3); each of the five `admitLimits` ceilings lowered by one in turn (GAP-4);
`streamOf` swallowing `locate`'s refusal and `checkKey` reading `MaxKeyBytes`
(GAP-8); and the twenty-six-name count clause (GAP-7). GAP-5 and GAP-6 were
re-verified **still open** — `grep -rn "event\.Bind\|event\.Open(\|event\.Read(\|\*event\.Repo"`
returns nothing outside `package event`, and a `lastVersion Version` field written
by `Repo.Load` still passes `go test -race ./event/...`. Both are rows in the
plan's `## Debt` with S5 named, and they are restated below unchanged rather than
re-numbered.

**Two of round 1's carried-in items from the S1 review, which the plan's `## Debt`
assigns to S4, are now closed by this section.** GAP-T8 (`Support.stated()` had
no caller and 0 % coverage) — `stated()` is 100 % and `stated() { return this ==
Supported }` turns eight tests red, so `Unstated`/`Unsupported`/`Supported` are
all three driven. GAP-T11's first edge (an identifier of exactly `MaxNameBytes`) —
`checkText`'s `>` → `>=` turns `TestFoldRefusesAnotherInstance`'s at-the-cap row
and the `Compose` fuzz seeds red. GAP-T7 is **partly** closed by the new
`event/bounds_test.go` and its residual is GAP-15 below.

**Twenty-eight new mutations were applied.** Twenty-one died; seven survived, of
which one is benign and six are the findings below. The full log is at the end.

---

### GAP-9 [high][immediate] `Reader.Next` moves its cursor past a page it refuses, and nothing in the tree would notice
- **Where:** `event/reader.go:53-56`; the test that would have to cover it is
  `event/reader_test.go:68-96`, the two page-defect rows of
  `TestAConsumerReadsThroughPagesTheStorePublished`.
- **What:** Hoisting the cursor assignment above the page check —

  ```go
  this.cursor = cursor
  if err := this.checkPage(page); err != nil {
      return false, err
  }
  this.events = page
  ```

  leaves `go test -race ./event/...` **green**. Both defect rows assert only that
  `Next` answers `ErrBackend`; neither reads `reader.Cursor()` afterwards, and
  neither calls `Next` again once the defect is cleared. `Reader.Cursor()` is
  public, is asserted exactly once in the whole tree (`reader_test.go:64`, that a
  *drained* reader's cursor is non-empty), and is never compared before and after
  a refusal.
- **Why this severity:** Demonstrated, not inferred. With the four-event stream the
  existing test already builds, a store whose first page has its positions
  swapped, and the defect cleared after the refusal: the shipped code answers
  `cursor: ""` and the consumer then reads **4 of 4** events; with the hoist it
  answers `cursor: "3"` and the consumer reads **1 of 4** — three committed
  events silently skipped, with no error anywhere after the one it already
  logged. That is the exact sentence `Reader.checkPage`'s own comment claims to
  prevent ("refused before a consumer checkpoints past an event it never saw"),
  §UC-039's *no event may be skipped at a page boundary*, and §INV-036's tiling
  claim. A projector or outbox relay that logs a transient `ErrBackend` and loops
  — the ordinary shape — loses a page per occurrence, and a consumer that
  persists `reader.Cursor()` on the error path persists a checkpoint past events
  it never saw. The refusal half is asserted; the *before* half, which is the
  whole point of refusing rather than returning, is asserted by nothing.
- **Why this timing:** No later section can close it. `eventtest.Factory` (plan
  §2137) offers `New`, `Begin`, `Sibling` and `Fail(t, s, outcome event.Outcome)`
  — an **Outcome** hook and no page-defect hook — so a conforming store in S5's
  `global paging` or `resumption` never produces a mis-paged page and never
  reaches this branch. Only a hand-written defect store inside `package event`
  can, and `recordingStore.whole` already exists. This is the identical argument
  round 1 used for GAP-1, on the same file, the same test and the same public
  reader. The section is `[x]` and its stated delivery is "every refusal a caller
  can reach".
- **Close criteria:**
  - [ ] `TestAConsumerReadsThroughPagesTheStorePublished` captures `reader.Cursor()`
        before each defect row and asserts it is unchanged after the refusal.
  - [ ] One row clears `store.whole` after a refused `Next` and asserts the reader
        goes on to deliver **every** event of the stream exactly once, so the
        assertion is about events reaching a consumer and not only about a string.
  - [ ] Hoisting `this.cursor = cursor` above `this.checkPage(page)` at
        `event/reader.go:53` turns that case, and only that case, red; restored, the
        suite is green.

### GAP-10 [medium][deferred] The resident-bytes ceiling is the one of `records`' three bounds with no at-the-bound control
- **Where:** `event/repo.go:187-189`; `event/repo_test.go:524-563`
  (`TestAnAppendWhoseActualBytesExceedTheResidentCeilingIsRefused`, its second
  subtest); compare `:516-521`, which carries the per-payload bound's control.
- **What:** `if resident > MaxResidentBytes` → `>=` leaves the suite green. The
  subtest asserts its own arithmetic in both directions — that
  `MaxBatchCount × size` is over the ceiling and `(MaxBatchCount-1) × size` is
  not — but `(MaxBatchCount-1) × size` is 67 044 351 against a ceiling of
  67 108 864, so no append in the section ever holds **exactly**
  `MaxResidentBytes` bytes. The two sibling bounds both got their at-the-bound
  control in round 1 (GAP-3 for the read side of `MaxPayload`, GAP-4 for the five
  kernel ceilings); this is the third and it was missed.
- **Why this severity:** `bounds.go`'s own comment says a deployment reads these
  ceilings to choose its numbers. With the mutation, a batch whose measured
  payload sum is exactly the documented ceiling is refused `ErrTooLarge` — a
  request-class refusal telling the caller to send less data when it already sent
  exactly what the framework publishes as the maximum. A caller that sized its
  batch from `MaxResidentBytes` can never write, and every test in the phase stays
  green. One-sided boundary coverage on a number a consumer is told to copy.
- **Why this timing:** One row in an existing table beside two that already have
  their controls; no contract impact; the ceiling is frozen by S1.
- **Close criteria:**
  - [ ] A control batch whose measured payload sum is asserted to be exactly
        `MaxResidentBytes` reaches the store — the arithmetic asserted before the
        row runs, as the two rows beside it already do.
  - [ ] `>` → `>=` at `event/repo.go:187` turns that control, and only it, red.

### GAP-11 [medium][deferred] `Reader.checkPage`'s ascent check has no equal-positions row, and equality is the case its comment is about
- **Where:** `event/reader.go:71-75`; `event/reader_test.go:80-86`, the
  "a page whose positions do not ascend" row.
- **What:** `page[offset].Position <= page[offset-1].Position` → `<` leaves the
  suite green. The only defect row swaps the first and last envelopes of a page,
  which produces a **strict descent**, so nothing distinguishes "does not ascend"
  from "descends". A page carrying two envelopes at one position is admitted
  under the mutation.
- **Why this severity:** The cursor a consumer persists **is** the last position
  of the page (`recordingstore_test.go:149`, and every real store by the same
  rule), so two envelopes sharing one position are two events one cursor cannot
  separate. If the page boundary falls between them, the second is skipped for
  ever: the store returns `Position > from` and the twin at the checkpointed
  position is gone. That is §UC-039's *no event may be skipped at a page
  boundary* and precisely what `checkPage`'s own comment names ("a consumer
  resuming from its cursor cannot tile the log"). The `<=` is the load-bearing
  character and it is untested.
- **Why this timing:** One row in an existing table; no contract impact; the
  ascent rule does not move.
- **Close criteria:**
  - [ ] A third row whose page repeats one position across two envelopes is
        refused with `ErrBackend`, beside the strict-descent row that already
        exists.
  - [ ] `<=` → `<` at `event/reader.go:72` turns that row, and only it, red.

### GAP-12 [medium][deferred] `Append`'s backing comparison could become `==` undetected, and §INV-016 says `==` cannot express what it needs
- **Where:** `event/repo.go:88-91`; `event/repo_test.go:127-167`
  (`TestAForgedTokenIsRefusedBeforeAnyStatement`), which is what §INV-015 names.
- **What:** `if !backing.Equal(at.backing)` → `if backing != at.backing` leaves the
  suite green. `Backing.Equal` is `crud.SameDataSource`, which answers **false**
  for two nil-interface identities, so two invalid backings are unequal; `==` on
  the struct answers **true** for two zero `Backing` values. Every row in the
  section compares a *valid* store backing against a forged token's zero one,
  where the two operators agree. Nothing drives invalid-against-invalid at this
  use site.
- **Why this severity:** §INV-016 states the requirement in those words — "The
  zero `event.Backing{}` holds a nil interface, so it is invalid and two zero
  backings compare **unequal** — the requirement `==` cannot express" — and
  `token.go:3-8` rests §INV-015's whole claim on it: a forged token's "backing is
  invalid and matches nothing, **including another invalid one**". `binding.go:24-29`
  deliberately does *not* retain the backing, "because a remembered backing cannot
  see a store that re-pointed at another database", so a store whose `Backing()`
  goes invalid after `Bind` — a per-request store over a released tenant lease, a
  decorator forwarding to an inner store that was closed — is reachable by
  construction. Under the mutation, such a store accepts a hand-built
  `At[account]{stream: <legal key>, version: N}`, step 5 passes, and the append is
  issued: a value the framework never minted reaches the backing. §INV-016 is
  checkpointed at S1, where the *type*'s comparison is proved; this is the caller
  seam's use of it, which is S4's.
- **Why this timing:** One row in an existing table; the sentinel and the
  behaviour do not change.
- **Close criteria:**
  - [ ] A row binds an honest store, sets its backing to `Backing{}` afterwards,
        and asserts a hand-built token with the zero backing is still
        `ErrWrongStore` with zero `Append` calls.
  - [ ] `!backing.Equal(at.backing)` → `backing != at.backing` at
        `event/repo.go:89` turns that row, and only it, red.

### GAP-13 [medium][deferred] `markerFor` resolves innermost-**among matching backings**, and only the by-backing half is pinned
- **Where:** `event/marker.go:32-40`; `event/transaction_test.go:276-336`
  (`TestWithinComposesForTwoBackings`) and `:243-267` (the crossed-context row).
- **What:** Making `markerFor` return the **outermost** matching marker instead of
  the innermost leaves the suite green. Round 1's M14 ("resolves innermost-first
  whatever its backing") is caught, because two stores each hold one marker and
  the by-backing key is what picks between them. But no context in the section
  ever holds **two markers of one backing**, so which end of a same-backing chain
  wins is decided by nobody's test.
- **Why this severity:** `marker.go:9-16` says a marker "chains on any marker
  already there rather than replacing it", and chaining only matters when there is
  more than one. Two `Within` calls over one store on two different transactions of
  it — an outer unit of work and an inner one, the ordinary nesting a
  savepoint-shaped API produces — leave two markers of one backing. Under the
  mutation the outer wins, so `transaction` compares the inner transaction the
  store reports against the outer authority the marker holds, and a **correct**
  operation is refused `ErrTransactionMismatch` at both `Load` and `Append`, with
  a message that by design names neither transaction. That is the same failure
  mode the comment's own paragraph exists to describe, one nesting level over.
- **Why this timing:** One subtest; `Within` and `markerFor` do not change. Phase 2
  (`eventpg` over `crudsql`, whose savepoints resolve to the parent `*sql.Tx`) is
  where nesting becomes routine, so it is worth pinning before then.
- **Close criteria:**
  - [ ] A case calls `Within` twice over one store on two different transactions of
        it and asserts the `Load` and `Append` inside the inner one proceed and
        carry the inner transaction's authority.
  - [ ] Resolving the chain outermost-first at `event/marker.go:34-38` turns that
        case, and only it, red.

### GAP-14 [low][deferred] §D.5's steps 2 and 3 are ordered by the position of a change in the batch, not by the code
- **Where:** `event/repo.go:74-83`; `event/repo_test.go:89-114`
  (`TestAppendRefusesInItsStatedOrder`), its second row.
- **What:** Merging the two loops into one that checks `decidedFor` and then
  `change.err` per change leaves the suite green. Reversing them (all `change.err`
  before all `decidedFor`) **is** caught, so the coarse order is pinned; the
  separation is not. The row for step 2 is
  `{"…", at, []Change[account]{theirs, unencodable}, ErrWrongStream}` and `theirs`
  is written first, so a per-change loop reaches the wrong-stream refusal before
  it ever sees the carried one.
- **Why this severity:** The section's own table claims each row "breaks the step
  it names **and every step after it**, and asserts the earlier sentinel". For
  this row that is true of the *changes* and not of the *code*: the earlier
  sentinel wins because of argument order, not because step 2 is a complete pass.
  `Append(at, unencodableForThisStream, changeForAnotherStream)` answers
  `ErrEncode` under the merged version and `ErrWrongStream` under the shipped one.
  Both are refusals with zero store calls and both are request-class, so the
  damage is a class of message rather than a write — hence low — but the claim in
  the plan is stronger than what the test gives.
- **Why this timing:** One row in an existing table; no contract impact.
- **Close criteria:**
  - [ ] One row places the carried refusal **first** and the other stream's change
        second, and asserts `ErrWrongStream`.
  - [ ] Merging `repo.go`'s two loops into one per-change loop turns that row, and
        only it, red.

### GAP-15 [medium][deferred] GAP-T7's residual: `MaxKeyBytes` and all four derivation relations are still asserted by nothing, in the section the plan's `## Debt` names as their owner
- **Where:** `event/bounds.go:12` and `event/bounds_test.go`; the plan's `## Debt`,
  *From S1's test review*, GAP-T7 ("**S4** is where the ceilings are first enforced
  and where the relation assertions belong, beside that enforcement").
- **What:** `event/bounds_test.go` (new in this section) closes most of GAP-T7:
  `MaxResidentBytes` is pinned absolutely at `64<<20`, the `ResidentPage` table is
  derived by hand from it, and `MaxBatchCount`/`MaxPageCount` swapped now turns
  `TestAStoreWhoseProductExceedsTheResidentCeilingIsRefused` red. What survives is
  `MaxKeyBytes = 2 << 10` → `2 << 20`: green across the whole tree. None of the
  four relations GAP-T7 names is asserted anywhere —
  `MaxResidentBytes >= MaxPayloadBytes`, `MaxResidentBytes / MaxPayloadBytes >= 64`,
  `MaxKeyBytes + MaxNameBytes + 8 <= 2704`, and
  `MaxBatchCount = MaxResidentBytes / jobs.DefaultPayloadBytes`.
- **Why this severity:** The third relation is the one that costs later: 2704 is a
  PostgreSQL btree index-row limit, and `eventpg` in phase 2 indexes
  `(family, key)`. A `MaxKeyBytes` that drifts above it is a ceiling the kernel
  admits and the phase-2 store cannot index — an insert that fails at runtime on a
  long key, on a bound the kernel told the store author to copy. Every S4 check is
  written in terms of the constant, so the constant itself is invisible to them by
  construction, which is exactly what GAP-T7 predicted and why it named S4.
- **Why this timing:** S4 is marked `[x]` and neither S5 (`event/eventtest`) nor S6
  (docs and structural checks) opens `bounds.go`. A `[deferred]` item whose named
  owner has closed is the shape that gets dropped, so it is restated here against
  the file that now exists.
- **Close criteria:**
  - [ ] `event/bounds_test.go` pins `MaxKeyBytes`, `MaxNameBytes`, `MaxPayloadBytes`,
        `MaxBatchCount` and `MaxPageCount` to stated absolute values with the reason
        for each.
  - [ ] The four derivation relations are asserted as relations, each with the
        consequence in its message.
  - [ ] `MaxKeyBytes` `2<<10` → `2<<20` turns that test red.

### GAP-5 [medium][deferred] The whole caller seam is proven against one hand-written fixture and no real store — **still open, re-verified**
- **Where:** every test in the section, via `newRecordingStore`
  (`event/recordingstore_test.go:44-63`); `event/eventmemory/*_test.go`.
- **What:** `grep -rn "event\.Bind\|event\.Open(\|event\.Read(\|event\.ReadOnly\|\*event\.Repo"`
  over the repository still returns nothing outside `package event`. Unchanged
  since round 1.
- **Why this severity / timing:** As round 1. Carried in the plan's `## Debt` under
  *From the S4 test review, round 1*, with S5 named.
- **Close criteria:** unchanged from round 1.
  - [ ] At least one S5 section drives `event.Open` → `Bind` → `Load` → `Append` →
        `Read` over `eventmemory` and is reported *passed*.
  - [ ] Deleting `Repo.checkPage`'s length check turns an S5 section red as well as
        `TestAnOverLongPageIsRefused`.
- **Status:** open, deferred, owner S5.

### GAP-6 [medium][deferred] `*Repo` is documented safe for many goroutines and nothing exercises it — **still open, re-verified**
- **Where:** `event/repo.go:8-16`; §INV-038's `*Repo[S, ID]` row;
  `event/concurrency_test.go`.
- **What:** Adding `lastVersion Version` to `Repo` and writing it at the end of
  `Load` still leaves `go test -race -count=1 ./event/...` green. Unchanged since
  round 1.
- **Why this severity / timing:** As round 1. Carried in the plan's `## Debt` with
  S5's `concurrency` section named.
- **Close criteria:** unchanged from round 1.
  - [ ] S5's `concurrency` section drives one `*Repo` from many goroutines under
        `-race`, loading and appending across more than one stream.
  - [ ] Adding an unsynchronised field written by `Repo.Load` turns that section red.
- **Status:** open, deferred, owner S5.

---

## Mutation log — round 2

Twenty-eight new mutations plus eleven re-runs of round 1's closure mutations,
applied one at a time, `go test -count=1 ./event/` after each, every file restored
from a pre-mutation copy and `diff`ed byte-identical afterwards. **Thirty-two
caught, seven survived**, one of the seven benign. `git status --porcelain event/`
is identical to its state at the start of the review; `gofmt -l .`, `go vet
./event/...` and `go build ./...` are clean at the end.

**Re-runs of round 1's claimed closures — all eleven behaved as claimed.**

| # | File:line | Mutation | Verdict | Turned red |
|---|---|---|---|---|
| P6 | reader.go:51 | `Next` maps a store failure at the append door | caught | `TestAnOutcomeOutsideTheVocabularyTakesTheFailSafeDefault` |
| P6b | errors.go:251-256 | `backendRefusal` never promotes a retryable cause | caught | the same test, plus `TestADeclaredWrapIsReachableByErrorsAsAndACauseIsNot` |
| B1 | binding.go:37 | `Bind` walks the fold table, applying every declared fact | caught | `TestABindInterrogatesNoCodecAndMutatesNoDeclaration` |
| M34 | repo.go:265 | the recorded-payload cap becomes `>=` | caught | `…/a_recorded_payload_of_exactly_the_bound_is_read` |
| M30 | binding.go:90 | the `MaxPayload` kernel ceiling drifts by one | caught | `TestBothDoorsCheckTheStore` |
| M30b | binding.go:91 | the `MaxBatch` kernel ceiling drifts by one | caught | `TestBothDoorsCheckTheStore`, `TestAnAppendWhoseActualBytes…` |
| M31 | binding.go:92 | the `MaxKey` kernel ceiling drifts by one | caught | `TestBothDoorsCheckTheStore` |
| M32 | binding.go:93 | the `StreamPage` kernel ceiling drifts by one | caught | `TestBothDoorsCheckTheStore` |
| M32b | binding.go:94 | the `MaxRead` kernel ceiling drifts by one | caught | `TestBothDoorsCheckTheStore` |
| K1 | repo.go:157-160 | `streamOf` swallows `locate`'s refusal | caught | `TestTheRenderedKeyIsCheckedBeforeTheStoresBound` |
| K2 | repo.go:168 | `checkKey` reads `MaxKeyBytes` rather than the store's bound | caught | `TestTheRenderedKeyIsCheckedBeforeTheStoresBound`, `TestARequestClassRefusal…` |

**New mutations.**

| # | File:line | Mutation | Verdict | Turned red |
|---|---|---|---|---|
| N1 | repo.go:175 | the batch-count bound becomes `>=` | caught | `TestAStreamWithHistoryFoldsToItsCurrentState` (3 subtests), `TestAConsumerReadsThroughPages…` |
| N2 | repo.go:187 | the resident-bytes bound becomes `>=` | **survived** | — (GAP-10) |
| N3 | reader.go:72 | the reader's ascent check becomes `<` (equal positions admitted) | **survived** | — (GAP-11) |
| N4 | reader.go:68 | the reader's page-length check becomes `>=` | caught | `TestAConsumerReadsThroughPagesTheStorePublished` |
| N5 | repo.go:235 | `Repo.checkPage`'s length check becomes `>=` | caught | 5 tests and all 13 fuzz seeds |
| N6 | marker.go:34-38 | `markerFor` resolves the outermost matching marker | **survived** | — (GAP-13) |
| N7 | repo.go:89 | `Append` compares backings with `==` instead of `Equal` | **survived** | — (GAP-12) |
| N8 | binding.go:45 | `Bind` retains the kernel ceilings instead of the store's bounds | caught | 5 tests |
| N9 | binding.go:70-72 | `admit` skips the backing-validity check | caught | `TestBothDoorsCheckTheStore` |
| N10 | repo.go:74-83 | `Append` merges the stream check and the carried-refusal check into one loop | **survived** | — (GAP-14) |
| N10b | repo.go:74-83 | `Append` checks the carried refusal **before** the stream | caught | `TestAppendRefusesInItsStatedOrder` |
| N11 | reader.go:51 | `Next` clears the last page when the store fails | **survived** | benign — no contract clause either way |
| N12 | reader.go:53-56 | `Next` advances the cursor before the page is checked | **survived** | — (GAP-9) |
| N13 | repo.go:30-34 | `Load` checks the key after asking the store | caught | `TestTheRenderedKeyIsCheckedBeforeTheStoresBound` |
| N14 | reader.go:57 | `Next` reports *more* whenever its cursor is non-empty | caught, badly | the package **hangs**; only the 10-minute panic timeout ends it |
| N15 | repo.go:121 | `Within` does not mark the context it returns | caught | `TestRepoAuthorityIs…`, `TestWithinComposesForTwoBackings` |
| N16 | token.go:37 | `Commit.Empty` reads `count <= 0` | **survived** | benign — `count` is never negative |
| N17 | text.go:29 | `checkText`'s length check becomes `>=` | caught | `TestFoldRefusesAnotherInstance`'s at-the-cap row, `FuzzComposeRenders…` (13 seeds) — closes GAP-T11's first edge |
| N18 | store.go:30 | `Support.stated()` answers false for `Unsupported` | caught | 8 tests — closes GAP-T8 |
| N19 | bounds.go:12 | `MaxKeyBytes` `2<<10` → `2<<20` | **survived** | — (GAP-15) |
| N20 | bounds.go:14-15 | `MaxBatchCount` and `MaxPageCount` swapped | caught | `TestAStoreWhoseProductExceedsTheResidentCeilingIsRefused` |
| C1 | repo.go:11-16, 42 | the repository keeps mutable state between two calls | **survived** under `-race` | — (GAP-6) |

**The demonstration behind GAP-9**, run against the shipped code and against N12,
with the temporary file deleted afterwards: a four-event stream, a first page whose
positions are swapped, the defect cleared after the refusal.

```
shipped   cursor after the refused page: ""    events read after the refusal: 4 of 4
N12       cursor after the refused page: "3"   events read after the refusal: 1 of 4
```

## Rubric axes with nothing new to report

- **Coverage of the spec.** Every UC and INV the matrix assigns to S4 has a named
  test that exists and passes; the twenty-six-name `-list` clause matches the
  section's own table and the files. No test in the section lacks a UC or INV
  behind it: `TestARequestClassRefusalRenders…` is C5, `TestABindInterrogates…` is
  §UC-055, `TestTheRenderedKeyIsChecked…` is §UC-051, and the fuzz target is
  §UC-015 + §INV-006 universally quantified. No scope creep found.
- **Tests that cannot fail.** None. Every table has more than one row; every
  refusal case has a control that fails against a door refusing everything; the
  arithmetic cases assert their own arithmetic before the rows run; the one
  `recover` is the subject of `TestAFoldPanicUnwindsOutOfLoad` and is asserted on;
  no `is not nil`-only check, no assertion on a value the test computed from the
  code under test.
- **Behaviour vs internals.** `recordingStore.exactly` counts a **declared seam**
  (§INV-007, §INV-027, §UC-055 are statements about how often the kernel may call a
  store), which is the only way to test them. No private method is patched. The one
  unexported read of the code under test is `aggregate.sealed`/`aggregate.facts` in
  `TestABindInterrogatesNoCodecAndMutatesNoDeclaration`, which is the subject of
  that test rather than a shortcut to it.
- **Doubles discipline.** One fake, no mocks; `event` cannot import
  `event/eventmemory` without a cycle, so an in-package store is structural. Its
  behaviour is real — expected-version admission, paging, `NewAuthority`,
  `bytes.Clone` inbound. GAP-5 is the one consequence and it is carried.
- **Edge coverage.** Empty stream, empty append, empty page, oversized payload,
  over-long batch, duplicate changes, non-UTF-8 and control-carrying stored type
  names, a control character in a rendered key, panics at all three codec methods
  and at the fold and the upcaster, an unclassified store error at both read doors
  and the append door, a closed store at four entry points. What is missing is
  named in GAP-10, GAP-11 and GAP-13. Cancellation at the public doors is not
  driven here and is checkpointed at S5 (`cancellation`); the mapper's cancellation
  branches are S1's and are covered there.
- **Universality.** No golden blob, no snapshot. Byte-bound cases derive from
  `MaxResidentBytes`/`MaxBatchCount`, product cases from `ResidentPage(maxPayload)`,
  the reader from `store.limits.MaxRead`. Four state types are driven through the
  repository — `account` (struct), `carrier` (byte payload), `ledger` (map) and the
  `notes.note` family — and the kernel branches on none. Adding a second aggregate
  needs no new branch in any helper. GAP-15 is the one place a number is asserted
  only relative to itself, and it is a constant rather than a sample.
- **Determinism and isolation.** `-race -count=3` and `-shuffle=on` three times, all
  green. `declareAccounts` and `newRecordingStore` build a fresh aggregate and a
  fresh store per call, so there is no shared mutable fixture. No sleep, no
  wall-clock assertion, no unseeded randomness, no network, no file left behind; the
  fuzz run wrote no corpus. The one map iteration (`transaction_test.go:302`) drives
  two independent repositories and its assertions are order-independent.
- **Honesty.** No `t.Skip`, no `xfail`, no `t.Log` standing in for an assertion, no
  widened tolerance. The section's claimed checkpoint block reproduces verbatim, its
  claimed count is 26, and every claimed closure mutation behaved as claimed.
- **Speed and layering.** Unit only, no database. `go test -race ./event/` is
  **1.26 s**; `./event/...` including `eventmemory` is 1.6 s wall.
