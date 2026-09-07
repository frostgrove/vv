# EVENTSOURCE_P1 — S3 (`event/eventmemory`: a complete, transaction-capable store) — TEST GAPS

## Round 1 — econv test reviewer (clean context) — 2026-09-07

**What was graded.** The eleven test files of `event/eventmemory` —
`store_test.go`, `transaction_test.go`, `read_test.go`, `capability_test.go`,
`admission_test.go`, `ownership_test.go`, `cancellation_test.go`,
`resumption_test.go`, `concurrency_test.go`, `construction_test.go` — 24 tests,
2 fuzz targets, 91 subtests, 1 960 test lines, against the 561 implementation
lines of `event/eventmemory/{doc,log,store,append,read,transaction,cursor}.go`
and the `event/bounds.go` (`ResidentPage`) and `event/store.go` edits the
section names as its own. Read alongside: the plan's §S3, §Coverage matrix,
§Contracts and §Carried gaps C7/C9;
`.agents/artifacts/usecases/EVENTSOURCE_P1_USECASES.md` §UC-006/008/022/028/029/
037/040/044/047/053/054/055/057 and §INV-002/003/009/013/014/021/028/032/035/041;
`.agents/artifacts/gaps/EVENTSOURCE_P1_S3_GAPS.md`; `CLAUDE.md`; and
`~/.claude/skills/econv/references/{gaps,universality,architecture,building-blocks,data-integrity,microkernel,restrictions,readability}.md`.

**Runs, all in this worktree.**

```
go build ./...                                          EXIT=0
go vet ./event/...                                      EXIT=0
gofmt -l event                                          silent
go test -race -count=1 ./event/...                      ok event 1.244s · eventmemory 1.026s
go test -race -count=3 ./event/...                      ok event 1.676s · eventmemory 1.054s
go test -race -count=1 -shuffle=on ./event/eventmemory/ (x5)  ok 1.021 / 1.024 / 1.024 / 1.024 / 1.023s
go test -race -count=1 ./event/eventmemory/ (cold cache)  real 1.230s
go test -list '.*' ./event/eventmemory/ | grep -cE '^(Test|Fuzz)'   26
go test -list '^(…the section's eight…)$'   | grep -c '^Test'        8   BLOCK1 COUNT OK
go test -list '^(…the section's phase-5 twelve…)$' | grep -cE '^(Test|Fuzz)'  12  BLOCK2 COUNT OK
go test -v ./event/eventmemory/ | grep -c '^    --- PASS'           91
go test -fuzz FuzzACursorEitherResumesInsideTheLogOrIsRefused -fuzztime 20s      7 046 749 execs, PASS
go test -fuzz FuzzAPayloadIsHandedBackByteForByteAndBelongsToWhoeverReadsIt …    5 622 160 execs, PASS
go test -coverprofile -covermode=atomic ./event/eventmemory/        98.2% of statements
```

Both of the section's own phase-5 checkpoint blocks reproduce exactly, so
nothing the section claims about counts or greenness is a claim that was not
run. No flake, no order dependence, no leftover corpus file: the two fuzz
campaigns wrote no `testdata/` into `event/eventmemory/`, and `cmp` against a
pre-campaign copy of every `.go` file under `event/` and `event/eventmemory/`
reports the tree byte-identical after the whole campaign.

**Where this section is genuinely strong, said first because it changes what the
findings mean.** Twelve deliberate breakages of the store's *absence-shaped*
semantics all died, most with a message that names the consequence rather than
the value: the payload clone, the burned positions, admission against
committed-plus-staged, the closed flag, the cursor fingerprint, the claim check,
the global index, the claim release, the finished-transaction check, the nil
binding, `ReadAll`'s ambient consultation, the per-log fingerprint. Every test is
`package eventmemory_test`, so nothing asserted is unreachable by a consumer.
There is not one mock, stub or hand-written double in the package: the real
`Log`, the real `Store` and the real `*Tx` are under test throughout, and the
only injected seam — `Spec.Clock` — is one the contract declares. Controls sit
beside almost every refusal, and several of them are the right kind (the
backwards clock asserted to actually descend; the tiling reader's count asserted
non-zero before its monotonicity claim is believed). `errors.Is` against the
exported sentinels, `t.Fatalf` messages in plain words, arrange/act/assert
visible, shared builders that do not hide the value under test. There is no
`t.Skip`, no `t.Parallel`, no sleep-to-synchronise, no wall clock in an
assertion, no network and no filesystem.

**What the campaign found on the other side of that.** Sixteen mutations
survived. The three worst are not edge cases: the store may throw away every
record's **type and revision**, it may key its entire history by the aggregate
**key alone and ignore the family**, and a read inside a transaction may hand
back **another stream's** staged records — and the suite, at 98.2 % statement
coverage, stays green on all three.

---

## Mutation log

Every mutation was applied to the implementation, `go test -race -count=1
./event/...` run, and the file restored from a pre-campaign copy. `cmp` confirms
the tree is byte-identical afterwards; `gofmt -l event` is silent and
`go vet ./event/...` is clean on the restored tree.

### Survived — 16

| # | Mutation | Where | Consequence if it shipped |
|---|---|---|---|
| M1 | `Type: ""`, `Revision: 0` in the envelope an append writes | `append.go:52-53` | every stored event loses the two fields that say which fact it is |
| M2 | `Type: req.Records[0].Type + "-mutated"`, `Revision: req.Records[0].Revision + 7` | `append.go:52-53` | every batch is recorded under one wrong type at a wrong revision |
| M3 | history, claims and staged counts keyed by `event.Stream{Key: s.Key}` — the family dropped | `log.go:77,81,91` + `read.go:28` + `transaction.go:129,131,138,142,145,147` | two aggregates of different families sharing a key share one history, one version counter and one claim |
| M4 | `stagedFor` returns `this.staged` unfiltered | `transaction.go:145-151` | a read inside a transaction returns another stream's staged records |
| M5 | `ReadStream` pages by `this.limits.MaxRead` | `read.go:35` | a stream page is not the `StreamPage` the store publishes; the kernel's page check refuses every read |
| M6 | a staged envelope is handed out without `handOut` | `read.go:38-42` | a reader inside a transaction writes into the record that transaction will commit |
| M7 | `Transaction` answers `event.NewAuthority(this.log.backing, this)` — the store, not the transaction | `transaction.go:53` | two *different* live transactions of one store compare `Same`; "two subsystems wrote together" is a false positive |
| M8 | `ResidentPage` answers `2 * (MaxResidentBytes / maxPayload)` | `event/bounds.go:28` | a page may hold twice `MaxResidentBytes`; store and kernel round one quantity the same wrong way |
| M9 | the default clock is `func() time.Time { return time.Time{} }` | `store.go:73` | every event in a store constructed without a `Clock` is recorded at the zero instant |
| M10 | `Rollback` returns `ctx.Err()` when the context is over | `transaction.go:95-96` | a cancelled request leaves its claims held for the life of the process — the exact hazard `transaction.go:77-80` says it prevents |
| M11 | `revalidate` gutted to `return nil` | `transaction.go:110-117` | the commit-time re-validation of every claim disappears |
| M12 | a staged envelope reads back at `this.log.position + 1` | `transaction.go:147` | a caller checkpoints a position that is not the one the event will be published at |
| M13 | an append with no records answers `Failure(Conflict, errStreamMoved)` | `append.go:36-38` | the branch is dead — no test drives it |
| M14 | the empty-records return moved above the ambient resolution | `append.go:29-38` | an empty append through a finished or nil transaction is admitted instead of refused |
| M15 | `Check` reports closure before it reads the deadline | `store.go:115-120` | a closed store under a cancelled context answers `ErrClosed`, not the cancellation |
| M16 | `ResidentPage(maxPayload <= 0)` — covered by M8's family; the `event/` package names neither `ResidentPage` nor `MaxResidentBytes` in any test | `event/bounds.go:24-28` | the kernel's resident rule has no test of its own anywhere |

### Killed — 14, with the message each produced

| Mutation | Caught by |
|---|---|
| `handOut` does not clone the payload | `TestAPageIsTheCallersIncludingItsCapacity`, `FuzzAPayloadIsHandedBackByteForByteAndBelongsToWhoeverReadsIt` |
| `Rollback` does not burn the positions it staged | `TestARolledBackAppendBurnsItsPositions` |
| `Append` admits against the committed version alone | `TestASecondAppendInOneTransactionIsAdmitted` |
| `Close` never records that it closed | `TestCheckAnswersWhileTheStoreIsOpenAndAfterItIsClosed` |
| `readCursor` never compares the fingerprint | `TestAStreamIsReadInPagesTheStorePublished` |
| `Append` drops the claim check | `TestAClaimCoversOnlyTheStreamsATransactionWroteTo` |
| `publish` never adds to the global index | `TestAnAppendIsAdmittedOnlyAtTheVersionItWasDecidedAt` |
| `Commit` never releases the claims it took | `TestAClaimCoversOnlyTheStreamsATransactionWroteTo` |
| `ambient` never notices a finished transaction | `TestARolledBackAppendBurnsItsPositions` |
| `WithTransaction` ignores a nil transaction | `TestATransactionThisStoreCannotUseIsRefusedAtEveryDoor` |
| `ReadAll` never consults the ambient transaction | `TestATransactionThisStoreCannotUseIsRefusedAtEveryDoor` |
| every log mints one fixed fingerprint | `TestTwoStoreValuesOverOneLog`, `TestACursorIsTheBackingsAndResumesThroughAnyStoreValueOverIt` |
| `Begin` hands back one shared `*Tx` per store | `TestAClaimCoversOnlyTheStreamsATransactionWroteTo` |
| the store records the wall clock and ignores the injected one | `TestVersionsAreDenseAndPositionsAscendWhateverTheClockSays` |
| `ReadStream` and `ReadAll` swap their page bounds | `TestACursorIsTheBackingsAndResumesThroughAnyStoreValueOverIt`, `FuzzACursorEitherResumesInsideTheLogOrIsRefused` |

### Uncovered statements, from the profile rather than from reading

```
append.go:37.3,38.1        the empty-records `return nil`               (GAP-T13)
transaction.go:88.3,89.1   Commit's `return err` from revalidate        (GAP-T12)
transaction.go:114.4,115.1 revalidate's `return errStaleClaim`          (GAP-T12)
log.go:59.3,60.1           NewBacking's error path — structurally unreachable, not a gap
```

---

### GAP-T1 [critical][immediate] `Envelope.Type` and `Envelope.Revision` are asserted by nothing — the store may drop both and the suite, at 98.2 % statement coverage, stays green

- **Where:** `event/eventmemory/append.go:52-53` (the two fields no test
  constrains); `event/eventmemory/store_test.go:42-48` — `records()`, the only
  `event.Record` factory in the package, which hard-codes
  `Type: "accounts.credited", Revision: 1` for every record every test ever
  appends; `event/eventmemory/store_test.go:115-137` — `payloadsOf`,
  `versionsOf` and `positionsOf`, the three projections nearly every assertion
  goes through, none of which reads either field. `grep -n '\.Type\|\.Revision'
  event/eventmemory/*_test.go` returns **zero** matches.
- **What:** M1 and M2. Replacing `Type: record.Type, Revision: record.Revision`
  with `Type: "", Revision: 0` — or with the first record's type suffixed and its
  revision plus seven — leaves `go test -race -count=1 ./event/...` green, both
  fuzz targets green, and 91 of 91 subtests passing.
- **Why this severity:** these are two of the five fields of `event.Envelope`
  that carry data (`Stream`, `Version`, `Position`, `Payload`, `RecordedAt` are
  the others, and all five *are* asserted). `Type` and `Revision` are what S2's
  chain uses to find the fact and the decoder for a stored event: a store that
  loses them turns every `Load` in S4 into §UC-013's unknown-event-type refusal
  or §UC-014's unreadable-revision refusal on a history that is perfectly intact
  on disk. It is the single defect a store can have that makes *every* aggregate
  in the framework unloadable, and it is the one this section cannot see. The
  contract states it in terms — `event/store.go`'s `Append`: "writes the records"
  — and §INV-003 makes one append one atomic unit, of which a record's identity
  is part.
- **Why this timing:** S4 binds its repository to this store and S5 runs the
  conformance suite against it. If the two fields are unpinned here, the first
  place the defect can surface is a `Load` in S4 whose refusal reads as a
  declaration bug, not a store bug — and S5's suite is being written against a
  reference store nobody has held to this. Every later store (phase 2's
  PostgreSQL) copies the shape this one certifies.
- **Close criteria:**
  - [x] `records()` produces records of at least two distinct `Type` values and
        at least two distinct `Revision` values, derived from the payload list
        rather than constant, so no test can accidentally use one of each.
  - [x] At least one test asserts, over a multi-record batch and over a read of
        the same stream, that every returned `Envelope.Type` and
        `Envelope.Revision` equals the `Record.Type` and `Record.Revision` at the
        same offset — for a committed read, a staged read inside a transaction,
        and a `ReadAll` page.
  - [x] `FuzzAPayloadIsHandedBackByteForByteAndBelongsToWhoeverReadsIt` (or a
        sibling) fuzzes the type name and revision alongside the payload, or a
        table drives at least three type/revision pairs.
  - [x] M1 and M2 both turn the suite red, with a message that names the field
        that was lost.
- **Status:** closed 2026-09-07 — `TestAnEnvelopeCarriesTheTypeAndRevisionOfTheRecordItWasWrittenFrom`
  (`event/eventmemory/identity_test.go`) and the varying `records()` helper. M1 and
  M2 both die.

### GAP-T2 [critical][immediate] Every stream in the suite comes from one hard-coded family, so a store that ignores `Stream.Family` entirely passes — `universality.md`

- **Where:** `event/eventmemory/store_test.go:14` — `const family =
  "accounts.account"`; `event/eventmemory/store_test.go:40` — `func streamOf(key
  string) event.Stream { return event.Stream{Family: family, Key: event.Key(key)} }`,
  the only constructor of an `event.Stream` in the whole package. `grep -n
  Family event/eventmemory/*_test.go` returns exactly that one line.
- **What:** M3. Collapsing the log's three `event.Stream`-keyed maps
  (`log.go:35` `streams`, `log.go:38` `claims`, `transaction.go:22` `counts`) and
  every lookup into them to `event.Stream{Key: s.Key}` — so the family is dropped
  from the store's notion of stream identity — leaves the suite green. Nine call
  sites changed, twenty-four tests still pass.
- **Why this severity:** this is the fit-to-one-example finding `universality.md`
  names, and its consequence is data corruption rather than a wrong answer.
  §INV-033 is "a stream is (family, key) compared byte-exact"; §INV-039 is "one
  family names one aggregate, forever"; §UC-059 is two aggregates in one family
  and §UC-002 is a second aggregate. An application with `accounts.account` key
  `A-17` and `orders.order` key `A-17` — the second commonest shape in any system
  that reuses a tenant or customer identifier as an aggregate key — would have
  the two histories interleaved in one stream, one shared version counter (so
  every second append conflicts), one shared claim, and folds that replay another
  aggregate's events. Nothing in the suite could report it. A second instance of
  the same domain object needs no new branch here; a second *family* does, and
  that is the test the suite has to be able to write without changing a helper.
- **Why this timing:** hard-coded/fit-to-one-example findings are never deferred
  (`gaps.md`, minimum `[high][immediate]`); this one is `critical` because the
  failure is silent corruption. S5's conformance suite is about to be built as
  the certification every store runs, and if the reference store's own package
  cannot express two families, the suite written against it will inherit the same
  blind spot for every store in phase 2 and after.
- **Close criteria:**
  - [x] `streamOf` takes a family as well as a key, or a second helper exists, so
        no test can write a stream without choosing a family.
  - [x] At least one test writes two streams that share a `Key` and differ only in
        `Family` and asserts: independent versions from 1, independent `ReadStream`
        pages, a claim taken by a transaction on one that does **not** refuse an
        append to the other, and a `ReadAll` that returns both.
  - [x] M3 turns the suite red.
  - [x] No `const family` at package scope that every stream in the package reads.
- **Status:** closed 2026-09-07 — `streamOf` takes a family, six families are in use across the
  package, and `TestTwoFamiliesSharingOneKeyAreTwoStreams` drives two of them at one
  key. M3 dies.

### GAP-T3 [critical][immediate] No transaction in the suite ever stages to two streams, so `stagedFor` can return every stream's staged records to a read of one

- **Where:** `event/eventmemory/transaction.go:141-152` (`stagedFor`, whose whole
  body is the `if envelope.Stream == stream` filter at `:147`);
  `event/eventmemory/read.go:29-43` (the read that consumes it). The four tests
  that stage inside a transaction each stage to exactly one stream:
  `transaction_test.go:61-100` (`TestAReadInsideATransactionSeesItsOwnStagedAppends`,
  one stream), `transaction_test.go:172-226` (one stream, two records),
  `admission_test.go:87-132` (`held` only), `capability_test.go:33-48` (one
  stream). `concurrency_test.go:171-189` (`appendAsWriter`) stages exactly one
  request per transaction.
- **What:** M4. Replacing the loop and its filter with `return this.staged`
  leaves the suite green. The `this.counts[stream] == 0` guard at `:142` is what
  hides it: for a transaction that touched one stream the two implementations are
  identical, and no test ever gives it a second stream to be wrong about.
- **Why this severity:** `event/store.go`'s `ReadStream` contract is "Envelopes of
  the stream that was asked for … whose first version is `after + 1` and each
  subsequent one exactly one higher". A transaction that credits an account and
  writes an audit stream — the ordinary two-stream unit of work `Within` and
  §UC-030 exist for — would, on reloading the account inside that same
  transaction, be handed the audit stream's envelopes as if they were the
  account's. In S4 the kernel folds that page: the aggregate replays foreign
  events, its version arithmetic is wrong, and §INV-006 (no partially rehydrated
  state) is violated with no refusal at any door. The kernel's own page shape
  check (C4/§INV-018) would catch the version discontinuity in *some* orderings
  and not others, so this is a corruption that surfaces intermittently.
- **Why this timing:** it is the one behaviour §UC-028 ("a caller transaction
  spans load and append") is *for*, and S4's `Within` composes two backings on
  top of it. A read-your-own-writes seam that has never been exercised with two
  streams is not a seam S4 can build on.
- **Close criteria:**
  - [x] A test stages records to two different streams inside one transaction and
        asserts each `ReadStream` inside that transaction returns only its own
        stream's records, in the right order, at dense versions continuing the
        committed ones — with a third, untouched stream reading empty as the
        control.
  - [x] The same test asserts the two-stream commit publishes both, and that the
        stream-order-within-the-log property holds across them.
  - [x] M4 turns the suite red.
- **Status:** closed 2026-09-07 — `TestATransactionThatStagesToTwoStreamsNeverCrossesThem`
  (`event/eventmemory/transaction_test.go`). M4 dies.

### GAP-T4 [high][immediate] Every store in the suite sets `StreamPage` and `MaxRead` to the same number where it matters, so `ReadStream` can page by the wrong published bound

- **Where:** `event/eventmemory/read.go:35` — `count := min(length-first,
  this.limits.StreamPage)`. The four stores whose paging is exercised past one
  page: `read_test.go:15` and `read_test.go:79` (`StreamPage: 2, MaxRead: 2`),
  `store_test.go:155` (`StreamPage: 4, MaxRead: 4`), `concurrency_test.go:20`
  (`StreamPage: 8, MaxRead: 8`). The assertion that *would* catch it,
  `read_test.go:86-89`, compares `len(page)` against `store.Limits().StreamPage`
  — but only in the one test where the two numbers are equal.
  `resumption_test.go:17` is the sole store with them different (`MaxRead: 2`,
  `StreamPage` defaulted to 256) and it never reads a stream past one page.
- **What:** M5. Changing `this.limits.StreamPage` to `this.limits.MaxRead` at
  `read.go:35` leaves the suite green. (The *swap* of both bounds is caught, by
  `resumption_test.go` and the cursor fuzz — so the `ReadAll` half is pinned and
  the `ReadStream` half is not.)
- **Why this severity:** `event/store.go` makes `StreamPage` one of the two
  numbers "only [the store] can apply, because only it issues the read". S4's
  kernel verifies a returned stream page against `Limits().StreamPage`
  (§INV-018/C4, `TestAMisPagedStreamIsRefusedBeforeItIsFolded`): a store paging by
  the wrong number returns an over-long page and *every* load through it is
  refused before it is folded. A store paging by a *smaller* number is worse — a
  short page is the end of a stream and the kernel issues no confirming read, so
  a history is truncated silently, which is the failure `event/store.go` names in
  terms. This is a missing test for a stated contract clause.
- **Why this timing:** S5's conformance suite certifies exactly this pair of
  numbers for every store, and it is being written against a reference store
  whose own package cannot tell the two apart. Phase 2's SQL store will set them
  differently as a matter of course (a `LIMIT` per stream versus a `LIMIT` per
  global scan).
- **Close criteria:**
  - [x] At least one test constructs a store with `StreamPage != MaxRead`, both
        smaller than the stream it writes, and asserts a `ReadStream` page is
        capped at `StreamPage` and a `ReadAll` page at `MaxRead` — each with the
        other number as the value the assertion would wrongly accept.
  - [x] M5 turns the suite red.
- **Status:** closed 2026-09-07 — `TestAStreamIsReadInPagesTheStorePublished` now runs over a store
  publishing `StreamPage: 2, MaxRead: 3`, with a subtest asserting each read is capped
  by its own number. M5 dies.

### GAP-T5 [high][immediate] §INV-021's hand-off 4 is asserted only over committed records — a staged envelope may be handed out without a clone

- **Where:** `event/eventmemory/read.go:36-43` — the loop that hands out
  `handOut(held[index])` where `held` is `committed` **or** `staged`;
  `event/eventmemory/read_test.go:12-74`
  (`TestAPageIsTheCallersIncludingItsCapacity`) — all five subtests read a stream
  with no transaction bound, so `staged` is always empty;
  `event/eventmemory/ownership_test.go:59-74` — the one staged subtest, which
  tests the **inbound** direction only (the caller overwrites its own array after
  staging).
- **What:** M6. Cloning only when the index came from `committed`, and returning
  `staged[…]` raw, leaves the suite green.
- **Why this severity:** §INV-021's fourth hand-off is quantified over the
  recipient — "what a read returns belongs to whoever receives it, for as long as
  it keeps it and from whatever goroutine it reads it on". A caller that reloads
  its aggregate inside its own transaction (the §UC-028 shape) and then mutates
  the payload of the envelope it was handed — which §UC-061 says it may do — is
  writing into the record that transaction is about to commit. The fact recorded
  is not the fact decided, which is the failure the invariant's own text says has
  recurred four rounds running. The plan's S3 `Covers` line claims INV-021
  hand-offs 4 and 7; it holds for half the store's output.
- **Why this timing:** S5's `payload ownership` conformance section is written
  from this store's behaviour, and a decorator returning one reused buffer is
  supposed to fail it. If the reference store itself has an uncloned hand-out
  path, the section will be written to a shape that does not cover it.
- **Close criteria:**
  - [x] A test reads a stream inside a transaction whose staged records are in the
        page, writes into every returned `Envelope.Payload` byte and appends to the
        `[]Envelope`, then re-reads inside the same transaction and asserts the
        staged payloads are unchanged — and commits and asserts the committed
        history is unchanged too.
  - [x] The same assertion over the boundary case: one committed record and one
        staged record in a single page.
  - [x] M6 turns the suite red.
- **Status:** closed 2026-09-07 — `TestAPageOfStagedRecordsIsTheCallersToo`
  (`event/eventmemory/read_test.go`), over a page holding one committed record beside
  two staged ones. M6 dies.

### GAP-T6 [high][immediate] The only "two authorities are not the same" control compares two *stores*, so an authority identified by the store instead of the transaction passes

- **Where:** `event/eventmemory/transaction.go:48-54` (`Transaction`, whose whole
  content is `event.NewAuthority(this.log.backing, tx)` at `:53`);
  `event/eventmemory/transaction_test.go:256-262` — the only negative
  `Same` assertion in the package, `other.Same(named)` where `other` came from
  `elsewhere` (a second store over a second log) and `named` from `here`;
  `event/eventmemory/transaction_test.go:30-42` — the only positive one, two
  calls resolving one transaction on one store.
- **What:** M7. Replacing `tx` with `this` — so the authority is identified by the
  *store value* — leaves the suite green: the positive case still holds (one
  store, one `this`) and the negative case still holds (two stores, two `this`).
  No test opens two live transactions on **one** store and compares them.
- **Why this severity:** §INV-028 states it in terms — "two calls that resolve to
  one **live** transaction answer authorities that compare `Same`, and two
  **live** transactions on one backing answer authorities that do not" — and
  `event/store.go`'s `Transaction` doc repeats the second half verbatim. This is
  what §UC-030 sells: two subsystems proving they wrote inside one transaction.
  Under M7, two subsystems that opened two *different* transactions on the same
  store compare `Same` and the proof is a false positive; the operation commits in
  two units and the caller believes it committed in one. The test at `:256-262`
  carries a failure message that claims to close exactly this hole ("the two
  stores named one transaction, so the comparison above passes whichever binding
  either of them found") — it is a control that does not control what it says.
- **Why this timing:** S4's `TestRepoAuthorityIsTheComparisonTwoSubsystemsUse` and
  S5's `transactions` section both build on the store's authority derivation, and
  §INV-028's own "falsified by" line assigns the `Begin`-twice case to the
  conformance suite. The store that suite certifies has to be held to it first,
  and the fix is one `Begin` and one assertion.
- **Close criteria:**
  - [x] A test begins two transactions on one store, binds each in turn, and
        asserts the two authorities are **not** `Same` while both are live — beside
        the existing two-calls-one-transaction positive.
  - [x] M7 turns the suite red.
- **Status:** closed 2026-09-07 — the second half of
  `TestASecondAppendInOneTransactionIsAdmitted`'s authority subtest: two transactions
  live on one store answer authorities that are not `Same`. M7 dies.

### GAP-T7 [high][immediate] `event.ResidentPage` — the kernel symbol this section added — is asserted only against itself

- **Where:** `event/bounds.go:24-29` (`ResidentPage`, named in §S3's **Files** and
  **Realises** lines as this section's own); `event/eventmemory/store_test.go:289-293`
  — `page := event.ResidentPage(limits.MaxPayload)` immediately followed by
  `if limits.StreamPage > page`, where the implementation under test computes its
  page as `min(defaultPage, event.ResidentPage(spec.Log.maxPayload))`
  (`store.go:52`) and re-checks it with the same function (`store.go:81`);
  `event/eventmemory/store_test.go:302-315` — the same shape for the
  admitted/refused boundary. `grep -n 'ResidentPage\|MaxResidentBytes'
  event/*_test.go` returns **nothing**: the kernel package has no test of its own
  for either.
- **What:** M8. Changing `return MaxResidentBytes / maxPayload` to
  `return 2 * (MaxResidentBytes / maxPayload)` leaves the whole tree green. Both
  sides of every assertion move together, which is the definition of a tautology:
  the test computes its expected value with the function it is testing.
- **Why this severity:** `MaxResidentBytes` is the one bound on the *product*
  rather than a factor — the worst case a read page or an append may hold
  resident (§INV-022). Under M8 a store may publish a page that holds 128 MiB and
  both the store's door check (`store.go:80-91`) and the kernel's own check agree
  it is fine, because they call the same doubled function. The bound becomes
  unenforceable everywhere at once and nothing in the repository reports it. It is
  also a missing test for a stated invariant (§INV-022's "every bound declared,
  reachable and never escapable").
- **Why this timing:** the symbol is exported kernel API that S4's door checks and
  S5's `bounds` section are about to depend on, and a changed rounding rule here
  silently changes what every store may publish.
- **Close criteria:**
  - [x] A test in `event`'s own package pins `ResidentPage` against literals
        derived from `MaxResidentBytes` by hand — at least the `<= 0` arm, the
        exact-divisor arm, an arm where the division truncates, and the
        `MaxPayloadBytes` arm — with no call to `ResidentPage` on the expected side.
  - [x] `store_test.go:289` and `:302` derive their expectation from a literal or
        from `Limits()` rather than from `event.ResidentPage`, or the test states
        explicitly that it is checking the store agrees with the kernel and the
        kernel's own value is pinned elsewhere.
  - [x] M8 turns the suite red.
- **Status:** closed 2026-09-07 — `TestTheResidentPageIsTheCeilingCountedInEnvelopes`
  (`event/bounds_test.go`, `package event`), and `store_test.go`'s two expectations now
  divide `MaxResidentBytes` by hand rather than calling `ResidentPage`. M8 dies.

### GAP-T8 [medium][immediate] `Spec.Clock`'s default is never pinned — a store built without a clock may record every event at the zero instant

- **Where:** `event/eventmemory/store.go:71-74` (`if clock == nil { clock = time.Now }`).
  The three stores in the suite that inject a clock are
  `admission_test.go:20`, `admission_test.go:137` and `concurrency_test.go:20`;
  every other store comes from `openStore` (`store_test.go:34-38`) or `newStore`
  with no `Clock`, and no test reads `RecordedAt` from any of them.
- **What:** M9. Defaulting to `func() time.Time { return time.Time{} }` leaves the
  suite green. Only the *injected* clock is asserted (correctly, and the
  wall-clock mutation is killed by `TestVersionsAreDense…`); the default is not.
- **Why this severity:** `Spec.Clock` is optional, so the default is the path
  nearly every consumer takes — `eventmemory` is the store §UC-040 puts in a
  caller's unit tests. Every envelope carrying `RecordedAt` zero is an audit and
  diagnostic field that is silently useless, and nothing at any later door checks
  it. It is a local defect with no contract impact on ordering (§INV-009 makes
  the clock non-ordering by design), which is why it is `medium` and not higher.
- **Why this timing:** it is a two-line assertion in a test that already exists,
  and `Spec` is public API S4 and S5 construct.
- **Close criteria:**
  - [x] A test constructs a store with no `Clock`, appends, and asserts the
        returned `RecordedAt` is not the zero time and lies between two instants
        the test itself sampled around the call.
  - [x] M9 turns the suite red.
- **Status:** closed 2026-09-07 — the `a store built with no clock records the instant its append ran
  at` subtest of `TestVersionsAreDenseAndPositionsAscendWhateverTheClockSays`. M9 dies.

### GAP-T9 [medium][deferred] `Rollback`'s "reads no deadline" half is asserted nowhere, though `Commit`'s is

- **Where:** `event/eventmemory/transaction.go:77-80` — the comment that states
  it for both ("Neither this nor Rollback reads the context's deadline… would
  hold its claims for the life of the process and starve every other writer"),
  `transaction.go:95` (`Rollback(context.Context)`); `event/eventmemory/doc.go:16-20`
  repeats the hazard. `cancellation_test.go:87-105` — the subtest named
  "finishing a transaction reads no deadline" exercises `Commit(cancelled)` and
  never `Rollback(cancelled)`.
- **What:** M10. Adding `if err := ctx.Err(); err != nil { return err }` to
  `Rollback` leaves the suite green.
- **Why this severity:** a rollback that refuses under the cancelled context of
  the request that is unwinding is exactly the shape that leaves a claim held
  forever — and `doc.go:18-20` says an abandoned transaction's claims are never
  reclaimed, so every later append to those streams is a conflict that never
  clears. It is a stated property of a public method with no test. Deferred
  because the code is correct today and the fix is one assertion in an existing
  subtest.
- **Close criteria:**
  - [ ] The same subtest rolls a second transaction back under the cancelled and
        the expired context and asserts `nil`, then asserts a later append to the
        stream it had claimed is admitted.
  - [ ] M10 turns the suite red.
- **Status:** deferred — carried to the plan's `## Debt`.

### GAP-T10 [medium][deferred] What position a staged envelope reads back at — one of the nine answers this section says it decided — is asserted nowhere

- **Where:** `event/eventmemory/read.go:11-12` (the sentence that decides it: "A
  staged envelope carries no position, because a position is assigned at commit");
  `event/eventmemory/transaction.go:141-152` (`stagedFor`, which returns the
  envelope as staged); §S3's **Decides** paragraph, which names "what position a
  staged envelope reads back at" among the nine. `transaction_test.go:97-99` is
  the only position assertion near a transaction and it fires *after* the commit.
- **What:** M12. Setting `envelope.Position = this.log.position + 1` on every
  staged envelope handed out leaves the suite green.
- **Why this severity:** a caller that reads inside its transaction and
  checkpoints the position it was handed would persist a cursor pointing at an
  event that does not exist at that position yet and may never (the transaction
  may roll back, and the position is then burned). §INV-035 makes a returned
  resume point safe to persist. It is `medium` because §UC-053's cursor comes
  from `ReadAll`, which returns no staged envelope at all, so the reachable
  damage is narrow.
- **Close criteria:**
  - [ ] `TestAReadInsideATransactionSeesItsOwnStagedAppends` asserts every staged
        envelope in the page carries `Position == 0`, and that the same envelope
        carries a non-zero, strictly-greater position after the commit.
  - [ ] M12 turns the suite red.
- **Status:** deferred — carried to the plan's `## Debt`.

### GAP-T11 [medium][deferred] The commit-time re-validation and `errStaleClaim` have no test and can be deleted outright

- **Where:** `event/eventmemory/transaction.go:106-118` (`revalidate` and its
  comment, "checked where the records are published rather than assumed"),
  `transaction.go:87-89` (`Commit`'s `return err`), `transaction.go:14`
  (`errStaleClaim`). The coverage profile reports both blocks at count 0:
  `transaction.go:114.4,115.1` and `transaction.go:88.3,89.1`.
- **What:** M11. Gutting `revalidate` to `return nil` leaves the suite green, and
  `go vet` clean.
- **Why this severity:** §S3 states plainly that `errStaleClaim` "is unreachable
  while the claim mechanism holds, which is the point of it", so this is not a
  bug — it is a defence-in-depth net with zero coverage. The cost is that the net
  itself is unverified: if the claim mechanism is ever narrowed (a claim released
  early, a claim scoped differently), the net either fires correctly or has been
  silently broken in the same edit, and nothing distinguishes the two.
  §S3's own phase-5 table cites `TestManyWritersLeaveOneDenseHistoryAndAMonotoneLog`
  as making `errStaleClaim` *reachable* under a specific mutation — that is a
  mutation-only proof and leaves the shipped path unexercised.
- **Close criteria:** either
  - [ ] a test reaches `errStaleClaim` through the exported surface (or through a
        deliberate, documented in-package defect fixture) and asserts a commit
        that finds a moved stream refuses rather than publishing at a wrong
        version, **or**
  - [ ] the section records `revalidate` explicitly as an unreachable assertion
        with the reason, and M11 is listed as a known-surviving mutation rather
        than an untested branch.
- **Status:** deferred — carried to the plan's `## Debt`.

### GAP-T12 [medium][deferred] The 200-round transaction race asserts nothing about how often either arm was refused, so it can pass on a store where the race never happens

- **Where:** `event/eventmemory/transaction_test.go:296-351`
  (`TestOneTransactionUsedFromManyGoroutinesRefusesRatherThanCrashes`),
  specifically `:341-349`: `for _, answer := range answers[:2] { if answer == nil
  { continue } … }`.
- **What:** every one of the 200 rounds may return `nil` from both the append and
  the read and the test passes. Its real content is `-race` plus the
  byte-identity of the refusal when one occurs — both valuable — but it makes no
  positive claim, so it is a test that can pass vacuously with no control beside
  it. `CLAUDE.md`'s own rule ("Put a control case next to any test that could
  pass vacuously") is what it is short of, and the pattern it names —
  `test/integration/gate_relscope_test.go` — is the fix.
- **Why this severity:** the property under test (a `*Tx` used from many
  goroutines refuses rather than crashing or corrupting) is genuinely held by the
  implementation and genuinely exercised by the race detector; what is missing is
  the evidence that the interleaving the test is named for ever occurred. If a
  future change made `Commit` block until the other two goroutines finished, the
  test would go quiet rather than red.
- **Close criteria:**
  - [ ] The test counts the rounds in which at least one of the two operations was
        refused and the rounds in which both were admitted, and fails if either
        count is zero across the 200 rounds — with the counts in the failure
        message.
- **Status:** deferred — carried to the plan's `## Debt`.

### GAP-T13 [medium][deferred] `Append`'s empty-records branch is dead, and its position relative to the ambient check is unpinned

- **Where:** `event/eventmemory/append.go:36-38`; the coverage profile reports
  `append.go:37.3,38.1` at count 0 — the only unreached statement in the package
  that is not the `errStaleClaim` pair or a structurally unreachable constructor
  arm.
- **What:** M13 (making the branch answer a conflict) and M14 (moving it above
  the ambient resolution at `append.go:32-35`) both leave the suite green. No
  test in the package ever calls `Append` with an empty `Records`.
- **Why this severity:** §INV-031 makes an empty append cost no store call at all,
  so the kernel never issues one — which is why this is `medium` and deferred.
  But the branch exists, it is reachable by any direct consumer of the store, and
  under M14 an empty append through a **finished or nil** transaction is admitted
  where the transaction rule (§INV-041's third row) says it must refuse before any
  statement. A store used directly in a consumer's unit test (§UC-040) is exactly
  where that would be reached.
- **Close criteria:**
  - [ ] A test appends an empty `Records` on a live context and asserts `nil` and
        an unchanged stream, and appends an empty `Records` through a finished
        transaction and asserts the same `Refused` classification the three
        populated doors give in `TestATransactionThisStoreCannotUseIsRefusedAtEveryDoor`.
  - [ ] M13 and M14 both turn the suite red.
- **Status:** deferred — carried to the plan's `## Debt`.

### GAP-T14 [medium][deferred] The goroutine-leak test measures a process-global count, so its strength depends on which test ran before it

- **Where:** `event/eventmemory/construction_test.go:96-97` (`time.Sleep`, then
  `before := runtime.NumGoroutine()`), `:107-110` (the comparison),
  `:125-134` (`settled`, which returns as soon as `running <= want`).
- **What:** `runtime.NumGoroutine()` counts every goroutine in the process.
  Under `-shuffle=on`, if this test runs immediately after
  `TestManyWritersLeaveOneDenseHistoryAndAMonotoneLog` (six writers and a reader)
  or `TestOneTransactionUsedFromManyGoroutinesRefusesRatherThanCrashes` (600
  goroutines across 200 rounds) while the runtime has not yet reaped their
  stacks, `before` is inflated and `settled(before)` returns on its first poll —
  the assertion is satisfied by the baseline rather than by the store. Every one
  of those goroutines *is* joined by a `WaitGroup`, so this is a strength
  dependency and not a flake: five `-shuffle=on` runs and three `-count=3` runs
  were green.
- **Why this severity:** the property (§INV-013, no constructor starts anything)
  is real and the mutation that starts a goroutine is killed today. What is
  fragile is that the test's own baseline is shared mutable state it does not
  own, which is what `determinism and isolation` is about.
- **Close criteria:**
  - [ ] The test takes its baseline after `settled` has stabilised (poll until two
        consecutive reads agree), or asserts the *delta* against a second baseline
        taken by the same method, so the comparison cannot be satisfied by another
        test's residue.
  - [ ] It states the assertion in a way that fails if `before` was inflated —
        e.g. the second baseline is asserted equal to the first.
- **Status:** deferred — carried to the plan's `## Debt`.

### GAP-T15 [low][deferred] The persistence subtest's assertion cannot fail for the reason its message gives

- **Where:** `event/eventmemory/capability_test.go:60-68`. After asserting
  `claimed.Persistence != event.Unsupported`, it opens a *second, fresh* log and
  asserts a read of it is empty, with the message "so the history is somewhere
  other than the log the caller holds and outlives it".
- **What:** what that assertion actually tests is that two logs are separate
  backings — which `store_test.go:158-166` and `capability_test.go:50-58` already
  pin as the `SharedBacking` claim's negative half. Non-persistence is not
  observable in one process, so the honest evidence is the capability value plus
  `doc.go:33`; the second half is decoration whose failure message names something
  it cannot detect.
- **Why this severity:** cosmetic. The capability value itself *is* asserted and
  the mutation that claims `Persistence: Supported` is killed. The cost is a
  reader believing the store's persistence claim is behaviourally certified here.
- **Close criteria:**
  - [ ] The subtest either drops the fresh-log assertion or renames it to what it
        proves (two logs are two backings), and states in one line why
        non-persistence has no in-process falsifier.
- **Status:** deferred — carried to the plan's `## Debt`.

### GAP-T16 [low][deferred] `Check`'s ordering of the deadline and the closure is unspecified and untested

- **Where:** `event/eventmemory/store.go:114-122`; `Append` (`append.go:21-26`),
  `ReadStream` (`read.go:14-19`), `ReadAll` (`read.go:52-57`) and `Begin`
  (`transaction.go:39-44`) all make the same choice. No test closes a store and
  then calls any of the five under a cancelled context.
- **What:** M15. Reading `closed` before `ctx.Err()` in `Check` leaves the suite
  green. The contract states neither order.
- **Why this severity:** cosmetic today — both answers are defensible and neither
  is wrong. It matters only because §UC-057 and §INV-029 make cancellation
  identity load-bearing, and a caller matching `context.Canceled` on a store that
  happens to be closed would see `ErrClosed` instead. Worth one row in a table.
- **Close criteria:**
  - [ ] Either the contract states which wins and a test pins it at all five
        doors, or the section records that the order is unspecified and M15 is a
        known-surviving mutation.
- **Status:** deferred — carried to the plan's `## Debt`.

### GAP-T17 [low][deferred] Every key in the suite is short ASCII, so `MaxKey` and the key's own edges are untouched

- **Where:** `event/eventmemory/store_test.go:40` — every key is `acme/…`;
  `event/eventmemory/store.go:64` publishes `MaxKey` and
  `construction_test.go:22-42` pins only the *number*. No test uses an empty key,
  a key at `Limits().MaxKey`, a multi-byte key, or a key carrying the cursor
  separator `":"` (`cursor.go:11`).
- **What:** the store keys three maps by `event.Stream`, so a multi-byte or
  separator-bearing key is structurally safe — which is exactly why one cheap
  test would say so rather than leaving it to be assumed. The payload side *is*
  fuzzed over unicode and NUL (`resumption_test.go:174`); the key side is not.
- **Why this severity:** low. The kernel validates the key before the store sees
  it (§D.14, `AppendRequest.Stream`: "the key is legal text within
  `Limits().MaxKey`"), so the store is not the enforcing party.
- **Close criteria:**
  - [ ] One table adds an empty key, a key at exactly `Limits().MaxKey`, a
        multi-byte key and a key containing `":"`, and asserts each round-trips
        through `Append`/`ReadStream`/`ReadAll` as its own distinct stream.
- **Status:** deferred — carried to the plan's `## Debt`.

### GAP-T18 [medium][deferred] §S3's `Covers` names UC-040, and no aggregate is declared anywhere in the package

- **Where:** §S3 **Covers** line (`UC-006 …, UC-040, …`); the coverage matrix
  routes UC-040's checkpoint to S5 (`TestTheMemoryStoreSatisfiesTheContract`).
  `grep -n 'Declare\|Define\|Aggregate' event/eventmemory/*_test.go` returns
  nothing: every test constructs `event.Record` values by hand
  (`store_test.go:42-48`).
- **What:** §UC-040 is "an aggregate unit-tested with no database" — the reason
  this store exists at all. The section's own package proves the store's
  mechanics and never once drives a declared aggregate through it, so the
  ergonomic claim (a consumer can unit-test an aggregate against this store with
  no database and no fixtures) is asserted at S5 and nowhere earlier.
- **Why this severity:** the matrix's checkpoint column puts the proof at S5, so
  this is bookkeeping rather than a hole in the contract — but §S3 says its
  evidence is "its own package tests", and for this one row it is not.
  Deferred because S5 is where the aggregate seam and the store meet by design.
- **Close criteria:** either
  - [ ] S3's `Covers` drops UC-040 (leaving the matrix's S5 checkpoint as its only
        claim), **or**
  - [ ] one test in the package declares a small aggregate, folds a loaded stream
        and appends a decision through this store, with no database and no double.
- **Status:** deferred — carried to the plan's `## Debt`.

---

**Verdict.** Six `[critical|high][immediate]` findings are open (GAP-T1 through
GAP-T7 excluding the deferred). The section's own mutation claim — "twenty-seven
mutations, twenty-seven killed" — is honest about the mutations it lists and
each one I re-ran did die; the gap is in what was never mutated. Sixteen
mutations survive, and three of them (drop `Type`/`Revision`, collapse
`Stream.Family`, unfilter `stagedFor`) are the shapes that would ship a store
whose histories are unloadable, interleaved across aggregates, or crossed
between streams — at 98.2 % statement coverage and 91 passing subtests.

The tree was left exactly as found: `cmp` byte-identical across every `.go` file
under `event/` and `event/eventmemory/`, `gofmt -l event` silent,
`go vet ./event/...` clean, `go test -race -count=3 ./event/...` green.

---

## Round 1 — closures, 2026-09-07

**All eight `[immediate]` findings are closed** — GAP-T1 to GAP-T3
(`[critical]`), GAP-T4 to GAP-T7 (`[high]`) and GAP-T8 (`[medium]`, one
assertion). The ten `[deferred]` ones, GAP-T9 to GAP-T18, are carried verbatim
into `EVENTSOURCE_P1_PLAN.md`'s `## Debt`, each with the section that owns it.
No finding is disputed: every one of the nine surviving mutations the
`[immediate]` findings name was reproduced here before the test was written, and
each now dies.

**What changed in the suite.** Four tests and one kernel test are new, three
existing tests grew an assertion, and two shared helpers stopped being
constants:

| Where | What |
|---|---|
| `event/eventmemory/store_test.go` | `streamOf(family, key)` — a stream cannot be written in this package without choosing a family, and the `const family` every stream read is gone. `records()` derives each record's `Type` and `Revision` from its place in the batch and its payload, over three kinds, so no batch is one kind by accident. The two `ResidentPage` expectations became `event.MaxResidentBytes / …` divisions, so the test no longer computes its expected value with the function under test |
| `event/eventmemory/identity_test.go` (new) | `TestAnEnvelopeCarriesTheTypeAndRevisionOfTheRecordItWasWrittenFrom` — five distinct type/revision pairs asserted per offset across a committed read, a staged read inside a transaction and a `ReadAll` page, with the distinctness of the five asserted first. `TestTwoFamiliesSharingOneKeyAreTwoStreams` — two families at one key: versions from 1 each, their own pages, a claim on one that does not refuse an append to the other, and a log holding both under two stream identities |
| `event/eventmemory/transaction_test.go` | `TestATransactionThatStagesToTwoStreamsNeverCrossesThem` — two streams staged in one transaction, each read inside it answering only its own at dense versions continuing the committed ones, a third untouched stream reading empty as the control, and the commit publishing both in each stream's own order. `TestASecondAppendInOneTransactionIsAdmitted` also begins a second transaction on the same store and asserts the two authorities are **not** `Same` while both are live |
| `event/eventmemory/read_test.go` | `TestAPageOfStagedRecordsIsTheCallersToo` — a page of one committed record beside two staged ones, written into byte by byte and appended to, then re-read inside the transaction and again after the commit. `TestAStreamIsReadInPagesTheStorePublished` now runs over `StreamPage: 2, MaxRead: 3` and asserts each read is capped by its own published number |
| `event/eventmemory/admission_test.go` | a store built with no `Clock` records an instant between two the test sampled around the append |
| `event/bounds_test.go` (new, `package event`) | `TestTheResidentPageIsTheCeilingCountedInEnvelopes` — seven arms pinned against counts divided out of 64 MiB by hand, plus the ceiling itself, so nothing on the expected side calls `ResidentPage` |

**Mutation evidence — nine applied to the implementation, nine killed.** Each
was applied, `go test -race -count=1 ./event/...` run, and the file restored from
a pre-campaign copy; `diff` reports every implementation file byte-identical
afterwards and no `testdata/` was written.

| # | Mutation | Died as |
|---|---|---|
| M1 | `Type: ""`, `Revision: 0` | *"the stream reads as [@0 @0 @0] where [accounts.credited@1 accounts.debited@2 accounts.frozen@7] was written, so every fold of it looks up the wrong fact and the wrong decoder for a history that is intact"* — and the staged and log assertions beside it |
| M2 | `Type: req.Records[0].Type + "-mutated"`, `Revision: … + 7` | *"the stream reads as [accounts.credited-mutated@8 …] where [accounts.credited@1 accounts.debited@2 accounts.frozen@7] was written"* |
| M3 | the family dropped from the log's three `event.Stream`-keyed maps, nine call sites | *"creating an order at the key an account already uses was refused with [outcome conflict], so a key names one stream whatever family it was written under and two aggregates share one history and one version counter"* |
| M4 | `stagedFor` returns `this.staged` unfiltered | *"the account read inside the transaction returned [credited 10 credited 20 credited 30 credited twice], so an aggregate reloaded inside the unit of work that wrote it folds another stream's events"* |
| M5 | `ReadStream` pages by `MaxRead` | *"a stream page holds 3 envelopes where the store publishes a stream page of 2 and a log page of 3, so a read of one stream is cut by the other number and the kernel either refuses every load or reads a short page as the end of the history"* |
| M6 | a staged envelope handed out without `handOut` | *"after a reader wrote into the page it was handed inside a transaction, that transaction reads [committed zzzzzz zzzzzzzzzz] — so the record it is about to commit is the reader's to rewrite"*, and the committed history failing beside it |
| M7 | `Transaction` answers the store rather than the transaction | *"two transactions live on one store at once answered authorities that compare the same, so two subsystems that each opened their own prove they wrote together and the operation commits in two units"* |
| M8 | `ResidentPage` doubled | *"the kernel's own payload ceiling admits a page of 128 envelopes where 64 of them is what 64 MiB holds …"*, and both arms of `TestAStoreIsBuiltOverEveryLogTheKernelWouldAdmit` red beside it |
| M9 | the default clock is the zero instant | *"a store constructed with no clock recorded an event at 0001-01-01 00:00:00 +0000 UTC where the append ran between … and …"* |

**Runs after the closures.**

```
gofmt -l .                                                    silent
go vet ./event/...                                            clean
go test -race -count=2 ./event/...                            ok event 1.449s · eventmemory 1.045s
go test -race -count=1 -shuffle=on ./event/...                ok (x2)
go test -list '.*' ./event/eventmemory/ | grep -cE '^(Test|Fuzz)'   30   (28 tests, 2 fuzz)
go test -v ./event/eventmemory/ | grep -c '^    --- PASS'     104
go test -coverprofile -covermode=atomic ./event/eventmemory/  98.2% of statements
go test -fuzz FuzzACursorEitherResumesInsideTheLogOrIsRefused -fuzztime 30s   10 579 583 execs, PASS
go test -fuzz FuzzAPayloadIsHandedBackByteForByteAndBelongsToWhoeverReadsIt -fuzztime 30s   164 897 execs, PASS
make unit                                                     no FAIL in any module
make check                                                    all nine checks ok
```

The four statements the profile still reports at count 0 are the same four as
before — `append.go:37`, `transaction.go:88` and `:114`, and `log.go:59`'s
structurally unreachable constructor arm — and they belong to GAP-T11 and
GAP-T13, which are deferred.

---

## Round 2 — econv test reviewer (clean context, re-audit) — 2026-09-07

**What was graded.** The same section, after round 1's eight closures: eleven
test files in `event/eventmemory` (`store_test.go`, `transaction_test.go`,
`read_test.go`, `identity_test.go`, `capability_test.go`, `admission_test.go`,
`ownership_test.go`, `cancellation_test.go`, `resumption_test.go`,
`concurrency_test.go`, `construction_test.go`) plus `event/bounds_test.go`,
against `event/eventmemory/{doc,log,store,append,read,transaction,cursor}.go`,
`event/bounds.go` and `event/store.go`. Read alongside: the plan's §S3, §Coverage
matrix and §Contracts; `EVENTSOURCE_P1_USECASES.md` §UC-006/008/022/028/029/037/
040/044/047/053/054/055/057 and §INV-002/003/009/013/014/021/022/028/032/033/035/
041; round 1 of this file; `CLAUDE.md`; and
`~/.claude/skills/econv/references/{gaps,universality,architecture,building-blocks,data-integrity,microkernel,restrictions,readability}.md`.

**Runs, all in this worktree.**

```
go build ./...                                          EXIT=0
go vet ./event/...                                      clean
gofmt -l event                                          silent
go test -race -count=1 ./event/...                      ok event 1.237s · eventmemory 1.025s
go test -race -count=3 ./event/...                      ok event 1.674s · eventmemory 1.060s
go test -race -count=5 ./event/eventmemory/             ok 1.081s
go test -race -count=1 -shuffle=on ./event/... (x7)     ok, every time
go test -list '.*' ./event/eventmemory/ | grep -cE '^(Test|Fuzz)'   30   (28 tests, 2 fuzz)
go test -list '.*' ./event/ | grep -cE '^(Test|Fuzz)'               28
go test -v ./event/eventmemory/ | grep -c '^    --- PASS'           104
go test -coverprofile -covermode=atomic ./event/eventmemory/        98.2% of statements
go test -fuzz FuzzACursorEitherResumesInsideTheLogOrIsRefused -fuzztime 20s    6 697 769 execs, PASS, 0 new interesting
go test -fuzz FuzzAPayloadIsHandedBackByteForByteAndBelongsToWhoeverReadsIt -fuzztime 20s   105 994 execs, PASS, 0 new interesting
```

Every count and every green claim round 1's closure block makes reproduces
exactly. **Section suite runtime: 1.02 s** for `event/eventmemory` under `-race`,
1.5 s wall for `./event/...`, no database, no network, no filesystem. No flake in
twelve runs, none under `-shuffle=on`, no `testdata/` written by either fuzz
campaign, and `diff -r` against a pre-campaign copy reports the whole `event/`
tree byte-identical after the mutation campaign below.

**Round 1's eight closures are real, and re-verified rather than believed.** All
nine mutations round 1 says it killed were re-applied here and all nine died
again, most with a message that names the consequence rather than the value.
That includes the three that mattered most: the envelope's type and revision, the
family in the stream key, and the cross-stream staged read. `classifiedAs` is
also **not** the vacuous comparison it could look like — `event/outcome_test.go`
pins the seven outcome phrases as seven distinct strings, so comparing a store's
rendering against `event.Failure(outcome, nil).Error()` cannot pass for the wrong
outcome. There is still not one mock, stub or hand-written double in the package;
the only injected seam is `Spec.Clock`, which the contract declares.

**What this round found on the other side of that.** Thirteen new mutations were
applied. **Six survived**, and four of them are new findings the previous round
did not name. Their common shape is not a missing field this time but a missing
*second element*: every transaction in the suite claims exactly one stream, every
transaction is used through exactly one store value, and every key in the suite
differs from every other in more than one byte. Each of those is a set the store
handles and the suite only ever hands one member of.

---

## Mutation log — round 2

Every mutation was applied to the implementation, `go test -race -count=1
./event/...` run, and the whole `event/` tree restored from a pre-campaign copy.
`diff -r` confirms it is byte-identical after every one of them; `gofmt -l event`
is silent and `go vet ./event/...` clean on the restored tree.

### Survived — 6

| # | Mutation | Where | Consequence if it shipped |
|---|---|---|---|
| R2-N1 | `transactionKey` carries `owner *Store` beside `log`; `Begin` records the store on the `Tx` and `ambient` looks up `transactionKey{log, owner: this}` — the binding keyed by the store **value** rather than by the log | `transaction.go:17-36,45,64` | a transaction begun through one store value over a log is invisible to every other store value over that log: the append silently runs on autocommit inside a unit of work the caller opened (GAP-T19) |
| R2-N2 | `Transaction` answers `event.Authority{}, nil` where `ambient` returned an error | `transaction.go:48-54` | the store contract's third answer collapses into its first: a context carrying a finished or nil transaction reads as "nothing of this store's is bound" (GAP-T22) |
| R2-N6 | `folded(stream)` lower-cases the key, and the log indexes `streams`, `claims` and `counts` by the folded stream while `Envelope.Stream` keeps the caller's | `log.go:76-93`, `read.go:28`, `transaction.go:120-152` | two aggregates whose keys differ only in case share one history, one version counter and one claim (GAP-T21) |
| R2-N7 | `stage` takes the claim only when `len(this.counts) == 0` — the first stream the transaction writes to | `transaction.go:128-132` | every stream but the first in a multi-stream unit of work is unprotected; an outside writer advances it and the transaction's own commit fails with the bare `errStaleClaim`, which is a conflict reported from the commit (GAP-T20) |
| R2-N8 | a staged envelope reads back at `this.log.position + index + 1` | `read.go:38-42` | a caller checkpoints a position an event will not be published at (carried GAP-T10, re-verified open) |
| R2-N9 | `release` breaks out of its loop after deleting one claim | `transaction.go:120-126` | every stream but one that a committed transaction claimed stays claimed for the life of the process: it is permanently unwritable and every writer is refused `Conflict` (GAP-T20) |

Two more of round 1's `[deferred]` findings were re-applied and confirmed still
open: `revalidate` gutted to `return nil` survives (GAP-T11), `Rollback` reading
`ctx.Err()` survives (GAP-T9), and deleting `Append`'s empty-records branch
outright survives (GAP-T13).

### Killed — 11, with the message each produced

| Mutation | Died as |
|---|---|
| `Type: ""`, `Revision: 0` in the envelope an append writes | `identity_test.go:67` — *"the stream reads as [@0 @0 @0] where [accounts.credited@1 accounts.debited@2 accounts.frozen@7] was written, so every fold of it looks up the wrong fact and the wrong decoder for a history that is intact"*, plus the staged read and the log |
| the family dropped from the log's three `event.Stream`-keyed maps | `identity_test.go:97` — *"creating an order at the key an account already uses was refused with … [outcome conflict], so a key names one stream whatever family it was written under"* |
| `stagedFor` returns `this.staged` unfiltered | `transaction_test.go:132` — *"the account read inside the transaction returned [credited 10 credited 20 credited 30 credited twice], so an aggregate reloaded inside the unit of work that wrote it folds another stream's events"* |
| `ReadStream` pages by `MaxRead` | `read_test.go:125` — *"a stream page holds 3 envelopes where the store publishes a stream page of 2 and a log page of 3"* |
| a staged envelope handed out without `handOut` | `read_test.go:98` — *"after a reader wrote into the page it was handed inside a transaction, that transaction reads [committed zzzzzz zzzzzzzzzz]"* |
| `Transaction` identifies the authority by the store value | `transaction_test.go:50` — *"two transactions live on one store at once answered authorities that compare the same"* |
| `ResidentPage` answers twice the count | `bounds_test.go:20` — *"the kernel's own payload ceiling admits a page of 128 envelopes where 64 of them is what 64 MiB holds"*, with both arms of `TestAStoreIsBuiltOverEveryLogTheKernelWouldAdmit` red beside it |
| the default clock is the zero instant | `admission_test.go:215` — *"a store constructed with no clock recorded an event at 0001-01-01 00:00:00 +0000 UTC where the append ran between … and …"* |
| the store lower-cases every key it writes **and** reads, envelope included | `identity_test.go:138` — *"the log holds [] under the account family at acme/A-17"*, and `read_test.go:156` beside it. The **envelope-preserving** variant of the same mutation survives — see R2-N6 |
| `page := defaultPage`, the resident cap dropped from the store's default | `construction_test.go:36` and `store_test.go:307` — *"StreamPage 256 at MaxPayload 1048576 is more than the 64 envelopes one read may hold"* |
| `ReadAll`'s `sort.Search` uses `>=`, so a resume repeats the cursor's own event | `admission_test.go:63` — *"reading the log page by page never reached the end"*, in four tests |
| `Commit` publishes without holding the log's mutex | `concurrency_test.go:120` — *"a reader that had already been handed position 25 was then handed 24, so an event became visible below one it had checkpointed past"* |

### Uncovered statements, unchanged from round 1

```
append.go:37.3,38.1        the empty-records `return nil`               (GAP-T13)
transaction.go:88.3,89.1   Commit's `return err` from revalidate        (GAP-T11)
transaction.go:114.4,115.1 revalidate's `return errStaleClaim`          (GAP-T11)
log.go:59.3,60.1           NewBacking's error path — structurally unreachable
```

---

### GAP-T19 [critical][immediate] No transaction in the suite is ever used through a second store value over the same log, so the binding may be keyed by the store and every per-request store silently escapes its caller's unit of work

- **Where:** `event/eventmemory/transaction.go:25-36` (the binding, keyed by the
  log by design and by comment), `:63-75` (`ambient`);
  `event/eventmemory/store_test.go:170-224`
  (`TestTwoStoreValuesOverOneLog` — two store values, no transaction),
  `transaction_test.go:74-113`
  (`TestAReadInsideATransactionSeesItsOwnStagedAppends` — a `sibling` store, used
  only for reads on the **plain** context), `capability_test.go:50-58` and
  `resumption_test.go:27-48` (second store values, `Backing` and `ReadAll` only).
- **What:** every one of the seven transaction tests begins its transaction on a
  store and uses it on that same store. `TestTwoLogsCarryTheirOwnTransactionsInOneContext`
  proves the *negative* half — a transaction of another log does not shadow this
  one — but nothing proves the positive half, that a transaction of **this log**
  is found through any store value over it. Mutation R2-N1 keys the binding by
  the store value instead of the log and the whole suite stays green.
- **Why this severity:** §UC-054 states that two store values over one backing are
  one store for every purpose in the document, with exactly one named exception
  (the bound-family set), and §INV-041's second row says that a transaction the
  store finds for its own backing means the operation runs **inside it**. Under
  R2-N1 the tenancy deployment §UC-055 describes — a store constructed per
  request, per repository, over a shared log — opens a transaction through one
  store value and appends through another: `ambient` finds nothing, INV-041's
  **first** row fires, and the append autocommits. The caller then rolls back and
  the events stay. That is the escape the transaction rule exists to close,
  landing silently, on the deployment shape the section's own `Covers` line
  claims (UC-054, UC-055).
- **Why this timing:** S4's `Repo`, `Within` and the token seam are written
  against `Store.Transaction` and the ambient binding, and S5's conformance suite
  will certify the semantics this suite is the reference shape for. A transaction
  seam whose cross-store-value behaviour is unpinned here is a contract S4 and S5
  are about to depend on without evidence.
- **Close criteria:**
  - [ ] A test begins a transaction on one store value over a log, appends and
        reads through a **second** store value over that same log inside it, and
        asserts the staged records are invisible to a third value on the plain
        context and land on commit — with the two-logs case beside it as the
        control.
  - [ ] The same test asserts `storeA.Transaction(ctx).Same(storeB.Transaction(ctx))`
        for one bound transaction and two store values over one log (§UC-054 ×
        §INV-028).
  - [ ] Mutation R2-N1 (`transactionKey` carrying the `*Store`) applied to the
        implementation turns that test red, and the message names the autocommit
        that would have happened.
- **Status:** open

### GAP-T20 [critical][immediate] Every transaction in the suite claims exactly one stream, so the claim set is untested in both directions — a store that claims only the first or releases only one passes

- **Where:** `event/eventmemory/transaction.go:120-132` (`release` and `stage`,
  both loops over the claim set);
  `event/eventmemory/admission_test.go:87-132`
  (`TestAClaimCoversOnlyTheStreamsATransactionWroteTo` — its transaction stages to
  `held` and to nothing else, and the "second transaction" and "rollback" subtests
  each open a fresh one-stream transaction);
  `event/eventmemory/transaction_test.go:115-175`
  (`TestATransactionThatStagesToTwoStreamsNeverCrossesThem` — the only two-stream
  transaction in the package, and it asserts nothing about claims, before or
  after its commit).
- **What:** the test whose name is *"a claim covers only the streams a
  transaction wrote to"* never gives a transaction more than one stream to write
  to. Two mutations survive: `stage` taking the claim only for the first stream
  (R2-N7) and `release` deleting only one of them (R2-N9).
- **Why this severity:** R2-N9 is a permanent liveness failure — after any
  transaction that writes to two streams commits, one of those streams stays in
  `log.claims` forever and every subsequent writer to it is refused `Conflict`
  with nothing to release the claim. R2-N7 is worse in a quieter way: the second
  stream of a unit of work is unprotected, an autocommit writer advances it, and
  the transaction's own `Commit` then returns the **bare** `errStaleClaim` — an
  unclassified error, which `event/store.go:174-178` says is read as uncertainty,
  and which contradicts the section's own decision that a conflict is reported
  from `Append` and never from the caller's commit. §UC-028 names the two-stream
  unit of work as this section's own.
- **Why this timing:** §UC-028's aggregate-plus-audit shape is the ordinary one,
  and S4's `Within` composes two backings on top of it. A claim mechanism whose
  set semantics is unpinned is a store S5's conformance suite will certify by
  running the same one-stream shape.
- **Close criteria:**
  - [ ] `TestAClaimCoversOnlyTheStreamsATransactionWroteTo` (or a test beside it)
        drives a transaction that stages to **two** streams and asserts an
        autocommit append is refused on **each** of them while the transaction is
        live, with a third, untouched stream admitted as the control.
  - [ ] The same test asserts that after the commit an autocommit append to
        **each** of the two is admitted, and the same after a rollback.
  - [ ] Mutations R2-N7 and R2-N9 each turn that test red.
- **Status:** open

### GAP-T21 [high][immediate] Every key in the suite differs from every other in more than one byte, so §INV-033's *key* half is unpinned — the log may index by a normalised key while the envelope keeps the caller's

- **Where:** `event/eventmemory/store_test.go:38-40` (`streamOf`) and every call
  site of it — `acme/A-17`, `acme/A-18`, `acme/first`, `acme/second`,
  `acme/held`, `acme/apart`, `acme/burned`, `acme/one`…: no two keys in the
  package differ only in case, only in trailing space, only in Unicode
  normalisation form, or only in a byte outside ASCII;
  `event/eventmemory/log.go:76-93`, `read.go:28`, `transaction.go:120-152` (the
  four `event.Stream`-keyed maps).
- **What:** round 1 closed the *family* half of §INV-033 with
  `TestTwoFamiliesSharingOneKeyAreTwoStreams`. The key half has no equivalent.
  Mutation R2-N6 — the log indexing by a case-folded key while `Envelope.Stream`
  keeps the caller's, so nothing reads back wrong, only *collides* — leaves the
  suite green. The visible variant, which also folds the key on the envelope, is
  caught; the invisible one is not.
- **Why this severity:** §INV-033 says a stream is `(family, key)` **byte-exact**,
  and it is on this section's `Covers` line as "the store keys its history by
  family and key both". Under R2-N6 two aggregates whose identities differ only
  in case share one history, one version counter and one claim — the same silent
  corruption GAP-T2 was graded critical for, on the other half of the same pair.
  It is also the exact hazard phase 2 walks into: a PostgreSQL column with a
  case-insensitive collation, or a `citext` key, reproduces R2-N6 by accident.
  Under `universality.md` a suite whose fixtures all differ in a way that hides a
  whole equivalence class of defects is a finding that is never deferred.
- **Why this timing:** S5's conformance suite is written from this suite's shape,
  and it is what the SQL store of phase 2 will be certified by. A key-identity
  case that does not exist here will not exist there either.
- **Close criteria:**
  - [ ] A test appends to two streams of one family whose keys differ only in
        case, and asserts they are two histories — versions from 1 each, their own
        `ReadStream` pages, a claim on one that does not refuse an append to the
        other, and a `ReadAll` holding both under two stream identities. The same
        shape as `TestTwoFamiliesSharingOneKeyAreTwoStreams`, on the other half of
        the pair.
  - [ ] At least one key in the suite carries a non-ASCII byte and one carries a
        trailing space, and both read back byte-identical in `Envelope.Stream`.
  - [ ] Mutation R2-N6 turns that test red.
- **Status:** open

### GAP-T22 [high][immediate] `Store.Transaction`'s third contract answer — an invalid authority **and** an error — is asserted nowhere, so the door that reports "something bound is not a transaction" may report "nothing bound"

- **Where:** `event/store.go:141-155` (the contract: three answers and no fourth);
  `event/eventmemory/transaction.go:48-54`;
  `event/eventmemory/transaction_test.go:428-461`
  (`TestTransactionAnswersWhatTheContextCarriesAfterTheStoreIsClosed` — asks with
  a **live** transaction and with nothing bound, never with a finished or a nil
  one), `:463-506` (`TestATransactionThisStoreCannotUseIsRefusedAtEveryDoor` —
  drives `Append`, `ReadStream` and `ReadAll` with both unusable contexts, and
  never `Transaction`).
- **What:** two of the contract's three answers are asserted; the third is not.
  Mutation R2-N2 makes `Transaction` swallow `ambient`'s error and answer
  `Authority{}, nil` — indistinguishable from "nothing of this store's is
  bound" — and the suite stays green.
- **Why this severity:** this is §INV-041's third row read through the one method
  that exists to report it. `Transaction` is what S4's `Within` and the token seam
  call to decide whether a unit of work is open; under R2-N2 a context carrying a
  finished transaction reads to them as an unmarked context, and the "refuses
  before any statement" guarantee moves from `Within` to whichever door happens to
  be called next. The section's own `Covers` line claims INV-041's store half.
- **Why this timing:** S4 is written against exactly these three answers and the
  section is marked `[x]`; a contract branch with no test is a contract the next
  section will depend on unverified.
- **Close criteria:**
  - [ ] A test asserts `store.Transaction(ctx)` answers an **invalid** authority
        and a **non-nil** error for a context carrying a finished transaction and
        for one carrying a nil one, beside the two answers already asserted.
  - [ ] Mutation R2-N2 turns that test red.
- **Status:** open

### GAP-T23 [medium][deferred] The three deferred findings re-verified this round are still exactly reproducible, and one of them is now reachable through a second path

- **Where:** GAP-T9 (`Rollback` reads no deadline — `transaction.go:95-104`),
  GAP-T10 (a staged envelope's position — `read.go:38-42`), GAP-T11
  (`revalidate` and `errStaleClaim` — `transaction.go:87-118`), GAP-T13 (the
  empty-records branch — `append.go:36-38`).
- **What:** each was re-applied as a mutation this round and each survived, so
  round 1's grading of them stands unchanged. GAP-T11 is now also reachable from
  GAP-T20: with `stage` claiming only the first stream, `revalidate` is the only
  thing standing between a multi-stream transaction and a commit at stale
  versions, and neither is tested — the two gaps compose into silent history
  corruption rather than a refusal.
- **Why this severity:** each on its own is an internal detail with no external
  contract, which is what round 1 deferred them for. The composition is not, but
  closing GAP-T20 removes it.
- **Why this timing:** they remain in the plan's `## Debt`; nothing here changes
  which section owns them.
- **Close criteria:**
  - [ ] The four stay in `EVENTSOURCE_P1_PLAN.md`'s `## Debt` with their owning
        section, unedited.
  - [ ] GAP-T20's closure test is run with `revalidate` gutted as well, and the
        report states which of the two assertions fired.
- **Status:** open

### GAP-T24 [low][deferred] The record kinds a stream is written with never match its family

- **Where:** `event/eventmemory/store_test.go:46-58` — `kinds` is three
  `accounts.*` type names, and `records()` hands them to every stream whatever
  family `streamOf` was given, so `orders.order`, `catalog.product`,
  `billing.invoice` and `shipping.shipment` streams all carry
  `accounts.credited` / `accounts.debited` / `accounts.frozen` envelopes.
- **What:** the store relates a record's type to nothing, so no assertion is
  wrong; but a reader of the suite cannot tell whether the independence is
  deliberate or accidental, and a future assertion that a type belongs to a
  family would have to rewrite every fixture.
- **Why this severity:** cosmetic. `TestAnEnvelopeCarriesTheTypeAndRevisionOfTheRecordItWasWrittenFrom`
  builds its own records explicitly and does pin the field.
- **Why this timing:** no contract depends on it and S5's suite declares its own
  facts.
- **Close criteria:**
  - [ ] `kindOf` derives the type name from the stream's family, or a comment on
        `kinds` states that the type is deliberately unrelated to the family
        because the store relates them in no way.
- **Status:** open

---

**Verdict for this round.** Two `[critical][immediate]` and two
`[high][immediate]` findings are open, so the gate is **red**. Everything round 1
closed is genuinely closed and re-proved; what remains is one shape repeated four
times — a set the store handles whose second element the suite never supplies.
