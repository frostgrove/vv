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

---

## Round 3 — remediation, cluster `ctx-value-under-the-log-mutex` — 2026-09-08

The three findings this cluster carries — GAP-13, GAP-14 and GAP-7 — were
reproduced first from a throwaway out-of-tree module at `/tmp/vvprobe` with a
`replace` onto this checkout, so the transcripts below are this worktree's and
not a rerun of the reviewer's. Each fix was then verified by mutation: the
source was broken, the named test watched to go red, and the file restored and
its sha256 compared.

### Reproduced before the fix

```
an unrelated ReadStream on a DIFFERENT stream waited 580.8ms
while the appending caller's ctx.Value ran                       ← GAP-13

autocommit append to the same stream after the transaction was abandoned:
                                          event: the store reported [outcome conflict]
a second transaction's append to the same stream:
                                          event: the store reported [outcome conflict]
control, an unclaimed stream:             <nil>                  ← GAP-7

ReadAll inside the transaction that staged one event: 0 envelopes, cursor "…:0"
staged envelope read back inside the transaction: version 1 position 0
                                                                 ← GAP-14, both answers
                                                                   decided in the extension
```

### Answered after it

```
an unrelated ReadStream on a DIFFERENT stream waited 0.0ms
while the appending caller's ctx.Value ran

autocommit append to the same stream after the transaction was abandoned: <nil>
a second transaction's append to the same stream:                         <nil>
control, an unclaimed stream:                                             <nil>
```

| Finding | Grade | Disposition |
|---|---|---|
| GAP-13 | `[medium][immediate]` | **closed by splitting `ambient` in two.** `ambient` is the `ctx.Value` lookup and runs **before** `this.log.mutex.Lock()` at all three doors; the new `Tx.live` is the liveness check and is the only half inside the section that acts. `ReadAll` resolves it the same way as the other two, so one concept has one resolution point. `Store.Transaction` reads both without the lock, because it acts on nothing |
| GAP-14 | `[medium][immediate]` | **closed in the kernel.** `event/store.go`'s `Log.ReadAll` now states that what a global read answers inside a bound transaction is **unspecified** — a store reading through the transaction it joined returns its uncommitted events, one whose global order is assigned at commit returns none, both conformant — that the kernel runs no global read inside a write transaction, and that a cursor from one must not be persisted. `Envelope.Position` states that it means nothing before the append carrying it committed, and that `Version` is not in that boat |
| GAP-7 | `[medium][deferred]` | **closed by a mechanism, not only the sentence round 1 added.** `log.claims` holds a `weak.Pointer[Tx]`. A transaction nobody can reach can never be committed, so its staged records will never be published and the streams it took are released the moment the runtime collects it — the next append to one of them drops the dead claim and is admitted. Stdlib only, no module, no `go.work` edit |

### Round 1's sentence, corrected

Round 1's dispositions say *"GAP-2's fix moves foreign code out of no critical
section, and one call is now made on a path that refuses."* The first clause was
**wrong** and GAP-13 is what it cost: `Spec.Clock` stayed above the lock, but the
same fix moved `ctx.Value` — foreign code the caller implements — below it. The
sentence should have read *the clock is still outside the section; the context
lookup moved inside it, and should not have.* The second clause still holds: an
empty or refused append reads the clock once and discards the answer. Round 1 is
left as written, as the ledger requires; this is the correction.

### Why GAP-7 got a mechanism after round 2 had closed it with a sentence

The close criteria offered three doors — the doc, a finaliser or a `Log`-level
sweep, or a suite case — and round 2 took the doc. The remediation list carries
the finding anyway with the reviewer's failing input attached, and a fix that
does not make that input pass is not a fix. A finaliser cannot work here on its
own: `claims[stream] = tx` makes the transaction **reachable from the log**, so
nothing would ever be collected and no cleanup would ever run. Making the claim
weak removes the retention and the reclamation in the same line, and it also
stops the log pinning an abandoned transaction's staged payloads. The one
non-determinism it adds is the collection's timing, it is one-way — a claim is
never *taken* by a collection, only released — and it is declared in the plan's
**Degradation** paragraph.

### Zero-diff obligation

Nothing under `event/` gained or lost an exported symbol: `make api` regenerates
`docs/api/surface.md` with no change attributable to this round. `ambient`,
`Tx.live`, `Log.claim` and the weak claim are unexported, and every value
`eventpg` must construct is still constructible from outside package `event`.
GAP-14 is deliberately the loosest clause that keeps both stores conformant: a
kernel that had *required* the committed-only answer would have forced `eventpg`
to open a second connection to serve `ReadAll`.

### Verified by mutation — seven applied, seven killed, every file restored byte-identically

```
Append resolves the ambient under the      → door 0 of the log never answered while an
  lock again                                 append was still inside its own caller's
                                             ctx.Value  (TestACallersOwnContextIsNever…)
ReadStream resolves it under the lock      → same, "…while a stream read was still inside…"
ReadAll resolves it under the lock         → same, "…while a log walk was still inside…"
the liveness check hoisted back out of     → panic: assignment to entry in nil map
  the critical section (GAP-2's defect)      (TestOneTransactionUsedFromManyGoroutines…)
the claim names its transaction strongly   → "appending to a stream claimed by a transaction
  again                                      nobody can reach any more answered [outcome
                                             conflict] after 8 collections"
no claim is ever held (the control)        → "appending to a stream a live transaction has
                                             claimed was admitted"
a staged envelope carries a position       → "the staged event reads back at position 2 beside
                                             the committed one at 1, and this store's answer
                                             to the position the kernel leaves unspecified
                                             before a commit is zero"
```

`TestOneTransactionUsedFromManyGoroutinesRefusesRatherThanCrashes` still goes red
on the fourth mutation, which is GAP-13's own close criterion: the split did not
undo GAP-2.

### Tests left behind

- `TestACallersOwnContextIsNeverRunInsideTheLogsLock`
  (`event/eventmemory/concurrency_test.go`) — a context that parks inside its own
  `Value`, driven through **each** of the three doors in turn, with the other two
  and an append asserted to answer within five seconds while it is parked. It
  first asserts the parked call has **not** answered, so the window it measures
  is real.
- `TestAParkedContextStopsTheDoorItWasGivenTo` — the control: without it, a
  `Value` that never actually parks would let every assertion above pass while
  proving nothing.
- `TestAStreamIsClaimedOnlyWhileSomethingCanStillCommitIt`
  (`event/eventmemory/transaction_test.go`) — the abandoned case, with the
  control beside it: a transaction the caller still holds keeps its claim across
  eight collections, and the positive case asserts the abandoned one's staged
  records were **not** published when the claim went. Stable at `-count=20`
  under `-race` and under `-gcflags=all=-N`.
- `TestAReadInsideATransactionSeesItsOwnStagedAppends` gains the staged-position
  assertion, so this store's answer to the field the kernel now leaves
  unspecified is pinned rather than incidental.

### Docs and plan updated in the same change

`event/eventmemory/doc.go`, `docs/modules/{en,ru}/event.md`,
`docs/modules/{en,ru}/eventmemory.md`, `docs/ai/flows/FL-036` (three new notes,
two file-table rows, two **Proved by** rows), `docs/ai/usecases/modules/event/UC-032`
clause 13, and the plan's GAP-2 and staged-position decision-table rows, its
**Degradation** paragraph, its S3 mutation transcript and a new S5 paragraph
recording that `global paging` and `resumption` certify **neither** answer to
GAP-14's question. `event/eventtest/sections_read.go` says the same where the
sections are written.

---

## Round 4 — econv-impl-reviewer (clean context, remediation audit of cluster `ctx-value-under-the-log-mutex`) — 2026-09-08

Scope: the three findings the cluster carries — **GAP-13** (`ctx.Value` under the log-wide
mutex; `ReadAll` resolving the ambient at a second point), **GAP-14** (`Log.ReadAll` and
`Envelope.Position` inside a bound transaction unspecified in the kernel), **GAP-7** (an
abandoned `*Tx` bricking its streams). Verified against the code, not against round 3's
disposition notes. Every probe below ran from a throwaway out-of-tree module at
`/tmp/evprobe2` with a `replace` onto this checkout. Six mutations were applied in place and
each file restored byte-identically — `append.go` `sha256 2cf5c9f3…c9296`, `read.go`
`775715fa…3322f`, `log.go` `8a40cd0a…bb54a`, `transaction.go` `0422263e…f5c1e92`, `store.go`
`2536262c…a46d3`, all five compared before and after; `git status --short -- event/` is
unchanged from the start.

### Checks I ran myself

| Command | Result |
|---|---|
| `gofmt -l .` (whole tree) | silent |
| `go vet ./event/...` | clean |
| `go test -race -count=2 ./event/...` | `ok event 6.016s`, `ok event/eventmemory 1.488s`, `ok event/eventtest 4.011s` |
| `go test -race -count=3 ./event/...` then `-count=1 -shuffle=on ./event/...` | green both times |
| `go test -race -count=30 -run TestAStreamIsClaimedOnlyWhileSomethingCanStillCommitIt ./event/eventmemory/` | green — the GC-driven case is not flaky at 30 runs |
| the same at `-count=20 -gcflags=all='-N -l'` and under `GOGC=off` | green both |
| `make check` | nine arms, all `ok` |
| plan checkpoint clause `…=8` (`event/eventmemory`) | `COUNT-8 OK` |
| plan checkpoint clause `…=12` (`event/eventmemory`) | `COUNT-12 OK` |
| `go list -deps ./event/eventmemory` minus stdlib | `utils, crud, errs, event, eventmemory` — no third-party, `go.mod`/`go.work` `use` untouched (`weak` is stdlib since Go 1.24; this module declares `go 1.26`) |

No flake observed anywhere.

### Microkernel — derived here, not inherited

`grep -rn "eventmemory" event/*.go | grep -v _test` → **0**. Stronger: I wrote a **complete
second `event.Store` in a package outside this repository** (`/tmp/evprobe2/store.go`, ~120
lines: its own `Backing` through `NewBacking`, its own `Limits` through `ResidentPage`, its own
cursor format, `Failure(Conflict|BadCursor, …)`, `var _ event.Store`). It **builds with zero
diffs to anything under `event/`**, and `eventtest.Run` drives it unchanged and correctly
reports four sections *failed* for the four things I deliberately left out (page cap, close,
cancellation, foreign-cursor rejection). Exported surface of `eventmemory` after this round is
unchanged at **8** package-level symbols, **10** `*Store` methods, **2** `*Tx` methods;
`Log.claim` and `Tx.live` are unexported. **`event/eventpg` is still writable with zero diffs
under `event/`: yes.**

### Metrics — counted

Non-test files under `event/eventmemory`: `transaction.go` 166, `store.go` 122, `log.go` 114,
`read.go` 97, `append.go` 67, `cursor.go` 42, `doc.go` 38 — threshold 400, no breach. Longest
function `Store.Append` **48** (was 45), then `ReadStream` 36, `ReadAll` 35, `NewLog` 22 —
threshold 50, no breach, and `Append` is now within two lines of it. Maximum nesting depth 2.
Internal imports per file **1** (`event`). Import cycles 0. Longest access chain
`this.log.streams[stream]` — 2 dots. No global mutable state, no `init`, no logger, no env
read, no `TODO`/`FIXME`/`nolint`, no `t.Skip` outside the two places where `SkipNow` **is** the
subject under test (`eventtest/run_test.go:133`, `eventtest/suite_test.go:58` — both legitimate).
No new string, numeric or regex literal in non-test code; `collections = 8` is test-only and named.

### The three cluster findings, re-derived

**GAP-13 — closed.** All five close criteria met, and I proved each by mutation rather than by
reading.

| Mutation applied in place | Test that went red |
|---|---|
| `Append` resolves `ambient` under the lock again | `TestACallersOwnContextIsNeverRunInsideTheLogsLock/an append …` — *"door 0 of the log never answered while an append was still inside its own caller's ctx.Value"* |
| `ReadStream` and `ReadAll` resolve it under the lock again | the same test's *"a stream read"* and *"a log walk"* subtests, both |
| the liveness check hoisted back out of the critical section (GAP-2's original defect) | `TestOneTransactionUsedFromManyGoroutinesRefusesRatherThanCrashes` → `panic: assignment to entry in nil map` at `(*Tx).stage ← (*Store).Append:65`. **The split did not undo GAP-2** |

I also built the AB-BA case the finding named, which no test in the tree covers: a caller whose
context decorator takes lock `L` inside `Value`, and a second goroutine holding `L` across an
`Append`. Against the mutated store it **hangs the whole log** (`DEADLOCK … after 20s`);
against the code as it stands the same probe finishes in `0.202s`. `ambient` is called at
exactly four sites (`append.go:27`, `read.go:21`, `read.go:64`, `transaction.go:49`) and none is
under a lock; the other two pieces of foreign code, `ctx.Err()` and the injected `Spec.Clock`,
are both above the lock too. `ReadAll` now resolves the ambient exactly as the other two doors
do — one concept, one resolution point.

**GAP-14 — closed, with one residual carried below as GAP-21.** `event/store.go:130-138` states
the `Log.ReadAll` answer as unspecified and names both conformant shapes; `:91-97` states what
`Envelope.Position` means before a commit and that `Version` is not in that boat. The plan's
decision-table row (`EVENTSOURCE_P1_PLAN.md:2310`) is rewritten as *this store's answer to a
question the kernel now settles as the store's*; `eventtest/sections_read.go:154-158` records
that neither `global paging` nor `resumption` certifies either answer. Mutating
`TestAReadInsideATransactionSeesItsOwnStagedAppends` is not needed — the new assertion at
`transaction_test.go:90-92` pins this store's answer, and it is the first thing in the tree that
does. The loosest-clause choice is right: requiring the committed-only answer would have forced
`eventpg` to open a second connection, which is a kernel dictating an extension's plumbing.

**GAP-7 — closed by a mechanism, with one hole carried below as GAP-20.** Two mutations, both
killed:

| Mutation | Test that went red |
|---|---|
| the claim names its transaction strongly again (`map[Stream]*Tx`) | *"appending to a stream claimed by a transaction nobody can reach any more answered [outcome conflict] after 8 collections"* |
| no claim is ever consulted (the control) | *"appending to a stream a live transaction has claimed was admitted, and it is what the store is supposed to refuse"* |

The reachability argument holds where it applies: a `*Tx` nobody can reach cannot be committed,
so releasing its claims on collection can never publish a record or admit an append at a version
a committable transaction also staged, and `errStaleClaim` stays unreachable for the same reason
round 2 gave. The hole is *who* can still reach it, which is GAP-20.

### What is clean, with the check that says so

- **No behaviour was widened and no assertion weakened.** The refusal precedence at `ReadAll` is
  unchanged (`Refused` still beats `BadCursor`; I checked the order, not the diff).
  `Store.Transaction` answers `(Authority{}, errFinished)` for a finished `*Tx` exactly as it did
  when `ambient` carried that check. A nil `*Tx` still reaches `live()` on a nil receiver
  legally and answers nil, and `WithTransaction(ctx, nil)` still refuses at all three doors.
- **Every map touch on `Log` is under `log.mutex`** — `claim`, `claimedByAnother` (including its
  `delete`), `publish`, `version`, `release`, `stage`, `stagedFor`, `stagedCount`; `Tx.finished`
  is the one field read outside it and it is an `atomic.Bool`.
- **Payload and page ownership are untouched**: inbound clone `append.go:57`, outbound clone
  `read.go:95`, pages allocated at `len == cap`.
- **Docs moved with the code, as `CLAUDE.md` requires.** `doc.go`, `docs/modules/{en,ru}/eventmemory.md`,
  `docs/ai/flows/FL-036` (two file-table rows naming `claim`, `claimedByAnother`, `ambient`,
  `Tx.live`, and two **Proved by** rows) and the plan all say the same thing. Every test name
  they cite exists: `TestACallersOwnContextIsNeverRunInsideTheLogsLock`,
  `TestAParkedContextStopsTheDoorItWasGivenTo`,
  `TestAStreamIsClaimedOnlyWhileSomethingCanStillCommitIt`,
  `TestAClaimCoversOnlyTheStreamsATransactionWroteTo` — all four found.

---

### GAP-20 [medium][immediate] The weak claim is defeated by the receipt the kernel itself hands back, and three documents state the reclamation with no condition on it

- **Where:** `event/eventmemory/log.go:81-83` and `:92-103` (the weak claim) against
  `event/authority.go:13-16` and `:33` — `Authority.identity` is a plain `any` holding the
  store's own `*Tx` — reached through `event/repo.go:92` and `:101`, which put that authority
  into every non-empty `Commit` receipt, and `event/token.go:34` and `:47`, which hand it to the
  caller. The unconditional claims are at `event/eventmemory/doc.go:17-22`
  (*"holds its claims and its staged records **only until the runtime collects it**"*),
  `docs/modules/en/eventmemory.md:75-81` and `ru:76-82` (*"an abandoned transaction is a stall
  and not a brick"*), and `.agents/artifacts/plans/EVENTSOURCE_P1_PLAN.md:2274-2287`
  (*"holds nothing once nobody can reach it"*).
- **What:** the mechanism is reachability, and the kernel publishes a strong reference to the
  `*Tx` as part of its normal write result. A caller that retains a `Commit` receipt — an audit
  buffer, an outbox row, a slice of receipts two subsystems compare later, which is exactly the
  §UC-030 use `token.go:28` names — keeps the `*Tx` alive, so its claims and its staged payloads
  are never reclaimed. Round 3's own justification (*"it also stops the log pinning an abandoned
  transaction's staged payloads"*) is false in that shape. `Repo.Authority(ctx)` returns the same
  value through a second door.
- **Why this severity:** reproduced, deterministically, in `/tmp/evprobe2`. A transaction is
  begun, staged to `orders.order acme/A-17`, and abandoned; the frame returns **only** the
  `event.Authority` the store answered for it — byte for byte what `Repo.Append` puts in the
  receipt. Eight `runtime.GC()` calls later, appending to that stream still answers
  `event: the store reported [outcome conflict]`, and it will for the life of the process:

  ```
  PINNED: appending to the stream of an abandoned transaction whose authority
  the caller still holds answered event: the store reported [outcome conflict]
  after 8 collections
  ```

  Delete `runtime.KeepAlive(held)` from that probe and the append is admitted, so the pin is the
  receipt and nothing else. The resulting behaviour is precisely GAP-7's original defect — one
  aggregate refusing every write forever, reported as a retryable-looking `Conflict` a §UC-022
  reload can never clear — reached through the one value the kernel is designed to hand out.
  Medium rather than high because it is the *pre-existing, already-accepted* degradation rather
  than a new wrong answer; what is new is three documents that deny it and a test that asserts
  only the case where nothing retains the transaction.
- **Why this timing:** the plan's **Degradation** paragraph is a written contract that phase 2's
  author reads and that `eventpg`'s own degradation section will be modelled on, and
  `docs/modules/{en,ru}/eventmemory.md` is where a consumer decides whether an abandoned
  transaction is a stall or a brick. A sentence costs nothing now; a store author who copied
  *"nobody can reach it"* as a general property has to be corrected later, in two packages and
  four documents.
- **Close criteria:**
  - [ ] `doc.go`, both `docs/modules/*/eventmemory.md` and the plan's **Degradation** paragraph
        state that the reclamation is reachability-based and that anything still naming the
        transaction — a retained `event.Commit` receipt, an `event.Authority`, a context that
        outlives the request — keeps its claims, **or** the store stops depending on reachability
        for this
  - [ ] a test beside `TestAStreamIsClaimedOnlyWhileSomethingCanStillCommitIt` covers the
        retained-receipt case and asserts whichever answer the sentence above promises, so the
        two cases are told apart rather than one standing for both
  - [ ] the plan's round-3 claim that the weak claim *"stops the log pinning an abandoned
        transaction's staged payloads"* is corrected or qualified
- **Status:** open

---

### GAP-21 [medium][deferred] The consumer half of the `ReadAll`-inside-a-transaction rule is written only where a store author reads it, and the kernel's own sentence about itself is ambiguous

- **Where:** `event/store.go:130-138` — the clause lives on `Log.ReadAll`, the interface a
  **store implementer** satisfies. The party it binds holds `*event.Reader`:
  `event/reader.go:27-33` (`Read`), `:48-57` (`Next`) and `:62` (`Cursor`) say nothing about a
  bound transaction. The same sentence is repeated at
  `docs/ai/flows/FL-036-a-decision-becomes-a-recorded-fact.md:230-235`.
- **What:** two things. First, *"a consumer that does may not checkpoint the cursor it comes back
  with"* is the one clause that prevents the failure GAP-14 named — a projector drain run inside
  a write transaction checkpointing past events a rollback then discards — and it is written on
  the store contract, not on the value the consumer holds and calls `Cursor()` on. Second,
  *"The kernel runs no global read inside a write transaction"* reads as a guarantee and is not
  one: `Reader.Next` **is** kernel code issuing the global read, on whatever context the consumer
  hands it, and nothing in `Read`, `Next` or `Cursor` refuses, warns or records that the context
  carried a transaction. What is true is the narrower *no kernel-initiated path opens a
  transaction and then reads globally*.
- **Why this severity:** on `eventmemory` a drain inside a write transaction silently reads
  nothing; on a store that joins the transaction it reads its own uncommitted rows and
  `Reader.Cursor()` returns a cursor past them. A consumer that persists that cursor and then
  rolls back skips those events permanently — §INV-034's *"an event that is permanently visible
  to one read and not the other"* reached without breaking a stated rule. Medium because phase 1
  ships only the store that answers the safe way and the rule *is* written down somewhere; the
  defect is that it is written in the doorway the wrong party walks through.
- **Why this timing:** a doc sentence on an already-shipped S4 type. Nothing is rewritten by
  adding it later and no contract another section is about to depend on changes shape.
- **Close criteria:**
  - [ ] `Reader.Next` or `Reader.Cursor` carries the consumer's obligation — a cursor from a walk
        issued while a transaction of this backing is bound must not be persisted — or `Read`
        refuses such a context outright and the plan says which was chosen and why
  - [ ] `event/store.go`'s sentence distinguishes *the kernel initiates no global read inside a
        write transaction* from *`Reader.Next` will run one on any context it is given*
  - [ ] `docs/modules/{en,ru}/event.md` states the same where the consumer of `Read` reads it
- **Status:** open

---

### GAP-22 [low][deferred] `ReadAll` moved cursor parsing *into* the critical section GAP-13 asked to shrink

- **Where:** `event/eventmemory/read.go:75-78`. Before this round `readCursor` ran at what was
  `read.go:58-62`, above `this.log.mutex.Lock()`; it now runs inside it.
- **What:** GAP-13's first close criterion reads *"only the `finished` check and the staging
  remain inside the critical section"*. `readCursor` (`cursor.go:26-42`) reads only
  `this.fingerprint`, which is written once in `NewLog` and never again, so it needs no lock —
  and a `strings.Cut` plus a `strconv.ParseUint` over caller-supplied text now runs while every
  reader and writer of the log waits. The same round also moved the refusal of a malformed
  cursor from *before* the lock to *after* it: a caller replaying a corrupt checkpoint in a loop
  now contends for the log on every attempt.
- **Why this severity:** no foreign code is involved, so there is no deadlock and no unbounded
  hold — the work is a few hundred nanoseconds over a string the caller supplied and the store
  bounds nothing about its length. Cosmetic in effect, and the direction is the one the cluster
  was opened to reverse.
- **Why this timing:** module-internal, no contract, and moving it back is three lines.
- **Close criteria:**
  - [ ] `readCursor` runs before `this.log.mutex.Lock()`, or a comment at the call site says why
        it is inside a section that only the liveness check and the staging were meant to hold
  - [ ] the refusal precedence is unchanged either way — `Refused` before `BadCursor`, which is
        what `TestATransactionThisStoreCannotUseIsRefusedAtEveryDoor` pins today
- **Status:** open

---

### GAP-23 [low][deferred] `ambient`'s comment still explains a rule `ambient` no longer applies

- **Where:** `event/eventmemory/transaction.go:59-62`, on a function whose body is now
  `transaction.go:69-78`.
- **What:** the first paragraph reads *"A transaction of another log is nothing of this store's,
  so its operations run on this store's own autocommit. **A finished one is not:** the caller
  holds a context that reads like a transaction and is not one, and answering it with autocommit
  is the escape the transaction rule exists to close."* After the split, `ambient` returns a
  finished `*Tx` with a nil error and decides nothing about it; the rule the sentence states is
  implemented in `Tx.live` (`:84-89`), which carries its own comment. The house rule is that a
  comment exists only where it carries something the code cannot — a comment that describes a
  neighbouring function's rule is the failure mode that rule guards against.
- **Why this severity:** cosmetic, and it misleads only a reader who trusts the comment over the
  five lines beneath it.
- **Why this timing:** an unexported function's comment; nothing reads it and nothing bends
  around it.
- **Close criteria:**
  - [ ] the finished-transaction rule is stated once, where it is implemented
  - [ ] `ambient`'s remaining comment says only what `ambient` does — the two keys, and why the
        lookup is above the lock
- **Status:** open

---

### GAP-24 [low][deferred] `claimedByAnother` is spelled as a predicate and mutates the map

- **Where:** `event/eventmemory/log.go:92-103`, the `delete(this.claims, stream)` at `:99`.
- **What:** the name is a question and the body answers it *and* drops the entry it found dead.
  The comment above it argues the lazy drop well (*"A claim is dropped where it is found rather
  than swept, because the only reader is the append that was about to be refused by it"*), and
  the argument is right; the name is what does not say so. `release` and `stage` are the package's
  two other writers of `claims` and both read as commands, which is the shape this one breaks.
  A second consequence, not a defect on its own: a claim left by a collected transaction on a
  stream nobody appends to again is never removed, so `claims` keeps one dead entry per such
  stream for the life of the log.
- **Why this severity:** local, correct, and covered by a comment. Command-query separation in an
  unexported six-line helper is a readability preference, not a behaviour question.
- **Why this timing:** internal naming, no contract.
- **Close criteria:**
  - [ ] the function's name says it may drop a dead claim, or the drop moves to the caller that
        acts on the answer
- **Status:** open

---

### Verdict for this cluster

**GAP-13 closed** (five of five criteria, four proved by mutation and one by a deadlock probe the
tree has no test for). **GAP-14 closed** (four of four criteria; GAP-21 is the residual, and it
is a doorway question rather than a reopening). **GAP-7 closed by a mechanism** (two of two
criteria, both proved by mutation) **but the mechanism has the hole GAP-20 names.** One
`[medium][immediate]` is open, so the cluster is not green.

---

## Round 5 — remediation of round 4, cluster `ctx-value-under-the-log-mutex` — 2026-09-08

Round 4 left one `[medium][immediate]` (GAP-20) and four deferred findings, three of
which round 3's own fixes introduced (GAP-21, GAP-22, GAP-23) and one of which its
rename opportunity created (GAP-24). All five are closed. GAP-20 was reproduced
before the fix from a throwaway out-of-tree module at `/tmp/evprobe3` with a
`replace` onto this checkout, driving the **kernel's** own `Repo.Append` receipt
rather than the store's `Transaction` directly, because the receipt is the value
the finding names.

### GAP-20 reproduced, through the receipt the kernel hands back

`/tmp/evprobe3` binds a `Repo` over an `eventmemory` store, begins a transaction in
a frame that returns **only** the `event.Commit` it got back, appends to
`orders.order acme/A-17` inside it and never commits. Eight `runtime.GC()` calls
later it loads and appends to the same aggregate through the same `Repo`:

```
the caller keeps the commit receipt:      event: the stream is not at the version this append was decided at: conflict
  and the authority in it is still valid: true
control, the receipt is dropped:          <nil>
control, the caller keeps the *Tx itself: event: the stream is not at the version this append was decided at: conflict
```

The first line is the defect and the two controls are what make it one: dropping
the receipt admits the append, so the pin **is** the receipt; holding the `*Tx`
refuses it, which is the claim doing its job. After the fix, same probe, same
three cases:

```
the caller keeps the commit receipt:      <nil>
  and the authority in it is still valid: true
control, the receipt is dropped:          <nil>
control, the caller keeps the *Tx itself: event: the stream is not at the version this append was decided at: conflict
```

| Finding | Grade | Disposition |
|---|---|---|
| GAP-20 | `[medium][immediate]` | **closed by mechanism and sentence, in that order.** The authority no longer names the `*Tx`: `Tx` carries a `txIdentity{log, nth}`, minted under the log's mutex at `Begin`, and `Store.Transaction` hands *that* to `event.NewAuthority`. It is comparable, stable for the transaction's life, monotone per log and never reused — every property `NewAuthority` asks for — and it names nothing that could commit or append, so a retained receipt pins no claim. What reachability still means is then stated without a condition missing: `doc.go`, `docs/modules/{en,ru}/eventmemory.md` and the plan's **Degradation** paragraph say that the `*Tx` itself and a context carrying it that outlives the request keep their claims — both of which can still be *used*, so the claim is doing its job — and that the receipt does not. The plan's round-3 claim about pinned staged payloads is corrected in the same paragraph |
| GAP-21 | `[medium][deferred]` | **closed on the consumer's own value.** `Reader.Cursor` now carries the obligation — a cursor is safe to persist *unless the walk ran on a context carrying a transaction of this backing* — because that is the value the consumer holds and calls. `event/store.go`'s sentence is split in two: *no path the kernel initiates opens a transaction and then reads globally*, which is true, and *`Reader.Next` issues this call on whatever context the consumer hands it, and neither it nor `Read` refuses one carrying a transaction*, which is the part that read as a guarantee and was not one. `docs/modules/{en,ru}/event.md` say both, on the `Reader.Cursor()` row and in the two store's-answer bullets. Refusing at the call was **rejected and the plan says why**: a `Reader` holds a `Log`, which has no `Transaction` method, and `ReadOnly` deliberately has no `Next` to walk past, so the kernel cannot ask the question without a store call per page and a widened seam |
| GAP-22 | `[low][deferred]` | **closed by moving the parse back out.** `readCursor` runs above `this.log.mutex.Lock()` again; only the liveness check and what acts on the log are inside. The *report* stays below, because the precedence is `Refused` before `BadCursor` — and that precedence was **not** pinned by anything, contrary to round 4's note: `TestATransactionThisStoreCannotUseIsRefusedAtEveryDoor` passed `""`, a cursor that parses. It now also passes a foreign one at every refusing door, with the control beside it — the same cursor inside a **live** transaction, which must be `BadCursor`, so the refusal above is the transaction and not the cursor |
| GAP-23 | `[low][deferred]` | **closed by moving the sentence to the code that implements it.** `Tx.live` carries the finished-transaction rule (*"answering it with autocommit is the escape the transaction rule exists to close"*); `ambient`'s comment says only what `ambient` does — the two keys, another log's transaction running on this store's autocommit, and why the lookup is above the lock |
| GAP-24 | `[low][deferred]` | **closed by naming the command.** `claimedByAnother(stream, tx) bool` is `releaseDeadClaim(stream) *Tx`: the name is the write it performs and the result is the claimant that survived it, which `Append` compares against its own transaction. The drop stays where it was found — moving it to `Append` would have pushed that function from 48 lines past `architecture.md`'s 50 — and the comment keeps the argument for the lazy drop, now including what it costs (one word per stream nobody appends to again) |

### Verified by mutation — four applied, four killed, every file restored byte-identically

The first is measured twice, in the tree and out of it, because the finding's own
input is an out-of-tree caller holding a receipt.

```
Store.Transaction hands the *Tx to        → "appending to a stream claimed by an abandoned
  NewAuthority again                        transaction whose commit receipt the caller still
                                            holds answered [outcome conflict] after 8
                                            collections"  (TestAStreamIsClaimedOnly…)
nameTransaction returns a constant        → "two live transactions of this store answer
  ordinal                                   authorities that compare the same" — the
                                            conformance suite's `transactions` section, at
                                            both limit settings, and TestASecondAppendIn…
ReadAll reports the bad cursor before it  → "reading the log from an unreadable cursor
  asks whether the transaction is live      through a context carrying a transaction that
                                            was already finished reported [outcome bad
                                            cursor] rather than … [outcome refused]"
ReadAll resolves the ambient under the    → "door 0 of the log never answered while a log
  lock again (GAP-13's own criterion,       walk was still inside its own caller's ctx.Value"
  re-checked after the parse moved)         (TestACallersOwnContextIsNeverRunInside…)
the fix reverted and the probe rerun      → the transcript above, first line
  out of tree
```

`event/eventmemory/read.go` `sha256 1e33722d…cc15ac` and `transaction.go`
`291405cb…c1e68` compared before and after every mutation, identical each time.

### Tests left behind

- `TestAStreamIsClaimedOnlyWhileSomethingCanStillCommitIt` gains *"the commit
  receipt of an abandoned transaction is not a route back to it"*, beside the two
  cases already there — the abandoned one and the still-held one — so the three
  are told apart rather than one standing for all. It asserts the authority is
  still valid at the end, so the case cannot pass by not holding the value it
  claims to hold. Stable at `-count=30`, at `-count=20 -gcflags=all='-N -l'` and
  under `GOGC=off`.
- `TestATransactionThisStoreCannotUseIsRefusedAtEveryDoor` gains the unreadable
  cursor at the two refusing doors and, in the live-transaction subtest, the
  control that the same cursor is `BadCursor` there.

### Zero-diff obligation

`txIdentity`, `nameTransaction` and `releaseDeadClaim` are unexported and
`Tx.identity` is an unexported field. `make api` regenerates `docs/api/surface.md`
with `eventmemory` at the same **8** package-level symbols; nothing under `event/`
gained or lost an exported symbol, and the two kernel edits — `Log.ReadAll`'s
clause and `Reader.Cursor`'s — are comments. `eventpg` is still writable with zero
diffs under `event/`, and nothing here narrows what it may answer: an authority
naming the store's own transaction handle stays conformant, because
`NewAuthority` asks for identity and never for the transaction object.

### Docs and plan updated in the same change

`event/eventmemory/doc.go`, `event/eventmemory/transaction.go` (the `txIdentity`
paragraph), `event/store.go`, `event/reader.go`, `docs/modules/{en,ru}/event.md`,
`docs/modules/{en,ru}/eventmemory.md`, `docs/ai/flows/FL-036` (the claim note, the
`ReadAll` note, two file-table rows and one **Proved by** row), and the plan — a
new decision-table row for what the authority names, the `Degradation` paragraph
rewritten around reachability, and the GAP-14 row extended with GAP-21's answer
and the rejected alternative.

---

## Round 6 — econv-impl-reviewer (clean context, re-audit of cluster `ctx-value-under-the-log-mutex` after round 5) — 2026-09-08

Scope: the three cluster findings — **GAP-13**, **GAP-14**, **GAP-7** — plus the five round 5
claims to have closed (**GAP-20**, **GAP-21**, **GAP-22**, **GAP-23**, **GAP-24**). Verified
against the code. Six mutations applied in place, each restored and checked with
`sha256sum -c`; `append.go 1363b8e7…e286ae`, `read.go 1e33722d…cc15ac`, `log.go 3a49f4ba…dfd6f98`,
`transaction.go 291405cb…c1e68`, `store.go b2fd2455…ba4e80`, `reader.go c04594c4…10a9aa3` all
identical before and after, and `git status --short -- event/` is unchanged from the start.
Out-of-tree probes ran from a throwaway module at `/tmp/vvaudit` with a `replace` onto this
checkout.

### Checks I ran myself

| Command | Result |
|---|---|
| `gofmt -l .` (whole tree) | silent |
| `go vet ./event/...` | clean |
| `go test -race -count=2 ./event/...` | `ok event 6.005s`, `ok event/eventmemory 1.492s`, `ok event/eventtest 4.036s` |
| `go test -race -count=50 -run TestAStreamIsClaimedOnlyWhileSomethingCanStillCommitIt ./event/eventmemory/` | green — the GC-driven case does not flake at 50 |
| the same at `-count=20 -gcflags=all='-N -l'` and under `GOGC=off` | green both |
| `make check` | nine arms, all `ok` (`check-deps`, `check-tiers`, `check-utils`, `check-triplets`, `check-todo`, `check-replaces`, `check-tidy`, `check-otel-schema`, `check-workspace`) |
| `make api` then `diff` against the pre-existing baseline | **no diff** — `eventmemory` still at 8 package-level entries; file restored |
| `go list -deps ./event/eventmemory` minus stdlib | `utils, crud, errs, event, eventmemory` — no third-party; `weak` is stdlib and the module declares `go 1.26` |

No flake observed anywhere.

### Microkernel — derived here, not inherited

`grep -rn "eventmemory\|eventtest\|eventpg" event/*.go | grep -v _test` → **0 lines**. Stronger
than a grep: I wrote a **complete second `event.Store` in a package outside this repository**
(`/tmp/vvaudit/pgshaped.go`, ~300 lines) and deliberately gave it the **opposite** shape to
`eventmemory` on every question this cluster touched — positions drawn from a non-transactional
sequence at *append* time (so a staged envelope already carries one), `ReadAll` reading **through**
the transaction it joined (so a walk inside a bound transaction returns that transaction's
uncommitted events), a **strong** claim, and the `*Tx` itself as the authority's identity. It is
built only from the exported surface (`NewBacking`, `NewAuthority`, `Failure`, `Limits`,
`Capabilities`, `ResidentPage`), and `eventtest.Run` drives it **unchanged, with zero diffs under
`event/`**, green at two limit settings. On the way there the suite caught two real defects in it
that I then fixed — a cursor fingerprint shared between two backings (`resumption` failed) and a
factory that reset its backing between store values — which is the suite doing its job on a store
it has never seen.

**`event/eventpg` is still writable with zero diffs under `event/`: yes.** GAP-14's loosening is
real rather than declared: the store that answers the *other* way passes.

### Metrics — counted

Non-test files under `event/eventmemory`: `transaction.go` 182, `store.go` 122, `log.go` 118,
`read.go` 103, `append.go` 67, `cursor.go` 42, `doc.go` 41 — threshold 400, no breach. Longest
function `Store.Append` **48**, then `ReadStream` 36, `ReadAll` 35, `New` 33, `NewLog` 22 —
threshold 50, no breach. Maximum nesting depth **3** (counting the function body as 1). Internal
imports per non-test file **1** (`event`). Import cycles 0. Longest access chain
`this.log.streams[stream]` — 2 dots. Exported surface: 8 package-level entries, unchanged against
`docs/api/surface.md`. Global mutable state 0 (`grep -n "^var " event/eventmemory/*.go` finds only
sentinel `errors.New` blocks and one `var _ event.Store` assertion). `init` 0, `log.Print`/`fmt.Print`
in non-test code 0, `os.Getenv` under `event/` 1 and it is in `eventtest/run_test.go` driving its own
subprocess, `TODO`/`FIXME` 0, `nolint` 0, `t.Parallel` 0. No new string, numeric or regex literal in
non-test code; the only literals under `event/eventmemory` are sentinel text, `cursorSeparator = ":"`
(the store's own format, and `readCursor(mintCursor(x)) == x` is fuzz-pinned by
`FuzzACursorEitherResumesInsideTheLogOrIsRefused`), and the four named defaults, each with its
derivation written beside it. `rand.Text()` is base32, so it can never contain the separator.

### The eight findings, re-derived

**GAP-13 — closed.** `ambient` is called at exactly four sites — `append.go:27`, `read.go:21`,
`read.go:70`, `transaction.go:62` — and **none is under `log.mutex`**; the other foreign values,
`ctx.Err()` and the injected `Spec.Clock`, are above it too. Three mutations, three kills:

| Mutation applied in place | Test that went red |
|---|---|
| `ReadAll` resolves `ambient` under the lock again (re-checked after round 5 moved the cursor parse) | `TestACallersOwnContextIsNeverRunInsideTheLogsLock/a log walk …` — *"door 0 of the log never answered while a log walk was still inside its own caller's ctx.Value"* |
| the liveness check hoisted back out of the critical section (GAP-2's original defect) | `TestOneTransactionUsedFromManyGoroutinesRefusesRatherThanCrashes` → `panic: assignment to entry in nil map` at `(*Tx).stage ← (*Store).Append:65`. **Round 5 did not undo GAP-2** |
| `Append` resolves `ambient` under the lock again | my own out-of-tree AB-BA probe hung: `DEADLOCK … after 20s` |

The AB-BA case the finding named is genuinely closed, and I built it rather than reasoned about it:
`/tmp/vvaudit/abba_test.go` runs a context decorator that takes a caller lock `L` inside `Value`
against a goroutine that holds `L` across an `Append`, 200 rounds each. Against the code as it
stands it finishes in `0.00s`; against the mutated store it hangs the whole log for the full 20s
budget. The tree has no test for that case, and it does not need one — it is implied by
`TestACallersOwnContextIsNeverRunInsideTheLogsLock`, whose control
(`TestAParkedContextStopsTheDoorItWasGivenTo`) asserts the parking context really does park.

**GAP-14 — closed.** `event/store.go:125-141` states the `Log.ReadAll` answer inside a bound
transaction and names both conformant shapes; `:91-97` states what `Envelope.Position` means before
a commit and that `Version` is not in that boat. Both are proved conformant rather than asserted:
my out-of-tree store answers the *other* way on both and passes `eventtest.Run` unchanged. In-tree,
the mutation *"a staged envelope carries a position"* goes red at `transaction_test.go:91` —
*"the staged event reads back at position 2 beside the committed one at 1"* — so this store's
answer is pinned rather than incidental. `eventtest/sections_read.go:154-158` records that neither
`global paging` nor `resumption` certifies either answer.

**GAP-7 — closed by a mechanism.** Two mutations, two kills: making the claim strong again
(`map[Stream]*Tx`) reddens *"appending to a stream claimed by a transaction nobody can reach any
more answered [outcome conflict] after 8 collections"*, and the same test's first subtest is the
control — a transaction the caller still holds keeps its claim across the same 8 collections, so
the positive case cannot pass by the claim never being taken.

**GAP-20 — closed, and it closed a second hole on the way.** `Store.Transaction` hands
`tx.identity` — a `txIdentity{log, nth}` minted under the log's mutex at `Begin` — and never the
`*Tx`. I reproduced the finding's own input out of tree, through the **kernel's** `Repo.Append`
receipt rather than the store's `Transaction`, with both controls:

```
the caller keeps the commit receipt:      <nil>   (and the authority is still Valid)
control, the receipt is dropped:          <nil>
control, the caller keeps the *Tx itself: event: … conflict   ← the claim doing its job
```

Reverting `Store.Transaction` to name the `*Tx` reddens
`TestAStreamIsClaimedOnlyWhileSomethingCanStillCommitIt/the commit receipt …`, and making
`nameTransaction` return a constant ordinal reddens the conformance suite's `transactions` section
at both limit settings plus `TestASecondAppendInOneTransactionIsAdmitted/the two operations answer
one authority`. Unremarked in round 5: the old scheme was ABA-free only *because* it pinned — a
collected `*Tx`'s address can be reused — so `Repo.transaction`'s `marked.Same(authority)` mismatch
check is now safe by construction instead of safe by retention.

**GAP-21 — closed.** `Reader.Cursor` carries the consumer's obligation, `event/store.go:137-141`
separates *no path the kernel initiates* from *`Reader.Next` runs one on any context you hand it*,
`docs/modules/en/event.md:141` and `ru:143` say it on the `Reader.Cursor()` row, and the plan
(`EVENTSOURCE_P1_PLAN.md:2328`) records the rejected alternative and why. One residual below as
GAP-26.

**GAP-22 — closed.** `readCursor` runs at `read.go:74`, above `this.log.mutex.Lock()` at `:76`; only
`tx.live()` and what acts on the log are inside. The precedence is now genuinely pinned:
`TestATransactionThisStoreCannotUseIsRefusedAtEveryDoor` passes `foreignCursor` at the two refusing
doors **and** carries the control that the same cursor inside a *live* transaction is `BadCursor`.
Mutating `ReadAll` to report the bad cursor before asking whether the transaction is live reddens it.

**GAP-23 — closed.** `ambient`'s comment now says only what `ambient` does; the finished-transaction
rule is stated once, on `Tx.live`, where it is implemented.

**GAP-24 — closed.** `releaseDeadClaim(stream) *Tx` names the write it performs and returns the
claimant that survived it. `Append:46` compares that against its own transaction. The lazy-drop
argument and its cost are in the comment.

### What is clean, with the check that says so

- **Nothing was widened and no assertion weakened.** Refusal precedence at all three doors is
  `Closed` → `Refused` (ambient) → `Refused` (liveness) → `BadCursor`/`Conflict`; I checked the
  order in the code, not the diff, and mutated the one pair round 5 moved. Every map touch on `Log`
  is under `log.mutex` (`claim`, `releaseDeadClaim` including its `delete`, `publish`, `version`,
  `nameTransaction`, `release`, `stage`, `stagedFor`, `stagedCount`); `Tx.finished` is the one field
  read outside it and it is an `atomic.Bool`; `Tx.identity` is written once at construction and
  never again.
- **`nameTransaction` introduced no new lock-order hazard**: `Store.Begin` calls `ctx.Err()` and
  reads `closed` *before* taking the log's mutex, and nothing foreign runs inside it.
- **Payload and page ownership are untouched**: inbound clone `append.go:57`, outbound clone
  `read.go:101`, pages allocated at `len == cap`.
- **Docs moved with the code.** `doc.go:22-25`, `docs/modules/en/eventmemory.md:83-89` and
  `ru:85-92` (parallel, both carrying the receipt clause), `docs/modules/{en,ru}/event.md`'s
  `Reader.Cursor()` row, `docs/ai/flows/FL-036` (file-table rows naming `claim`,
  `releaseDeadClaim`, `nameTransaction`, `txIdentity`, `ambient`, `Tx.live`, `readCursor`) and the
  plan all say the same thing. Every `Test…`/`Fuzz…` name any of them cites exists — I checked all
  of them mechanically against `func <name>` in the tree, and none is missing.
- **Universality**: nothing in the changed non-test code is fitted to a sample. No fixed phrase
  list, no layout-tuned regex, no `if id == "…"`, no `parts[2]`, no fixture path from a non-test
  file, no uncalibrated threshold. The one number a reader might question, `collections = 8`, is
  test-only, named, and survives `-count=50`, `-gcflags=all='-N -l'` and `GOGC=off`.

---

### GAP-25 [low][deferred] `Log.ReadAll`'s new clause opens by stating as fact the thing its next breath declares unspecified

- **Where:** `event/store.go:131-135`.
- **What:** the paragraph reads *"**Unlike ReadStream, this does not read the caller's own
  writes**: whether a read issued while a transaction of this store's backing is bound returns that
  transaction's uncommitted events is unspecified. A store reading through the transaction it
  joined returns them, a store whose global order is assigned at commit returns none, and both are
  conformant."* The lead clause is a flat statement of the committed-only answer; the rest of the
  sentence retracts it. Only the colon binds them, and the lead clause is the part that reads like
  a contract line on an interface method, sits beside `ReadStream`'s genuinely absolute clause at
  `:187-189`, and is what a skim picks up.
- **Why this severity:** cosmetic in effect — the paragraph as a whole is right and the two
  conformant shapes are named two lines down. It is a comment, so nothing behaves differently.
- **Why this timing:** the reader it misleads is phase 2's author, and the cost if he stops at the
  colon is exactly what GAP-14's close was written to avoid: implementing the committed-only answer
  and opening a second connection to serve `ReadAll` inside a write transaction. Cheap to say
  correctly now; a doc sentence either way, and nothing is rewritten by fixing it later.
- **Close criteria:**
  - [ ] the clause's first sentence states the question rather than one of its answers — e.g.
        *"Unlike `ReadStream`, whether this reads the caller's own uncommitted writes is
        unspecified"*
  - [ ] the two conformant shapes still follow, unchanged
  - [ ] `docs/modules/{en,ru}/event.md` do not repeat the flat form
- **Status:** open

---

### GAP-26 [low][deferred] `Reader.Cursor` carries the right obligation with a cause that does not hold

- **Where:** `event/reader.go:62-68`, repeated as the finding's own words in
  `EVENTSOURCE_P1_S3_GAPS.md` round 4 GAP-21.
- **What:** the obligation — *a cursor from a walk that ran on a context carrying a transaction of
  this backing must not be persisted* — is correct and unconditional. The reason given for it is
  not: *"a store reading through that transaction answered its uncommitted events and this cursor
  is already past them, so **a rollback afterwards discards events nothing will read again**"*. If
  the transaction rolls back its own events never existed, so skipping them is the right answer,
  not a loss. The harm that is real is the other one: on a store drawing positions from a
  non-transactional sequence, a walk inside a transaction returns its own uncommitted rows at
  positions **above the store's safe watermark**, and the cursor then sits past positions that
  *other, still-in-flight* transactions hold and will commit at. Persisting it skips those
  permanently — and it does so whether the walking transaction commits or rolls back.
- **Why this severity:** a comment. A consumer who simply obeys the stated obligation is safe
  either way, which is why this is not medium.
- **Why this timing:** the obligation is already stated correctly, so nothing is blocked and no
  contract shape changes. The risk is second-order — a phase-2 author reasoning *past* the
  obligation from the cause given ("we always commit, so this cannot bite us") and draining inside
  a committing transaction — which is §INV-034's *an event permanently visible to one read and not
  the other* reached without breaking any written rule.
- **Close criteria:**
  - [ ] the reason on `Reader.Cursor` names the in-flight-position hazard rather than the rollback,
        or drops the reason and keeps the obligation
  - [ ] `docs/modules/{en,ru}/event.md`'s `Reader.Cursor()` row says the same
  - [ ] the plan's GAP-14/GAP-21 row (`EVENTSOURCE_P1_PLAN.md:2328`) is not left stating the
        rollback as the reason
- **Status:** open

---

### GAP-27 [low][deferred] The envelope construction and the payload clone are the largest thing left inside the section GAP-13 and GAP-22 were opened to shrink

- **Where:** `event/eventmemory/append.go:50-60`, between `this.log.mutex.Lock()` at `:33` and the
  `stage`/`publish` at `:62`/`:65`.
- **What:** GAP-13's first close criterion, as round 3 quoted it, is *"only the `finished` check and
  the staging remain inside the critical section"*, and GAP-22 was filed for a `strings.Cut` plus a
  `strconv.ParseUint` that had moved into it. The loop that builds the envelopes and calls
  `bytes.Clone` on every record's payload is still there and is two orders of magnitude larger: at
  this store's own defaults that is `MaxBatch 64 × MaxPayload 64 KiB = 4 MiB` of `memcpy` per
  append while every reader and writer of the log waits, and the kernel admits an append up to
  `MaxResidentBytes` = **64 MiB** (`event/repo.go:187`), so a deployment that raises `MaxPayload`
  can hold the log-wide mutex across a 64 MiB copy. Only `Version: admitted + offset + 1` genuinely
  needs the lock; the clone does not.
- **Why this severity:** no foreign code, so no deadlock and no unbounded hold — the work is
  bounded, this store's own, and cannot block. It is head-of-line latency in an in-memory store,
  which is the ordinary harm the cluster named rather than the severe one. Not introduced by this
  cluster; it predates it, and this round is the first to measure it.
- **Why this timing:** module-internal, no contract, and it is a hoist of ten lines. It is recorded
  because the criterion it fails is one this cluster is being graded against, and because the two
  smaller occupants of the same section were both moved out — leaving the largest one unremarked
  reads as a decision nobody made.
- **Close criteria:**
  - [ ] the payload clone runs above `this.log.mutex.Lock()`, or a comment at `append.go:50` says
        why the whole envelope build belongs inside a section only the liveness check and the
        staging were meant to hold
  - [ ] `Store.Append` stays inside `architecture.md`'s 50-line bound (it is at 48 today)
  - [ ] the inbound-clone guarantee is unchanged either way — no `event.Record.Payload` array
        reaches `Log.streams` or `Log.global`, which
        `TestNothingACallerHandsToAnAppendIsRetainedOrRewritten`
        (`event/eventmemory/ownership_test.go:12`) pins
- **Status:** open

---

### Verdict for this cluster

**GAP-13, GAP-14 and GAP-7 are closed**, and so are **GAP-20, GAP-21, GAP-22, GAP-23 and GAP-24**
— six mutations applied and killed, two out-of-tree probes (the receipt through `Repo.Append`, and
the AB-BA deadlock) reproducing the finding's own inputs and now answering correctly, both with
controls that fail if the mechanism under test stops being the thing that makes them pass. The
microkernel boolean was re-derived rather than inherited: a complete second store written outside
this repository, deliberately the opposite shape on every question this cluster touched, passes
`eventtest.Run` with **zero diffs under `event/`**. Three new findings, all `[low][deferred]`, two
of them comments and one a pre-existing hoist. **Nothing in this cluster blocks.**
