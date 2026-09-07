# EVENTSOURCE_P1 — implementation S3 — GAPS

## Round 1 — econv-impl-reviewer (clean context) — 2026-09-07

Reviewed against the **code**, not the plan's prose. Read in full:
`.agents/artifacts/plans/EVENTSOURCE_P1_PLAN.md` (§ Carried gaps C7/C9, § Microkernel
classification, § Contracts `### event/eventmemory/` incl. the six-answer decision table,
§ S3, § S4, § S6, § Architecture metrics), `.agents/artifacts/usecases/EVENTSOURCE_P1_USECASES.md`
(§UC-006, UC-008, UC-022, UC-028, UC-029, UC-030, UC-037, UC-044, UC-046, UC-049, UC-053,
UC-054, UC-055, UC-057, UC-066; INV-009, INV-016, INV-021, INV-032, INV-035, INV-041),
`CLAUDE.md`, `docs/roadmaps/2026-09-01-postgres-event-sourcing-roadmap.md`, `event/*.go`,
`event/eventmemory/*.go`, and
`~/.claude/skills/econv/references/{microkernel,architecture,building-blocks,data-integrity,universality,readability,restrictions,gaps}.md`.

Every number and transcript below was produced by running in this worktree. The adversarial
probes ran from a throwaway out-of-tree module at `/tmp/evrace` with a `replace` onto this
checkout, so **nothing in the repository was edited** — with one exception, a single mutation
spot-check on `event/eventmemory/read.go` that was restored byte-identically
(`sha256 48a12a2b2f88f5c9651df60e01341e1607d249509da141f93507e5f9bf3182dd` before and after).

### Checkpoint verification — the pasted transcripts are real

| Clause | Result here |
|---|---|
| `go build ./... && go vet ./event/... && test -z "$(gofmt -l event)" && go test -race -count=1 ./event/...` | `ok …/event 1.245s`, `ok …/event/eventmemory 1.006s`, `EXIT=0` — byte-identical to the plan's paste |
| `go test -list '^(…eight names…)$' ./event/eventmemory/ \| grep -c '^Test'` | **8** |
| `go test -list '.*' ./event/eventmemory/` | **9** tests, the eight plus `TestAStreamIsReadInPagesTheStorePublished`, exactly as S3 says |
| `make check` | nine arms, all `ok` (`check-deps`, `check-tiers`, `check-utils`, `check-triplets`, `check-todo`, `check-replaces`, `check-tidy`, `check-otel-schema`, `check-workspace`) |
| mutation spot-check: `handOut` stops cloning → `go test -run TestAPageIsTheCallersIncludingItsCapacity` | `FAIL … after a reader wrote into the page it was handed, the stream reads [zzzzzzzzzzz zzzzzzzzzzz]` — **the plan's mutation table row is real, word for word** |

### Architecture metrics — counted, not eyeballed

*Size.* Seven non-test files, **568** lines: `transaction.go` 143, `store.go` 118, `log.go` 90,
`read.go` 85, `append.go` 64, `cursor.go` 42, `doc.go` 26. Threshold 400 — no breach. Comment
lines 67 → density **11.8 %**.

*Functions.* 27 across the six code files. Longest **45** (`Store.Append`), then 32
(`Store.ReadStream`), 32 (`New`), 28 (`Store.ReadAll`), 22 (`NewLog`). Threshold 50 — no breach.
Maximum nesting depth **2** (`ReadStream`'s `for → if`, `revalidate`'s `for → if`); every guard
is an early return. No flag parameter, no mode switch, no boolean argument anywhere on the
surface.

*Public surface.* `go doc -all github.com/frostgrove/vv/event/eventmemory` yields exactly
**8 package-level symbols** (`LogSpec`, `Log`, `NewLog`, `Spec`, `Store`, `New`, `Tx`,
`WithTransaction`), **10** `*Store` methods (the eight of `event.Store`, plus `Check` and
`Begin`) and **2** `*Tx` methods. Compared symbol by symbol with the plan's
`### event/eventmemory/` block and the S3 **Realises** line: **zero silent extra surface, nothing
marked done that is missing.** The only textual difference is the `ReadStream` parameter renamed
`s` → `stream`, which is not a contract change.

*Coupling.* Internal (`github.com/frostgrove/vv/…`) imports: **1** in every file — `event` and
nothing else. Total imports per file: 4, 4, 0, 4, 4, 5, 4 — threshold 5, no breach.
`go list -deps ./event/eventmemory` = `utils, crud, errs, event, eventmemory`; no new
third-party dependency, no new `go.mod`, root module still dependency-free.

*Microkernel purity.* `grep -rn "eventmemory" event/*.go | grep -v _test` → **0 matches**.
`grep -rn "memory" event/*.go | grep -v _test` → 3 matches, all the English word in prose
(`fact.go:93,150`, `codec.go:18`). The kernel neither imports nor branches on this extension.
`git status --short -- event/` shows `?? event/eventmemory/` — S3 is a **pure addition**; the
modified kernel files (`aggregate.go`, `codec.go`, `fact.go`, `seal.go`, `encodable.go`) carry
declaration-seam work, none of it store-shaped. **A second store costs zero diffs to `event/`**,
subject to GAP-6.

*Isolation.* Fakes needed to unit-test each new object: `Log` **0**, `Store` **0–1** (a
`func() time.Time`), `Tx` **0**. `Store` and `Tx` are already in their own package; deleting
`Tx` touches `transaction.go`, six lines of `append.go` and four of `read.go`. No shared mutable
context, no god object, no `init`, no package-level mutable state (the eight package `var`s are
all immutable `errors.New` sentinels plus one `var _ event.Store` assertion).

*Purity.* No logger, no `os.Getenv`, no `otel`, no `panic`/`recover`, no I/O anywhere in the
package (`grep -rn "log\.|fmt\.Print|os\.Getenv|otel|panic\(|recover\(\)"` → nothing outside the
`this.log` field). Randomness is confined to `NewLog` (`crypto/rand.Text()` as the log's
fingerprint); the clock is confined to `Append`; both reads are pure.

*Concurrency, beyond `-race`.* A 16-goroutine stress over 4 contested streams with interleaved
autocommit appends, transactional appends, commits, rollbacks and both reads, `-race -count=5`:
**green every run**, positions strictly increasing and per-stream versions dense across ~480
committed events per run. The store's core is sound. What `-race` does not see is GAP-2, which
is a lost-update-shaped logic race, not a data race.

---

### GAP-1 [critical][immediate] A second log's transaction shadows this store's own, and the store then silently autocommits

- **Where:** `event/eventmemory/transaction.go:25` (`type transactionKey struct{}`),
  `:27-29` (`WithTransaction`), `:53-66` (`ambient`, specifically `case tx.log != this.log:
  return nil, nil`).
- **What:** `WithTransaction` stores the `*Tx` under one **unkeyed** package-level context key,
  so a second `WithTransaction` for a *different* `*Log` replaces the first for every reader.
  `ambient` then sees a `*Tx` of another log, takes row one of §INV-041 — *nothing of this
  store's is bound* — and the operation runs on **autocommit**, while the caller's own
  transaction for that store is sitting in the same context, invisible. The framework's own
  mechanism for this, `crud.WithExecutorFor`/`ExecutorFor` (`crud/executor.go:341-354,370-373`),
  is a **chain keyed by the data source** precisely so that two sources compose; this is a
  single unkeyed `context.WithValue`.
- **Why this severity:** proven, not argued. From `/tmp/evrace/shadow_test.go` against this
  checkout:

  ```
  ctx = eventmemory.WithTransaction(ctx, txA)   // txA begun on storeA over logA
  ctx = eventmemory.WithTransaction(ctx, txB)   // txB begun on storeB over logB
  storeA.Append(ctx, …)                          → nil
  storeA.Transaction(ctx)                        → valid=false err=<nil>
  txA.Rollback(ctx); txB.Rollback(ctx)
  storeA.ReadStream(bg, stream, 0)               → 1 event

  logA kept 1 events after its transaction rolled back: storeA silently autocommitted
  ```

  A write the caller opened a transaction for was committed outside it, with no error at any
  door, and survived the rollback that was supposed to erase it. Two logs in one context is not
  exotic: §UC-007, §UC-054 and §UC-055 all make two backings in one operation ordinary, and
  §UC-028's own **Control** requires exactly *"two `Within` contexts chained for two different
  backings, with both stores' operations asserted to proceed"* — which this shape cannot pass.
  It also breaks §UC-046's **Must not happen** verbatim: *"An append must never run on autocommit
  while an executor for this store's backing is bound in the context"*, which is [[D-118]]'s
  standing half. `Store.Transaction` answering *"nothing of this store's is bound"* while this
  store's transaction is in the context is a false answer to the one question the contract
  (`event/store.go:140-153`) says has three answers and no fourth.
- **Why this timing:** it is the public shape of `WithTransaction`, S4's `Within`, marker chain
  and `Repo.Authority` are about to be built on top of `Store.Transaction`'s answer, and S5's
  conformance suite will be written to whatever this store does. Changing the key later changes
  a published contract and every test written against it. It is also a data-integrity finding
  (`data-integrity.md`: the transaction boundary), never deferrable.
- **Close criteria:**
  - [ ] the context binding is keyed by the backing it is for — `transactionKey{log *Log}`, or a
        chained binding in the shape of `crud.WithExecutorFor`/`ExecutorFor` — so two
        `WithTransaction` calls for two different logs both remain findable
  - [ ] `storeA.Append` on a context carrying both `txA` (logA) and `txB` (logB) stages into
        `txA`, and the events are absent after `txA.Rollback`
  - [ ] `storeA.Transaction(ctx)` on that context answers a valid authority naming `txA`
  - [ ] a regression test in `event/eventmemory/transaction_test.go` pins it, with the
        single-log case beside it as its control
  - [ ] the plan's `### event/eventmemory/` decision-table row *"A `*Tx` of another log stays row
        one"* is rewritten to say that a foreign `*Tx` does not shadow this store's own
- **Status:** closed 2026-09-07 — `event/eventmemory/transaction.go`: the binding is keyed by the `*Log` it was begun on (`transactionKey{log *Log}`), so two `WithTransaction` calls for two logs both stay findable and neither shadows the other; a nil `*Tx` binds under the log-less key and is found by every store that has no binding of its own, which keeps row three for the one binding that names no backing. `transaction_test.go` (`TestTwoLogsCarryTheirOwnTransactionsInOneContext`: the store names its own transaction, the two stores' authorities are asserted **not** `Same`, each append lands in the transaction of its own log, and the one-log case is the control). The plan's decision table gains the row. Verified by mutation: restoring the single unkeyed key turns both halves red — *"the first store answered that nothing of its own was bound while its own transaction was"* and *"the first log kept [ours] after the transaction it was appended in was rolled back"* — and leaves the control green

---

### GAP-2 [high][immediate] A `*Tx` touched from two goroutines panics with `assignment to entry in nil map`

- **Where:** `event/eventmemory/append.go:27` (`ambient` is consulted **before** the lock at
  `:36`) together with `transaction.go:111-117` (`release` sets `staged, counts = nil, nil`) and
  `transaction.go:119-123` (`stage` writes `this.counts[stream] += …`).
- **What:** check-then-act. `Append` reads `tx.finished` outside `log.mutex`, then acquires the
  mutex and calls `tx.stage`. If a `Commit` or `Rollback` wins the mutex in between, `counts` is
  already `nil` and the map write panics. `Tx.finished` being an `atomic.Bool` announces that
  concurrent access was intended; nothing in `doc.go`, in the plan or in `event/store.go`
  restricts a `*Tx` to one goroutine, and `database/sql`'s `*sql.Tx` — the shape §UC-028 names
  as the other binding route — answers `ErrTxDone` rather than crashing.
- **Why this severity:** proven. `/tmp/evrace/main_test.go`, one `Append` goroutine and one
  `Commit` goroutine over one `*Tx`, 200 attempts, `-race`:

  ```
  panic: assignment to entry in nil map
  github.com/frostgrove/vv/event/eventmemory.(*Tx).stage(...)
      event/eventmemory/transaction.go:122
  github.com/frostgrove/vv/event/eventmemory.(*Store).Append(…)
      event/eventmemory/append.go:62
  ```

  `event/store.go:135-137` states that a store's panic is **not recovered**, so this takes the
  request goroutine and, unrecovered, the process. Concrete inputs: an `errgroup` appending to
  two aggregates inside one transaction; a watchdog goroutine calling `tx.Rollback` on a deadline
  while the handler is still appending. The near-miss variant is quieter and worse in kind:
  `ReadStream` on the same interleaving reaches `stagedFor` on the released `*Tx`, reads a nil
  map and silently returns committed-only.
- **Why this timing:** it is a store-contract question — *may a `*Tx` be used from two
  goroutines* — that S5's conformance suite has to assert one way or the other, and `eventpg`
  must not be built to match a store that crashes. Deciding it after the suite exists means
  rewriting the suite.
- **Close criteria:**
  - [ ] the finished check and the staging happen under one acquisition of `log.mutex`, so a
        `*Tx` finished concurrently produces a refusal rather than a panic
  - [ ] `Append`, `ReadStream`, `Commit` and `Rollback` over one `*Tx` from N goroutines never
        panic, under `-race`, over at least 200 iterations
  - [ ] the answer for an append onto a concurrently-finished `*Tx` is the same one an append
        onto an already-finished `*Tx` gets today (`Failure(Refused, errFinished)`), and a test
        pins that the two paths agree
  - [ ] `doc.go` states what a `*Tx` promises about concurrent use, since the plan's C9 states it
        for the clock and for nothing else
- **Status:** closed 2026-09-07 — `event/eventmemory/append.go` and `read.go`: `ambient` is called **inside** `log.mutex`, so the liveness check and the staging are one acquisition and a transaction finished in the window is refused with `Failure(Refused, errFinished)` instead of writing to a released map. `release` already ran under that mutex, so under it `finished == false` implies the maps are live. `Transaction` still resolves without the lock, because it acts on nothing. `doc.go` states what a `*Tx` promises about concurrent use and that one context carries one transaction per log; the plan's decision table carries the same row. `transaction_test.go` (`TestOneTransactionUsedFromManyGoroutinesRefusesRatherThanCrashes`: 200 rounds of append ‖ read ‖ commit-or-rollback under `-race`, every non-nil answer compared byte for byte against the refusal a transaction finished **beforehand** produces). Verified by mutation: resolving before the lock reproduces `panic: assignment to entry in nil map` at `(*Tx).stage` ← `(*Store).Append`

---

### GAP-3 [medium][immediate] `Transaction` on a closed store answers a valid authority, against the contract it implements

- **Where:** `event/eventmemory/transaction.go:41-47`, against `event/store.go:150-153`.
- **What:** the contract says, in the sentence the kernel ships: *"A closed store answers the
  second rather than reporting closure, because this method writes and reads nothing."* The
  second answer is *an invalid authority and no error*. This implementation never consults
  `this.closed` and answers the **first** — a valid authority — when a `*Tx` of this log is
  bound. Measured: `closed store Transaction -> valid=true err=<nil>`.
- **Why this severity:** the divergence is one sentence away from the words the kernel already
  ships, and S5's `eventtest` will pin one of the two readings for every store, `eventpg`
  included. If the suite pins the contract's literal words, this store fails its own section; if
  it pins the store's behaviour, the kernel's sentence becomes false and the next reader of
  `event/store.go` implements the other one. No wrong data results today — the operation that
  follows refuses with `Closed` — which is why this is medium and not high.
- **Why this timing:** it changes a published contract that S4's `Within`/`Authority` and S5's
  suite are about to read.
- **Close criteria:**
  - [ ] one reading is chosen, and the loser is edited: either `Transaction` returns
        `event.Authority{}, nil` on a closed store, or `event/store.go:150-153` stops saying it
        must
  - [ ] the plan's six-answer decision table gains the row (it has none for a closed store)
  - [ ] a test in `event/eventmemory/transaction_test.go` pins the chosen answer, with an open
        store beside it as its control
- **Status:** closed 2026-09-07 — the contract was the loser, not the store. `event/store.go`: *"A closed store answers exactly what an open one would and never reports closure here, because this method writes and reads nothing and what the context carries did not change when the store was closed."* Answering *nothing of this store's is bound* while the caller's own transaction is in the context is a **fourth** answer to a three-answer question, and it contradicted the plan's own C10 row 4 (`Within` succeeds on a closed store with a transaction bound) — the two rows disagreed and row 5 is corrected with row 4. `transaction_test.go` (`TestTransactionAnswersWhatTheContextCarriesAfterTheStoreIsClosed`, with the open store, the nothing-bound case and the following `Append` refusing `Closed` as its controls). Verified by mutation

---

### GAP-4 [medium][immediate] A finished `*Tx` is refused at one read door and ignored at the other

- **Where:** `event/eventmemory/read.go:23-26` (`ReadStream` calls `ambient` and refuses) versus
  `read.go:49-59` (`ReadAll` never calls `ambient`).
- **What:** the plan's decision table says the store answers `Failure(Refused, …)` *"at **both**
  doors"* when a caller drives it past the kernel's own check. `refuse`'s two doors are
  `appendDoor` and `readDoor` (`event/errors.go:183-188`), and `ReadAll` is a `readDoor`
  operation. It is not covered. Measured, with a rolled-back `*Tx` in the context:
  `ReadStream -> event: the store reported [outcome refused] ; ReadAll -> <nil>`.
- **Why this severity:** one context, two reads of the same store, two different verdicts about
  the same wiring mistake. A caller that discovers its transaction is dead through `ReadStream`
  and not through `ReadAll` cannot write one recovery path; `eventtest` cannot assert *"a
  finished transaction is refused"* without naming which read it means; and `eventpg`, which
  will resolve its executor per statement, will not reproduce the asymmetry.
- **Why this timing:** the plan's own words are the specification S5 will encode, and they say
  both doors.
- **Close criteria:**
  - [ ] either `ReadAll` consults `ambient` and refuses a `*Tx` of this log that is finished or
        nil, or the plan's row is narrowed to name `Append` and `ReadStream` and to say why a
        position-ordered read is transaction-independent
  - [ ] whichever is chosen, a test pins it for both reads, with the live-transaction case as its
        control
- **Status:** closed 2026-09-07 — `event/eventmemory/read.go`: `ReadAll` consults `ambient` and refuses a `*Tx` of this log that is finished or nil, so one context produces one verdict at all three doors. It still returns no staged envelope to anyone — a staged envelope has no position — and the comment says which of the two the consultation is about. `transaction_test.go` (`TestATransactionThisStoreCannotUseIsRefusedAtEveryDoor`: the finished and the nil binding × `Append`/`ReadStream`/`ReadAll`, with a live transaction served at all three as the control). Verified by mutation

---

### GAP-5 [medium][immediate] The store's own defaults refuse a log at the kernel's own `MaxPayload` ceiling

- **Where:** `event/eventmemory/store.go:12-16` (`defaultStreamPage = 256`,
  `defaultMaxRead = 256`) and `:76-87` (`resident`), with `log.go:11-14`
  (`defaultMaxPayload = 64 << 10`).
- **What:** the five defaults are not calibrated against each other across the legal range of the
  numbers they multiply. Measured:

  ```
  NewLog(LogSpec{MaxPayload: event.MaxPayloadBytes})   // 1 MiB — the kernel's own ceiling
  New(Spec{Log: log})                                  // every operational number left at zero
  → event: this store is not the one this value was minted over, or it did not state its own
    bounds: StreamPage 256 at MaxPayload 1048576 holds more than the resident ceiling of
    67108864 bytes
  ```

  A consumer who sets exactly one legal number gets a construction failure naming a knob it never
  touched. The plan's sentence *"No store the kernel would have admitted is refused"* is true of
  the arithmetic and false of the defaults.
- **Why this severity:** it is a `universality.md` finding — five magic numbers whose derivation
  is stated nowhere and whose product is unchecked against the range of the factor the caller is
  most likely to raise. It is loud rather than silently wrong, which is why it is medium; the
  literals are the mechanism, which is why it is immediate. `MaxPayloadBytes = 1 << 20` and
  `MaxResidentBytes = 64 << 20` are the kernel's and already carry their derivation at
  `event/bounds.go:3-8`; the store's five do not.
- **Why this timing:** `universality.md` findings are never deferred, and `eventpg` will copy
  this defaulting shape in phase 2.
- **Close criteria:**
  - [ ] either the operational defaults are derived from the log's `MaxPayload` so that
        `New(Spec{Log: any legal log})` always succeeds, or `NewLog` refuses a `MaxPayload` no
        default store can be built over, or the refusal names the number the caller **did** set
        and says which one to lower
  - [ ] each of the five defaults carries, in the plan or at the line that chose it, the sentence
        that says where the number comes from
  - [ ] a test constructs a store over a log at `event.MaxPayloadBytes` and asserts the stated
        outcome
- **Status:** closed 2026-09-07 — `event/eventmemory/store.go`: the page default is `min(defaultPage, event.ResidentPage(log.MaxPayload))`, so `New(Spec{Log: any log the kernel admits})` always succeeds — at `event.MaxPayloadBytes` it yields the 64 envelopes one read may hold instead of refusing over a knob the caller never touched. `resident`'s refusal now names the number the caller **did** set and the number it may not pass. All five defaults carry their derivation: two on `log.go`'s `const` block, two on `store.go`'s, and the table in the plan's `### event/eventmemory/` block. `store_test.go` (`TestAStoreIsBuiltOverEveryLogTheKernelWouldAdmit`). Verified by mutation: a flat 256 refuses a store over a log at half the kernel's payload ceiling

---

### GAP-6 [medium][immediate] The resident-bytes rule is re-derived inside the extension because the kernel exports the constant and no predicate

- **Where:** `event/eventmemory/store.go:73-87`, whose comment claims to be *"exactly what the
  kernel refuses at `Bind` and `Read`, one door earlier"*.
- **What:** the kernel exports `MaxResidentBytes` (`event/bounds.go:15`) and no function that
  applies it. The store therefore re-derives the rule — as `MaxResidentBytes / MaxPayload`
  compared against each page bound. The kernel's own version does not exist yet (`event/binding.go`
  and `event/reader.go` are S4), so a comment asserting agreement with unwritten code is
  unverifiable today, and `eventpg` will write a third copy in phase 2.
- **Why this severity:** `architecture.md`'s one-format-place rule — the same rounding of one
  domain quantity in more than one place. The divergence mode is loud (a store boots and the
  kernel refuses it at `Bind`), so medium rather than high; but it is an extension-point defect:
  an extension is obliged to reimplement kernel policy the kernel does not offer.
- **Why this timing:** S4 is about to write the second copy. Extracting the predicate before that
  costs one exported function; after it costs an edit to two packages plus whatever `eventpg`
  has copied.
- **Close criteria:**
  - [ ] the kernel exposes the rule once — an exported predicate over `event.Limits` — and both
        `eventmemory.New` and S4's `Bind`/`Read` call it
  - [ ] the comment at `store.go:73-75` no longer asserts agreement with code it cannot see, or
        the agreement is asserted by a test that runs both
  - [ ] a test pins that a `Limits` the kernel refuses is refused by `New`, and one it admits is
        admitted, at the boundary value
- **Status:** closed 2026-09-07 — `event/bounds.go` gains `ResidentPage(maxPayload int) int`, the one place `MaxResidentBytes` becomes a count; it guards its own divisor, because `Limits` is a store's data. `eventmemory` calls it for its page default **and** its refusal, and the plan's `Bind`/`Read` step 4 is rewritten to call it too, so S4 writes no second copy. The comment at `store.go` no longer asserts agreement with code it cannot see — it says the rule is the kernel's, applied one door earlier so the refusal can name the numbers. `store_test.go` pins the boundary: `event.ResidentPage(MaxPayloadBytes)` admitted, that page plus one refused with `ErrWrongStore`. Verified by mutation: a store that doubles the kernel's own answer admits a page the kernel would refuse. **Not** taken: a `Limits`-shaped predicate. The store needs the number itself for its default, `Bind` checks five numbers of which this is one, and one exported division serves both without either party phrasing the other's refusal

---

### GAP-7 [medium][deferred] An abandoned `*Tx` bricks its streams for the life of the process

- **Where:** `event/eventmemory/transaction.go:111-117` (`release` is the only writer that
  deletes from `log.claims`) and `:119-123` (`stage` is the only writer that adds).
- **What:** a `*Tx` that is never committed and never rolled back — the request goroutine
  panicked, a `defer` was forgotten, an early `return` skipped it — keeps its claims forever.
  Every later append to those streams, transactional or autocommit, gets `Conflict` from
  `claimedByAnother`, and the caller's §UC-022 obligation (reload and decide again) can never
  clear it. Its staged envelopes are retained too. There is no timeout, no reclamation and no
  way to observe it.
- **Why this severity:** `doc.go:14-17` states the starvation for a transaction *held open across
  a network call* — recoverable, because that transaction does finish. It does not state the
  permanent case. The failure a consumer sees is one aggregate that refuses every write for the
  rest of the process, reported as a retryable-looking conflict class. Medium because
  `Persistence: Unsupported` and this is not a production store; deferred because closing it is
  a design choice (a finaliser, a claim generation, or nothing plus a stated degradation) rather
  than a defect in what is written.
- **Why this timing:** no contract another section depends on changes either way.
- **Close criteria:**
  - [ ] `doc.go` states that an abandoned transaction's claims are never reclaimed, or the store
        reclaims them
  - [ ] the plan's **Degradation** paragraph for `eventmemory` says the same
- **Status:** closed 2026-09-07 — one sentence rather than a mechanism, which is what the finding asked for first. `event/eventmemory/doc.go` states that a transaction never committed and never rolled back holds its claims and its staged records for the life of the process, that nothing reclaims them, and that every later append to those streams is refused as a conflict that will never clear; the plan's **Degradation** paragraph and the S3 section say the same. No reclamation is added: a finaliser or a claim generation is a design choice this store does not need at `Persistence: Unsupported`

---

### GAP-8 [low][deferred] An empty `Append` returns `nil` without checking `Expected`

- **Where:** `event/eventmemory/append.go:31-33`.
- **What:** `Append` short-circuits on `len(req.Records) == 0` before the version check.
  Measured: `Append(Expected: 999, Records: nil)` on a stream at version 1 → `nil`.
  `event/store.go:165-166` says a store *"admits the batch if and only if the stream is at
  `req.Expected`"* and says nothing about an empty one.
- **Why this severity:** the kernel never issues an empty append (S4 short-circuits it), so no
  caller reaches this through `Repo`. But `Store` is a published, directly callable API that
  `doc.go:1-4` calls *"a complete implementation of the store contract and not a test double"*,
  and `eventtest` may issue one. `nil` from `Append` reading as "the stream was at Expected" is
  false here and may be true on a SQL store that issues the statement anyway.
- **Why this timing:** no section depends on it; the shape is safe as long as it is written down.
- **Close criteria:**
  - [ ] `event/store.go` states what an empty `AppendRequest` means for a store, or the store
        checks `Expected` before the short circuit
  - [ ] whichever is chosen, one test pins it
- **Status:** deferred, carried to the plan's `## Debt` — owner **S5**. The fix is a sentence on `event/store.go`'s `Append` and not code, and the two candidate sentences differ in what they cost `eventpg` (a round trip for a no-op the kernel never sends). Recorded with the standing constraint that no conformance case may issue an empty append until it is written down, so the suite cannot encode whichever answer this store happens to give

---

### GAP-9 [low][deferred] `ReadStream` turns an out-of-range `after` into "the end of the stream"

- **Where:** `event/eventmemory/read.go:33-36` (`first := int(after)` then `if first < 0`).
- **What:** `after` is an `event.Version`, a `uint64`. Above `math.MaxInt` the conversion is
  negative and the store answers an empty page with a nil error. `event/store.go:157-160` makes
  a short page *"the end of the stream"* and says the kernel issues no confirming read, so an
  out-of-range cursor reads as a complete, empty history rather than as a refusal. Measured:
  `ReadStream after version 2^63 -> 0 envelopes, err=<nil>`. On a 32-bit build the truncation is
  worse in kind — `int(1<<32 + 5)` is `5`, which would return a page starting at version 6.
- **Why this severity:** not reachable through the kernel, which counts up from zero, and a
  64-bit build cannot hold a stream long enough for the truncation case. Low, and cheap.
- **Why this timing:** internal, no contract impact.
- **Close criteria:**
  - [ ] an `after` the store cannot index is answered with `Failure(Refused, …)` or the
        comparison is done in `uint64` so no conversion is needed
- **Status:** closed 2026-09-07 — `event/eventmemory/read.go`: the end-of-stream comparison is done in `event.Version`, so no conversion is needed and none is made until `after` is known to be inside the slice. The 32-bit truncation and the negative `int` both disappear rather than being detected

---

### GAP-10 [low][deferred] Two comments restate contracts the kernel already ships

- **Where:** `event/eventmemory/read.go:78-81` (on `handOut`, a **4-line** function) and
  `read.go:11-15` (first two clauses).
- **What:** `CLAUDE.md` allows a comment only for *"a genuinely complex function of roughly 40+
  lines … or an invariant the code cannot make visible"*, and calls removing one that fails the
  rule *"expected cleanup, not a regression"*. `handOut`'s four lines carry a four-line comment
  that paraphrases `event/store.go:96-99` (*"Yours to give away, including its capacity"*);
  `ReadStream`'s opening clause paraphrases `event/store.go:161-162`'s read-your-own-writes
  sentence. The rest of the package's comments pass — `Append` is 45 lines, and `publish`,
  `Log`, `Close`, `ambient`, `Commit`, `revalidate` and `mintCursor` each state a decision or an
  invariant the code cannot make visible.
- **Why this severity:** style, and a second copy of one prose contract that will drift from the
  kernel's when the kernel's is edited.
- **Why this timing:** cosmetic.
- **Close criteria:**
  - [ ] `handOut`'s comment is removed or reduced to the part that is this store's own decision
  - [ ] `ReadStream`'s comment keeps the staged-position decision and drops the restatement
- **Status:** closed 2026-09-07 — `event/eventmemory/read.go`: `handOut`'s four-line comment is one line and states only this store's own reason (the log keeps every envelope it published), and `ReadStream`'s opening restatement of the read-your-own-writes contract is gone; the staged-position decision stays. `CLAUDE.md` calls this cleanup rather than a regression, and both comments were inside functions the other closures were rewriting

---

### GAP-11 [low][deferred] Three undocumented zero-value behaviours, none of them in the plan's contract block

- **Where:** `event/eventmemory/store.go:66-69` (`Clock == nil → time.Now`), `log.go:16-19` and
  `store.go:18-29` (every `int` at zero silently becomes a default), `store.go:110-118`
  (`Check` also returns `ctx.Err()`).
- **What:** the plan's `### event/eventmemory/` block writes `// zero means the store's default`
  on `LogSpec.MaxPayload` and nowhere else, says nothing about a nil `Clock`, and describes
  `Check` as *"`event.ErrClosed` on a closed store and nil otherwise"*. The code defaults six
  zero values and adds a third `Check` answer. Defaulting a nil clock to `time.Now` is an
  established repo idiom (`jobs/schedule.go:247-249`, `storage/storageminio/backend.go:127`,
  `tenancy/authority.go:69`, `health/registry.go:50`), so the behaviour is right; what is missing
  is that a consumer reading `Spec` cannot tell `Clock` is optional, and a consumer who leaves it
  nil in a test gets wall-clock timestamps with no signal.
- **Why this severity:** no wrong result, and the house comment rule is the reason none of it is
  written at the struct. `docs/modules/` is where this belongs, and S6 owns it.
- **Why this timing:** S6 is the section that writes the consumer reference.
- **Close criteria:**
  - [ ] `docs/modules/{en,ru}/event.md` states every zero-value default of `LogSpec` and `Spec`,
        including the clock
  - [ ] the plan's contract block matches the code on the clock and on `Check`'s three answers
- **Status:** deferred, carried to the plan's `## Debt` — owner **S6**. The plan-side half is closed in this edit: the contract block now writes `zero means the store's default` on every field that has one, `zero means time.Now` on `Clock`, and `Check`'s three answers in the order it gives them. What is left is `docs/modules/{en,ru}/event.md`, which is where a consumer reads it and which the house comment rule keeps out of the struct

---

### GAP-12 [low][deferred] No documentation exists for a package that is complete

- **Where:** `docs/modules/` (no `event.md` in either language), `docs/ai/flows/Index.md` (no
  reverse-index rows for `event/eventmemory/*.go`), `docs/ai/decisions/Index.md` (none of the
  five planned ADRs), `docs/roadmaps/Roadmap.md`.
- **What:** `grep -rln "eventmemory" docs/` → **0 files**. `CLAUDE.md` is explicit that a doc is
  updated *"in the same change as the code. Never 'later'"*, and that an index which does not
  list a file is worse than a missing file.
- **Why this severity:** the plan parks all of it in S6, which is written out in detail and names
  each file, so nothing is dropped — this is a recorded schedule, not a loss. Recorded here so
  the S3 gate does not read as evidence that the obligation was met.
- **Why this timing:** S6 owns it and S6 has not run.
- **Close criteria:**
  - [ ] S6 lands `docs/modules/{en,ru}/event.md`, the flow(s) and reverse-index rows for
        `event/eventmemory/*.go`, and the five ADRs
  - [ ] `docs/roadmaps/Roadmap.md` and the postgres-event-sourcing roadmap reflect what phase 1
        closed
- **Status:** deferred, carried to the plan's `## Debt` — owner **S6**, which names each file. Recorded rather than closed so the S3 gate does not read as evidence that `CLAUDE.md`'s same-change obligation was met; `grep -rln "eventmemory" docs/` is still zero files

---

### What is clean, with the check that says so

- **Contract conformance.** The exported surface is the plan's, symbol for symbol
  (`go doc -all`, 8 package-level + 12 methods). `var _ event.Store = (*Store)(nil)`
  (`store.go:38`) is present and compiles. All eight contract methods, `Check`, `Begin`,
  `WithTransaction`, `Commit`, `Rollback` exist with the planned signatures.
- **Microkernel.** Zero kernel references to the extension, proven by grep above. A second store
  needs `Stream`, `Version`, `Position`, `Cursor`, `Envelope`, `AppendRequest`, `Record`,
  `Limits`, `Capabilities`, `Outcome`, `Failure`, `NewBacking`, `NewAuthority` and the bounds
  constants — **every one of them exported**, which is exactly the property the redesign
  demanded. The only kernel edit phase 2 would want is GAP-6's predicate.
- **`Backing.Equal` is not a second answer.** `NewBacking(log)` hands the `*Log` pointer to
  `crud.SameDataSource` (`crud/executor.go:562-572`), which is type-check plus `==`. The log's
  `fingerprint` is a *cursor* identity, not a second backing identity, and is minted once in
  `NewLog` from the same value — the two cannot disagree.
- **Payload ownership (C7, §INV-021 hand-offs 4 and 7).** `Append` clones every inbound payload
  (`append.go:54`); `handOut` clones every outbound one (`read.go:83`); both pages are freshly
  allocated at `len == cap` (`read.go:38`, `:68`). The mutation spot-check above confirms the
  test that pins it actually fails when the clone is removed.
- **Positions, versions and monotone visibility.** Positions are assigned inside the single
  critical section that publishes (`log.go:83-90`), so commit order is position order and no
  lower position can arrive after a cursor. `Rollback` burns rather than reissues
  (`transaction.go:92`). Verified under a 16-goroutine `-race -count=5` stress: strictly
  increasing positions and dense per-stream versions across ~480 events per run.
- **Conflict reporting.** From `Append`, never from `Commit` (`append.go:39-45`,
  `transaction.go:72-84`), which is what makes §UC-022's caller branch fire on this store as it
  will on a SQL one. Two concurrent autocommit appends at one version produce exactly one
  receipt and one `Conflict` (their own test, and my stress).
- **Error discipline.** No `errors.Is` by string anywhere; every refusal is
  `event.Failure(outcome, cause)` with the cause an unexported package sentinel. No message
  carries a key, a version, a payload byte or a cursor: the seven sentinels
  (`append.go:11-14`, `transaction.go:11-15`, `cursor.go:13-16`) are all constant prose, and the
  only formatted errors are configuration numbers at construction (`log.go:60-70`,
  `store.go:41-42,76-87`).
- **Restrictions.** No third-party dependency, no new `go.mod`, no `go.work` edit
  (`check-workspace: ok`, `check-replaces: ok`, `check-tidy: ok`). No `TODO`, `FIXME`, `nolint`
  or `t.Skip` under `event/` (`check-todo: ok`). No process-wide logger, no env read, no
  telemetry, no `panic`/`recover`, no mutated input argument, no write outside the store's own
  state. Randomness is one `crypto/rand.Text()` at log construction and is what makes a foreign
  cursor detectable.
- **Readability.** Every function is a guard-clause cascade; maximum nesting 2; no function over
  45 lines; no flag parameter; names are domain names (`admitted`, `staged`, `claimedByAnother`,
  `burn`-by-`publish`, `handOut`, `revalidate`).

---

## Round 1 — dispositions, 2026-09-07

All twelve findings are answered; none is closed by silence and none is rejected.
Nine are repaired in the code, three are carried to
`.agents/artifacts/plans/EVENTSOURCE_P1_PLAN.md` `## Debt` with the argument for
each. Two of the repairs changed the **kernel**, which is why they are named
here and in the S3 section's `**Files**` line: `event/bounds.go` gains
`ResidentPage`, and `event/store.go`'s closed-store sentence on `Transaction` is
corrected. The contract sections `#### event/bounds.go`, `### event/eventmemory/`
(the surface block, the nine-answer decision table, the defaults table and
**Degradation**), C10 row 5, `Bind`'s step 4 and both S3 checkpoint transcripts
were changed in the same edit.

| Finding | Grade | Disposition |
|---|---|---|
| GAP-1 | `[critical][immediate]` | **fixed** — the binding is keyed by the `*Log`, so one context carries one transaction per backing and no store answers *nothing of mine is bound* while its own transaction is there |
| GAP-2 | `[high][immediate]` | **fixed** — the two doors that act resolve the transaction inside the section that acts; a concurrently finished `*Tx` is refused, never a nil-map panic |
| GAP-3 | `[medium][immediate]` | **fixed in the contract** — a closed store answers exactly what an open one would; the kernel's sentence said *the second* and contradicted the plan's own C10 row 4 |
| GAP-4 | `[medium][immediate]` | **fixed** — `ReadAll` consults the ambient transaction, so one context gets one verdict at all three doors |
| GAP-5 | `[medium][immediate]` | **fixed** — the page default is capped by the resident rule, so every log the kernel admits builds a store with nothing set; all five defaults carry their derivation |
| GAP-6 | `[medium][immediate]` | **fixed** — `event.ResidentPage` is the one copy of the division, called by the store and by S4's `Bind`/`Read` |
| GAP-7 | `[medium][deferred]` | **fixed anyway** — one sentence in `doc.go` and one in the plan's **Degradation**; no reclamation mechanism added |
| GAP-8 | `[low][deferred]` | **carried to `## Debt`**, owner S5, with the constraint that no conformance case may issue an empty append until the contract says what one means |
| GAP-9 | `[low][deferred]` | **fixed anyway** — the comparison is done in `event.Version`, so the conversion that could truncate no longer exists |
| GAP-10 | `[low][deferred]` | **fixed anyway** — both comments were inside functions the other closures were rewriting |
| GAP-11 | `[low][deferred]` | **half closed** — the plan's contract block now matches the code on all three; `docs/modules/{en,ru}/event.md` is carried to `## Debt`, owner S6 |
| GAP-12 | `[low][deferred]` | **carried to `## Debt`**, owner S6 |

### What the fixes turned up that the review did not name

**The closed-store question had already been decided twice, differently, inside
the plan.** C10 row 4 says `Within` **succeeds** on a closed store when a
transaction of its backing is bound; C10 row 5 says `Repo.Authority` answers *the
invalid authority, nil error* on a closed store. Both delegate to
`Store.Transaction`, so they cannot both hold. GAP-3 is the third statement of the
same question and the store was the only one of the three that was self-consistent.
Row 5 now reads with row 4, which is why this closure edits the contract rather
than the store: a store obeying the sentence as written would have made `Within`
unusable on the very path C10 row 4 exists to keep open.

**GAP-1's nil-`*Tx` row had to be kept deliberately, and keying alone loses it.**
A binding keyed by the backing has nowhere to put a transaction that names no
backing, and dropping it would have made `WithTransaction(ctx, nil)` invisible to
every store — the same escape GAP-1 is about, one route further out. It binds
under the log-less key instead, and a store consults that key only when it found
no binding of its own, so a real transaction is never shadowed by a mistaken one.

**GAP-2's fix moves foreign code out of no critical section, and one call is now
made on a path that refuses.** `Spec.Clock` is still read **before** the lock, so
a slow or blocking clock cannot stall every reader and writer of the log; the
price is that an empty or refused append calls it once and discards the answer.
That is the deliberate side of the trade and is stated here rather than left as
an oddity in the diff.

---

## Round 2 — econv-impl-reviewer (clean context, re-audit) — 2026-09-07

Read in full before touching the code: `CLAUDE.md`, the plan's `### event/eventmemory/`
contract block (surface, the nine-answer decision table, the five-defaults table,
**Degradation**), § S3, § Architecture metrics, § Breaches, § Debt (GAP-8/11/12 rows);
`.agents/artifacts/usecases/EVENTSOURCE_P1_USECASES.md` §INV-009, INV-021, INV-032, INV-034,
INV-035, INV-036, INV-038, INV-041; `docs/roadmaps/2026-09-01-postgres-event-sourcing-roadmap.md`
(non-negotiable invariants, forbidden packages, module boundary); every non-test file under
`event/` and `event/eventmemory/`; and
`~/.claude/skills/econv/references/{microkernel,architecture,building-blocks,data-integrity,universality,readability,restrictions,gaps}.md`.

Every probe below ran from a throwaway out-of-tree module at `/tmp/evprobe` with a `replace`
onto this checkout. Two mutation spot-checks touched the repository and were restored
byte-identically: `transaction.go` `sha256 10540b97…4be82` and `append.go`
`sha256 c9905ccd…c2a49`, both verified before and after. `git status --short -- event/` at the
end is unchanged from the start.

### Checkpoint verification — I ran it, it is real

| Clause | Result here |
|---|---|
| `go build ./... && go vet ./event/... && test -z "$(gofmt -l event)" && go test -race -count=1 ./event/...` | `ok …/event 1.247s`, `ok …/event/eventmemory 1.015s`, `EXIT=0` |
| `go test -list '.*' ./event/eventmemory/ \| grep -c '^Test'` | **14**, matching the plan's re-run paste |
| the eight-name phase-5 count clause | `COUNT OK` (8) |
| `go test -race -count=3 ./event/...` then `-count=1 -shuffle=on` | green both times — the 200-round transaction race is stable, not lucky once |
| `make check` | nine arms, all `ok` |
| `make unit` | `EXIT=0`, zero `FAIL` lines across every module |
| `gofmt -l .` (whole tree) | silent |

### Round 1's twelve findings — verified against behaviour, not against the wording

| Round 1 | Verified how | Verdict |
|---|---|---|
| GAP-1 shadowed transaction | `/tmp/evprobe` binds `txA` (logA) then `txB` (logB); `storeA.Append` → `nil`, `storeA.Transaction` → **valid**, and after `txA.Rollback` `storeA.ReadStream` reads **0** events. Mutation: restoring one unkeyed key turns `TestTwoLogsCarryTheirOwnTransactionsInOneContext` red with the two messages the plan pastes, word for word | **closed** |
| GAP-2 nil-map panic | 300 rounds of `Append ‖ ReadStream ‖ ReadAll ‖ Commit-or-Rollback` over one `*Tx`, `-race`: no panic. Mutation: hoisting `ambient` back above the lock reproduces `panic: assignment to entry in nil map` at `(*Tx).stage ← (*Store).Append:63` | **closed** — and it is what GAP-13 below is about |
| GAP-3 closed-store `Transaction` | `open valid=true` / `closed valid=true` / `open.Same(closed)=true`; `event/store.go:150-153` now says so | **closed** |
| GAP-4 one verdict at every door | finished `*Tx` and nil `*Tx` × `Append`/`ReadStream`/`ReadAll` → `[outcome refused]` at all six | **closed** |
| GAP-5 defaults refuse a legal log | `MaxPayload` ∈ {1, 1 Ki, 64 Ki, 512 Ki, 1 Mi} all build with nothing operational set → pages 256, 256, 256, 128, **64** | **closed** |
| GAP-6 re-derived resident rule | `event.ResidentPage` is the one division; `eventmemory` calls it for the default **and** the refusal | **closed** |
| GAP-7 abandoned claims | `doc.go:17-20` states it; plan's **Degradation** states it | **closed** |
| GAP-8 empty append | still `Append(Expected: 999, Records: nil)` on a stream at 1 → `nil`. Carried in `## Debt`, owner S5 | **open, deferred, carried** |
| GAP-9 out-of-range `after` | `ReadStream(after: 2^63)` → 0 envelopes, `nil`; the `int` conversion is now made only after `after` is known to be inside the slice | **closed** |
| GAP-10 restating comments | `handOut`'s comment is one line and is this store's own reason; `ReadStream`'s restatement is gone | **closed** |
| GAP-11 zero-value defaults | plan half closed; `docs/modules/{en,ru}/event.md` carried, owner S6 | **open, deferred, carried** |
| GAP-12 no docs | `grep -rln "eventmemory" docs/` → **0 files**. Carried, owner S6 | **open, deferred, carried** |

No round-1 closure is a rewording: every one of them changes an answer I measured, and the two
I mutated turn red when reverted.

### Architecture metrics — counted here, not copied

*Size.* Seven non-test files, **595** lines: `transaction.go` 152, `store.go` 122, `log.go` 94,
`read.go` 87, `append.go` 64, `cursor.go` 42, `doc.go` 34. Threshold 400 — no breach.

*Functions.* **27**. Longest **45** (`Store.Append`), then 33 (`ReadStream`), 33 (`New`),
31 (`ReadAll`), 22 (`NewLog`). Threshold 50 — no breach. Maximum nesting depth **2** everywhere
(measured as brace depth 3 inside a `func`, in `Append`, `ReadStream`, `ambient`, `revalidate`,
`stagedFor`). No flag parameter, no mode switch, no boolean argument on the surface. Maximum
parameter count 4 (`bound`).

*Public surface.* `go doc -all` yields **8** package-level symbols (`Log`, `LogSpec`, `NewLog`,
`Spec`, `Store`, `New`, `Tx`, `WithTransaction`), **10** `*Store` methods, **2** `*Tx` methods.
Symbol for symbol against the plan's `### event/eventmemory/` block: **no silent extra surface,
nothing marked done that is missing.** Two cosmetic differences, neither a contract change:
`ReadStream`'s parameter is `stream` where the plan writes `s`, and `Commit`/`Rollback` take an
unnamed `context.Context`.

*Coupling.* Internal imports per file: **1** (`event`) in all six code files. Total imports per
file 4, 4, 4, 4, 5, 4, 0 — threshold 5, no breach. `go list -deps ./event/eventmemory` =
`utils, crud, errs, event, eventmemory`; no new `go.mod` (`find event -name go.mod` → nothing),
no third-party dependency, `check-replaces`, `check-tidy` and `check-workspace` all `ok`.
Longest access chain `this.log.streams[stream]` — 2 dots, at the threshold. Import cycles 0.

*Microkernel.* `grep -rn "eventmemory" event/*.go | grep -v _test` → **0**.
`grep -rniE "if name ==|registry|func init\(\)|switch store" event/*.go | grep -v _test` → **0**.
Stronger than a grep: I declared a **complete second `event.Store` in a package outside this
repository** (`/tmp/evprobe/second_store_test.go`, 60 lines: its own `Backing` through
`NewBacking`, its own `Authority` through `NewAuthority`, its own `Limits` through
`ResidentPage`, `Failure(Conflict|BadCursor, …)`) — it compiles, satisfies
`var _ event.Store`, appends and reads, and needed **zero** edits to any file under `event/`.
The file phase 2 would have to change is **none**.

*Isolation.* Fakes to unit-test each object: `Log` **0**, `Store` **0–1** (a `func() time.Time`),
`Tx` **0**. Relocation: the package already is its own package and imports one internal package.
Deletion: removing `Tx` touches `transaction.go`, six lines of `append.go` and six of `read.go`.
No `init`, no goroutine started by a constructor, no package-level mutable state — the eight
package `var`s are seven immutable `errors.New` sentinels plus `var _ event.Store`.

*Purity.* No logger, no `os.Getenv`, no `otel`, no `panic`/`recover`, no I/O. Randomness is one
`crypto/rand.Text()` at `NewLog`; the clock is one injected `func() time.Time` read only in
`Append`. Both reads and the fold path are pure.

*Universality.* Every string and numeric literal in the package, enumerated: three import-path
and package-name strings, seven sentinel messages (all constant prose, none carrying a key, a
version, a payload byte or a cursor), five `fmt.Errorf` formats over configuration numbers,
`cursorSeparator = ":"`, and four calibrated defaults — `64 << 10`, `512`, `64`, `256` — each
with its derivation on the `const` block that holds it and in the plan's defaults table.
**No event-type name, no family name, no payload shape, no `parts[2]`, no `len(rows) == n`, no
fixture path.** `mintCursor`/`readCursor` are the single format place for the cursor and
round-trip: `ReadAll` → `"…:1"` → resume → `"…:1"`, asserted equal.

*Concurrency beyond `-race`.* 16 goroutines × 40 rounds over 4 contested streams across **two
store values on one log**, mixing autocommit appends, transactional appends, commits and
rollbacks, then a full `ReadAll` tiling and a per-stream read: positions **strictly increasing**
across 559 committed events, per-stream versions **dense from 1**, no cross-stream leakage,
green under `-race`. Payload ownership (C7): a caller mutating its own array after `Append`
leaves the store reading `"original"`; a reader writing into the page it was handed leaves the
store reading `"original"`; the page comes back `len == cap`.

---

### GAP-13 [medium][immediate] GAP-2's fix moved the caller's own `Value` implementation inside the log-wide mutex

- **Where:** `event/eventmemory/append.go:29-35` and `event/eventmemory/read.go:21-27` — in both,
  `this.log.mutex.Lock()` precedes `this.ambient(ctx)`, and `ambient`
  (`transaction.go:63-75`) begins with `ctx.Value(transactionKey{log: this.log})` and may make a
  second `ctx.Value(transactionKey{})`.
- **What:** `context.Context` is an interface the **caller** implements or wraps, so `ctx.Value`
  is foreign code, and it is now executed while the one mutex every reader and writer of the log
  contends for is held. Round 1's disposition states the opposite in writing — *"GAP-2's fix
  moves foreign code out of no critical section"* — and the plan's decision-table row keeps only
  the part that has to be inside (*"resolve it **inside the section that acts**"*). The liveness
  check that GAP-2 actually needed under the lock is `tx.finished.Load()`; the **lookup** did
  not have to move.
- **Why this severity:** proven, in `/tmp/evprobe/lock_test.go`. A context whose `Value` blocks
  (a caching wrapper behind a mutex, a `Value` that consults a remote-ish cache, or simply a
  deep chain) is entered by one `Append`; a second goroutine's completely unrelated
  `ReadStream` on a **different stream** is then blocked for as long as that `Value` runs:

  ```
  an unrelated ReadStream was BLOCKED for 300ms:
  the caller's ctx.Value runs inside the store-wide mutex
  ```

  The head-of-line block is the ordinary harm; the hard one is an AB-BA deadlock. A caller whose
  context decorator takes lock `L` in `Value`, and any other goroutine that holds `L` across a
  `store.Append`, hangs the entire log permanently — `event/store.go:135-137` does not recover a
  store's panic and there is nothing to recover here anyway, because nothing panics. It is a
  regression rather than an original defect: before GAP-2's closure the lookup was above the
  lock. Medium rather than high because the deadlock needs a locking `Value` and the blocking
  case needs a slow one, and because every store in the tree is in-process.
- **Why this timing:** the fix is one line per door and it changes nothing observable, which is
  only true while `Append` and `ReadStream` are the only two doors written. S5's conformance
  suite will run the store under deliberately defective decorators, some of which wrap the
  context, and `eventpg` is being told by the plan's decision table to copy *"resolve it inside
  the section that acts"* — which for a SQL store means resolving an executor while holding
  whatever it holds. Naming the split now costs a sentence; naming it after `eventpg` copies the
  row costs an edit to two packages.
- **Close criteria:**
  - [ ] the `ctx.Value` lookup happens **before** `this.log.mutex.Lock()` at every door, and only
        the `finished` check and the staging remain inside the critical section
  - [ ] `ReadAll` resolves the ambient transaction the same way the other two doors do — one
        concept, one resolution point (today it is the only door that resolves it outside the
        lock, `read.go:58`)
  - [ ] `TestOneTransactionUsedFromManyGoroutinesRefusesRatherThanCrashes` still goes red when
        the `finished` check is hoisted back out of the lock, so the split did not undo GAP-2
  - [ ] a test with a context whose `Value` blocks asserts that an unrelated operation on the
        same log completes while it is blocked
  - [ ] the plan's decision-table row for GAP-2 says which half is inside the section that acts
        and which half is not, and round 1's *"moves foreign code out of no critical section"*
        sentence is corrected
- **Status:** open

---

### GAP-14 [medium][immediate] What a read returns inside a transaction is stated for `ReadStream` and left open for `ReadAll`, and the two stores will answer it differently

- **Where:** `event/store.go:116-121` (`Log.ReadAll`, which says nothing about a bound
  transaction) against `:158-164` (`ReadStream`, which states read-your-own-writes explicitly);
  `event/eventmemory/read.go:47-50` and `:11-12`, which decide both answers for this store.
- **What:** two facts a consumer can observe are decided in the extension and stated nowhere in
  the kernel:
  1. **`ReadAll` inside a bound transaction.** `eventmemory` returns no staged envelope to
     anyone, its own transaction included. A SQL store issuing `SELECT … ORDER BY position`
     **on the transaction it joined** returns its own uncommitted rows by construction — that is
     what joining means under §INV-041's second row, and `eventpg` would have to add a
     deliberate second connection to avoid it.
  2. **`Envelope.Position` on a staged envelope.** `eventmemory` answers **0**; measured, a page
     inside a transaction comes back `version 1 position 1`, `version 2 position 2`,
     `version 3 position 0`, `version 4 position 0`. A store whose positions come from a sequence
     assigns them at insert, so the same page there carries real positions.
- **Why this severity:** `Envelope` is handed to the consumer whole (§INV-038, and S4's
  `Reader.Events()` returns the page), so both facts are consumer-visible, and the divergence is
  silent in the direction that matters: a projector or outbox drain that a caller happens to run
  inside its own write transaction reads **nothing** on `eventmemory` and **its own uncommitted,
  possibly-rolled-back events** on a SQL store — with positions it may checkpoint past.
  §INV-034's *"an event that is permanently visible to one read and not the other"* is the
  neighbouring hazard and this is how a store gets there without breaking a stated rule. Medium
  rather than high because phase 1 ships only the store that answers the safe way, and because
  nothing in `event/` reads `Position` yet.
- **Why this timing:** it is a public contract other sections are about to depend on. S4's
  `Reader` walks `ReadAll`, and S5's suite will certify *whatever this store happens to do* —
  which is the exact failure mode GAP-8 was deferred with a standing constraint to avoid. One
  clause on `Log.ReadAll` and one on `Envelope.Position` are cheap now and are a phase-2
  contract change later.
- **Close criteria:**
  - [ ] `event/store.go`'s `Log.ReadAll` states whether a read issued while a transaction of this
        store's backing is bound may return that transaction's uncommitted events, or states
        that it is unspecified and that no kernel path may run a global read inside a write
        transaction
  - [ ] `event/store.go` states what `Envelope.Position` means on an envelope a store returns
        that is not yet committed — a value, or explicitly unspecified
  - [ ] the plan's decision-table rows for `ReadAll` and for the staged position are marked as
        **this store's answer to a question the kernel now settles**, rather than as the answer
  - [ ] S5's `global paging` / `resumption` inventory records whether either question is
        certified or is one of the things a store is only *allowed* to do
- **Status:** open

---

### GAP-15 [medium][deferred] `errNotTransaction`'s text describes the answer it is not giving

- **Where:** `event/eventmemory/transaction.go:12`, returned only at `:66-68`.
- **What:** the message is `"eventmemory: this context carries no transaction of this store's for
  this store"`. The branch that returns it is §INV-041's **third** row — *something of this
  store's is bound and it is not a transaction* — reached when a log-less binding
  (`WithTransaction(ctx, nil)`) exists and this log has none of its own. The sentence says the
  opposite, *carries no transaction of this store's*, which is row **one**, the row whose answer
  is autocommit and no error; and it says "for this store" twice.
- **Why this severity:** the cause is reachable through `event.CauseOf` and is the only text a
  consumer gets on the one path the design added to be **loud**. An operator reading
  *"carries no transaction … for this store"* in a log next to a `[outcome refused]` concludes
  nothing was bound and looks for the missing `WithTransaction`, when the actual mistake is a
  `WithTransaction(ctx, nil)` — usually an unchecked `Begin` error two frames up. Medium because
  it misdirects diagnosis on a fail-closed path; no wrong data results.
- **Why this timing:** an error string, internal to this package, that no other section reads or
  matches on. `errors.Is` against it is impossible from outside because it is unexported, so
  nothing will bend around it.
- **Close criteria:**
  - [ ] the message names what was found — a binding that names no log and is not a transaction —
        rather than what was not found
  - [ ] it carries no key, version, payload byte or cursor (it does not today either)
- **Status:** open

---

### GAP-16 [low][deferred] `Commit` has two errors, not one, and the second leaves the transaction live and still claiming

- **Where:** `event/eventmemory/transaction.go:81-93` (`Commit` returns `errFinished` at `:85`
  and whatever `revalidate` returns at `:87-89`), `:109-118` (`revalidate` → `errStaleClaim`),
  against the plan's decision-table row *"Both errors stay **unexported**: each method has
  exactly one, so *non-nil* carries the whole of it."*
- **What:** the justification for leaving `Commit`'s error unexported is that the method has one
  error. It has two, and they mean opposite things: `errFinished` says *you already finished
  this transaction* — a caller's `defer tx.Rollback()` beside an explicit commit produces it and
  correctly ignores it — while `errStaleClaim` says *the invariant the claim exists to hold did
  not hold, nothing was published*. A caller cannot tell them apart without comparing strings,
  which `CLAUDE.md` forbids. Worse, the `errStaleClaim` return happens **before** `release()`, so
  the transaction stays `finished == false` and keeps every claim it took; a caller that treats a
  non-nil `Commit` as "already done" and returns leaves those streams refusing every write for
  the life of the process — GAP-7's permanent starvation reached through an error path instead
  of through an abandoned `defer`.
- **Why this severity:** `errStaleClaim` is **unreachable today** and I checked rather than
  assumed: `stage` claims every stream it stages to, `claimedByAnother` refuses both an
  autocommit append and another transaction's append on a claimed stream, and a second
  transaction therefore cannot advance a stream a live transaction holds — so
  `log.version(stream)` cannot move between admission and commit. It is a defensive assertion,
  which is why this is low rather than medium. The defect is that the plan's stated reason for
  the API shape is false and that the guard's own failure path is the worst outcome it could
  choose.
- **Why this timing:** `Commit` and `Rollback` are `eventmemory`'s own surface, not
  `event.Store`'s, so nothing else is built on them and `eventpg` will not inherit the shape.
  Nothing is rewritten by fixing it later.
- **Close criteria:**
  - [ ] the plan's row no longer claims `Commit` has one error, or `Commit` has one error
  - [ ] the `errStaleClaim` path either releases the claims or `doc.go` states that it does not
        and why
  - [ ] a caller can tell the two apart without comparing strings, or the plan states why it need
        not
- **Status:** open

---

### GAP-17 [low][deferred] `doc.go` does not say what `WithTransaction(ctx, nil)` does to every other store in that context

- **Where:** `event/eventmemory/transaction.go:31-36` (an undocumented exported function) and
  `event/eventmemory/doc.go:22-25`, which states the per-log rule and stops there.
- **What:** a nil `*Tx` binds under the log-less key, and `ambient` consults that key for
  **every** store that found no binding of its own — including stores over completely unrelated
  logs. Measured: with only `WithTransaction(ctx, nil)` in the context, `Append`, `ReadStream`
  and `ReadAll` all answer `[outcome refused]`, on every store. That is the right answer and the
  plan argues it well (*"a binding that could have been meant for anyone must fail closed for
  everyone"*); it is written in the plan's decision table and in no place a consumer reads.
- **Why this severity:** the reaching mistake is ordinary — `tx, err := store.Begin(ctx)`
  followed by `ctx = eventmemory.WithTransaction(ctx, tx)` before `err` is checked — and the
  symptom is every event operation in that request refusing, including operations on a different
  log that the caller never associated with the failed `Begin`. It fails closed and it is loud,
  so nothing is silently wrong; what is missing is the sentence that turns three minutes of
  confusion into none.
- **Why this timing:** documentation, and S6 already owns the package's consumer-facing pages
  (GAP-11, GAP-12, both carried in `## Debt`).
- **Close criteria:**
  - [ ] `doc.go` states that a nil transaction names no log, that every store with no binding of
        its own finds it, and that this is deliberate
  - [ ] `docs/modules/{en,ru}/event.md` says the same where a consumer reads it
- **Status:** open

---

### GAP-18 [low][deferred] The plan's architecture-metrics table is stale for four of the six files, and carries no breach row for the concrete store's ten methods

- **Where:** `.agents/artifacts/plans/EVENTSOURCE_P1_PLAN.md` lines 4022-4027 and the
  `### Breaches, each justified in writing` table below them.
- **What:** two things. First, four of the six *actual* line counts predate round 1's closures:
  `log.go` 90 → **94**, `store.go` 118 → **122**, `read.go` 84 → **87**, `transaction.go`
  143 → **152**; `append.go` 64 and `cursor.go` 42 are right. Nothing breaches 400 either way —
  the table's numbers are simply not the code's. Second, `architecture.md`'s *public methods per
  class ≤ 7* is breached by `*eventmemory.Store` at **10** (the eight of `event.Store`, plus
  `Check` and `Begin`) and the breach table carries a row only for the `event.Store` **port** at
  8. Both extra methods are argued at the contract block — `Check` is §UC-008's `health.Probe`
  shape satisfied structurally, `Begin` is the transaction opener the port deliberately excludes
  — so the substance is justified in writing; the row that would let a reader find that
  justification from the metrics table is not there.
- **Why this severity:** cosmetic in effect. No threshold is actually exceeded on size, and the
  method-count breach has a real argument sitting two sections away. `architecture.md` grades an
  **unjustified** breach `high`; this one is justified in the wrong place.
- **Why this timing:** the plan is edited again in S4, S5 and S6, when the same table gains rows
  for `binding.go`, `repo.go`, `reader.go` and the suite files, and re-measuring six files costs
  one `wc -l`.
- **Close criteria:**
  - [ ] the four line counts match `wc -l`
  - [ ] the breach table carries a row for `*eventmemory.Store` at 10 public methods, pointing at
        the `Check` and `Begin` arguments already written
- **Status:** open

---

### GAP-19 [low][deferred] The injected clock is read on every `Append` call, including the ones that record nothing

- **Where:** `event/eventmemory/append.go:27`, above the lock and above the ambient check, the
  empty-batch short circuit and both conflict checks.
- **What:** `recorded := this.clock()` runs and is discarded whenever the append is refused
  (`Refused`, either `Conflict`) or empty. Round 1's disposition names this as the deliberate
  price of keeping a slow clock out of the critical section — correctly — but it is recorded only
  in the GAPS file. `Spec.Clock`'s own comment says *"called on every append"*, which is true of
  every `Append` **call** and reads as every append **recorded**.
- **Why this severity:** the only party that can see it is a test clock that counts, and the
  visible effect is a gap in the timestamps of a deterministic fixture — a puzzle, not a wrong
  answer. Wall-clock and any monotone clock are unaffected.
- **Why this timing:** no contract depends on it and S5's suite injects its own clock, where the
  behaviour is at worst surprising.
- **Close criteria:**
  - [ ] `Spec.Clock`'s comment says the clock is read once per `Append` call rather than once per
        recorded append, or the read moves below the checks and the plan states the critical-section
        trade it accepts
- **Status:** open

---

### What is clean, with the check that says so

- **Contract conformance.** 8 package-level symbols + 12 methods, symbol for symbol against the
  plan. `var _ event.Store = (*Store)(nil)` present and compiling (`store.go:42`). Nothing on the
  surface the plan does not declare; nothing the plan declares that is missing.
- **Microkernel.** Zero kernel references to the extension, and a **complete second store written
  outside this repository** compiles and runs against the published kernel with zero diffs to
  `event/`. `Stream`, `Version`, `Position`, `Key`, `Cursor`, `Envelope`, `Record`,
  `AppendRequest`, `Limits`, `Capabilities`, `Support`, `Outcome`, `Failure`, `NewBacking`,
  `NewAuthority`, `ResidentPage` and the six ceilings are all reachable and constructible from
  another package — which is the property the redesign demanded and it holds.
- **`Backing.Equal` is not a second answer.** `NewBacking(log)` hands the `*Log` to
  `crud.SameDataSource` (`crud/executor.go:562-571`: nil check, type equality, `==`). The log's
  `fingerprint` is a **cursor** identity minted once in `NewLog`; nothing compares backings with
  it.
- **Payload and page ownership (C7, §INV-021 hand-offs 3, 4, 7).** Inbound cloned at
  `append.go:54`, outbound cloned at `read.go:85`, both pages allocated fresh at `len == cap`
  (`read.go:36`, `:73`). Measured from outside: mutating the caller's array after `Append` and
  writing into a handed-out page both leave the store reading `"original"`.
- **Positions, versions, ordering.** Positions assigned inside the one critical section that
  publishes (`log.go:87-93`); `Rollback` burns rather than reissues (`transaction.go:101`).
  Verified over 559 concurrent commits across two store values on one log: strictly increasing
  positions, dense per-stream versions from 1, no cross-stream leakage. `MonotoneVisibility:
  Supported` is honest — nothing can commit below the newest position because the assignment and
  the publish are one section.
- **Cursors.** `mintCursor`/`readCursor` are one format place and round-trip. A foreign cursor,
  `"garbage"`, `":"`, `"AAAA:notanumber"`, `"AAAA:1:2"` and `"AAAA:-1"` are all
  `Failure(BadCursor, …)`; the empty cursor is the start of the log and resumes correctly from an
  empty log.
- **Transaction rule (§INV-041, [[D-118]]).** Row one — a transaction of another log — is
  autocommit, which §INV-041 requires verbatim (*"for this store those are the first row"*).
  Row two joins. Row three refuses before anything is written. Two logs in one context both stay
  findable and their authorities compare **not** `Same`; two stores over **one** log share the
  transaction and compare `Same`.
- **Error discipline.** No comparison by string anywhere. Every refusal is
  `event.Failure(outcome, cause)` with an unexported package sentinel as the cause; no message
  carries a key, a version, a payload byte or a cursor. Cancellation travels bare at all five
  operating doors (`context canceled` from `Append`, `ReadStream`, `ReadAll`, `Check`, `Begin`),
  and `Commit`/`Rollback` deliberately do not read it.
- **Restrictions.** No third-party dependency, no new module, no `go.work` edit. No `TODO`,
  `FIXME`, `nolint`, `t.Skip` or `//nolint` under `event/`. No process-wide logger, no env read,
  no telemetry, no goroutine started by a constructor, no global mutable state, no mutated input
  argument, no write outside the store's own state. Tests are `package eventmemory_test` and
  reach no unexported field; no `testdata` path is referenced from a non-test file.
- **Readability.** Every function is a guard-clause cascade returning early; maximum nesting 2;
  longest function 45 lines; no flag parameter; names are domain names (`admitted`, `staged`,
  `claimedByAnother`, `publish`, `handOut`, `revalidate`, `mintCursor`, `release`). Comments are
  decisions and invariants rather than restatements — the two round 1 named are gone, and I
  found no new one that restates its code.
