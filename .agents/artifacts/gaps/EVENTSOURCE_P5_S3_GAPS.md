# EVENTSOURCE P5 — S3 (the operation receipt) — GAPS

## Round 1 — econv-code-reviewer — 2026-09-12

**Verdict: GREEN. No `[critical]` and no `[high]`.** Nine findings, all `[medium]` or `[low]`,
recorded in [`EVENTSOURCE_BACKLOG.md`](EVENTSOURCE_BACKLOG.md) `## P5` as items **34–42** and
**not fixed**, per the 2026-09-08 delivery policy.

**Every one of the nine was driven, not read.** Ten hard states were constructed and run against
the shipped `event/receipt` over `eventmemory` and a fake `Ledger`; five behaved exactly as
[SPEC] §5.3 says, four are findings, and one (`P3`) is a `-race` report quoted verbatim below.
Four of the section's thirty claimed mutations were re-applied independently — including both of
the two the section rests on — and **every one was caught with a `--- FAIL:` line naming a test**.
No survivor.

**No ES-07 nuance the study records is dropped.** The enumeration is below, eight for eight,
each located in the code.

**The pasted checkpoint output is real.** Every arm re-run at HEAD, including both counting arms,
`make api`, `check-event-kernel` and `event-kernel-moved` against the section's own saved
`.git/event_kernel_before_s3`. Transcript below.

**The one red arm is foreign and is named.** `go test -count=1 ./scripts/` reports exactly one
`--- FAIL:` line, `TestNoI18nPackageCostsMoreThanItsErrorSeam`. It is the owner's i18n work, it is
not this section's, I touched neither it nor its allowlist — and **`make unit` is therefore not
green and this report does not claim it is.** `make check` is all green, eleven arms.

---

## 1. The pasted checkpoint is real — every arm re-run at HEAD

```
gofmt -l .                                                          silent (0 files)
go build ./...                                                      silent
go vet ./event/...                                                  silent

go test -list '^(the fourteen)$' ./event/receipt/ | grep -c ^Test   14   (plan: RECEIPT-LISTED=14)
go test -race -count=1 -run '^(the fourteen)$' ./event/receipt/     ok 1.018s (plan pasted 1.019s)
go test -race -count=1 ./event/...   ok event 7.215s / eventmemory 1.506s
                                     / eventtest 4.400s / projection 5.610s / receipt 1.019s
                                     (plan pasted 7.146 / 1.507 / 4.381 / 5.620 / 1.021)

make api                             regenerated; byte-identical to the tree's file
grep -q '^## github.com/frostgrove/vv/event/receipt$' docs/api/surface.md   present
git diff --numstat docs/api/surface.md                              116 added, 0 removed
                                     (S1's 3 + S2's 35 + S3's 78; 0 removed is INV-120's first arm)

go test -list '^(the scripts seven)$' ./scripts/ | grep -c ^Test    7    (plan: SCRIPTS-LISTED=7)
go test -race -count=1 -run '^(the scripts seven)$' ./scripts/      ok 8.442s (plan pasted 8.530s)

./scripts/checks.sh deps                                            check-deps: ok
./scripts/checks.sh tiers                                           check-tiers: ok
./scripts/checks.sh utils                                           check-utils: ok
./scripts/checks.sh event-kernel                                    check-event-kernel: ok
./scripts/checks.sh event-kernel-moved .git/event_kernel_before_s3 …
                                     the twelve event/receipt files and nothing else
                                     event-kernel-moved: ok   EXIT=0

make check                           deps, tiers, utils, triplets, todo, replaces, tidy,
                                     otel-schema, otel-module, workspace, event-kernel — all ok
go test -count=1 ./scripts/          exactly one --- FAIL: TestNoI18nPackageCostsMoreThanItsErrorSeam
```

**The manifest claim is exact, and I measured it rather than believing it.**
`scripts/event_kernel.sha256` holds **170** lines; **12** of them begin with a hash whose path is
`event/receipt/`. Diffed against the section's own saved baseline:

```
$ diff .git/event_kernel_before_s3 scripts/event_kernel.sha256 | grep -c '^[<>]'
12
$ diff <(cut -d' ' -f3 .git/event_kernel_before_s3) <(cut -d' ' -f3 scripts/event_kernel.sha256)
131a132,143
> event/receipt/claim.go … resolve_test.go        (twelve additions, no line changed)
```

Twelve additions, **zero removals and zero changed hashes** — so `event`, `event/projection`,
`event/eventmemory` and `event/eventtest` are byte-identical to what S2 left, which is the
section's own claim about itself and INV-120's third arm.

**Kernel boundary.** `git status --porcelain event/` at HEAD lists twelve modified files and seven
untracked paths; the only one S3 owns is `event/receipt/`, and the `event-kernel-moved` run above
is the mechanical proof that nothing else moved. The other nineteen are S1's and S2's, both `[x]`.

---

## 2. Microkernel purity — counted, not eyeballed

```
$ go list -deps ./event/receipt | grep frostgrove
github.com/frostgrove/vv/utils
github.com/frostgrove/vv/crud
github.com/frostgrove/vv/errs
github.com/frostgrove/vv/event
github.com/frostgrove/vv/event/receipt
```

Five, exactly the closure [SPEC] §5.3 and plan P-2 declare. **Zero third-party packages**, in test
files too (`check-deps` runs the root module with `-deps -test -tags=integration` and is `ok`).

```
$ grep -rn "event/receipt" --include=*.go . | grep -v '^./event/receipt'
scripts/projection_test.go:182,187,194     (three, all a directory path in a walk — not an import)
```

**Nothing imports `event/receipt`. The kernel does not know it exists**, which is the sentence
§1.2 and INV-126 both rest on (*"nothing in `receipt` can see a second append: `event` does not
import this package"*). A second `Ledger` implementation costs **zero** kernel diffs: it is an
interface in the extension, implemented by the application, handed in through `ClaimSpec.Ledger` /
`ResolveSpec.Ledger`. The extension point declares a contract (`receipt.go:36-67`, 32 lines of
where-each-method-runs), a registration mechanism (the spec field), a failure policy (`ErrLedger`,
refused before the answer is compared or rendered) and a compatibility note (the `INSERT … ON
CONFLICT DO NOTHING` then `SELECT` ordering, and what each isolation level does to a loser). The
package works with **zero** `Ledger`s registered — every door refuses a nil one through `absent`,
driven at `claim_test.go:581` and `errors_test.go:126`.

```
$ grep -n "time.Now\|rand\.\|os.Getenv\|log\.\|	go " event/receipt/{claim,resolve,key,fingerprint,receipt,errors,doc}.go
(only three prose occurrences of the string "time.Now()" inside doc comments)
```

No clock, no randomness, no env read, no log line, no goroutine. `startsNothing` and
`TestNothingInTheProjectionPackageOpensATransaction` (widened to `../event/receipt`, floor 7 files,
fixture control over `Begin`/`Commit`/`Rollback`/`InNewTx`/`InTx`/`InAtomic`) are green — INV-123
and [[D-092]] hold.

---

## 3. Architecture metrics — counted

| Metric | Measured | Note |
|---|---|---|
| non-test files | 7 | `doc` 45, `errors` 34, `fingerprint` 52, `key` 67, `receipt` 74, `resolve` 186, `claim` 293 |
| longest non-test file | **293** (`claim.go`) | |
| longest function | **35** lines (`Held.Complete`), then `Resolve` 23, `sameUnit` 23, `checkKeyText` 22, `Claim` 21 | none near a 40-line comment threshold except `Complete`, which carries one |
| max brace nesting, `claim.go` | **2** | guard clauses throughout; no `if` inside an `if` inside a loop |
| exported types / funcs / consts / sentinels | **10 / 6 / 7 / 4** | exactly [SPEC] §5.3 + P-21's two, `make api` measured — no silent extra surface |
| internal import fan-out | **1** first-party (`event`) | no cycle possible: nothing imports back |
| parameters, widest door | 3 (`Once`) | `Claim`/`Resolve` take `(ctx, spec)` |
| global mutable state | **0** | `fingerprintPrefix` is a `const` |
| constructor-visible dependencies | `Ledger` + `event.Store`, both in the spec struct | |
| comment lines / total, non-test | 276 / 751 (37%) | `event/store.go` is 61%; this is the tree's contract-surface convention, and [SPEC] §5 mandates these comments by name |

**Isolation test.** Every new object passes. `Key` and `Fingerprint` are value objects validated at
construction with no repository, clock or config inside them. `Receipt` is a record. `Held` is a
one-claim handle. `Claim`, `Once`, `Resolve` are functions over a spec. The whole package is
testable with **two** fakes (`eventmemory.Store`, a `Ledger`) — which is what its own suite uses —
moves to its own repository unchanged, and deleting it damages nothing, because nothing imports it.
No distributed god object, no shared mutable context, no feature needing four files edited.

---

## 4. Nuance conformance — ES-07, eight for eight

| Study nuance (`EVENTSOURCE_P5_STUDY.md` §ES-07/2) | Where it is in the code |
|---|---|
| 1. idempotence keyed on the caller's key has no expected-version window | `event/repo.go:160-166` (*"THE VERSION THE TOKEN WAS LOADED AT IS NOT IN IT"*) and `digestOf` at `:203-211`, which digests the stream and the records and nothing else. Driven: `claim_test.go:289-308` asserts the retry's version **differs** and its fingerprint **equals**; `claim_test.go:418-437` asserts the moved version answers `Repeated` and not `Collided` |
| 2. a partial overlap is its own answer — the fingerprint is over the whole range, never per event | `digestOf` hashes one preimage per batch; `claim_test.go:398-416`'s table collides on a byte, on a stream and on a **reordering**, which per-event comparison cannot see |
| 3. an idempotence claim is only as strong as its concurrency anchor; vv must not introduce a weak side | `AppendRequest.Expected` untouched (`event-kernel-moved` shows `event/store.go` did not move); a `Repeated` appends nothing at all, so there is no write for a weak anchor to weaken |
| 4. nothing here is a hidden retry of `Store.Append` | `Store.Append` unchanged; `receipt` issues no `Append` on any path — `Once`'s work is the **caller's** closure |
| 5. the uncertain window is the caller's `COMMIT`, not `Append`'s | `Resolve` is the door, and `resolve.go:150-166` refuses to run inside any transaction so its answer is about a committed row |
| 6. an absent row has two causes and no clean answer; the third answer plus `idle_in_transaction_session_timeout` | `Unresolved` (`resolve.go:20`), the hard clause at `:74-84` naming the lever by name, and `standingOfAnAbsentRow` at `:120-125`. Driven — see §5, P1: the open-writer answer and the rolled-back answer are **bit-identical** |
| 7. retention is what makes the window finite, and neither the reference nor the port solved it | `Ledger.Horizon` (`receipt.go:60-67`), `Expired`, and the *"the sweep is the application's; nothing here prunes"* sentence. Driven — see §5, four standings |
| 8. the port's fail-closed cross-aggregate check, and the PK-index serialisation that is five steps not three | The stream is **inside the fingerprint** (`repo.go:205`), so a redelivery routed to another aggregate is `Collided`, driven at `claim_test.go:404`; the five steps are the `Ledger` doc's `INSERT … DO NOTHING` **then** `SELECT`, the second snapshot, the speculative-insertion wait, the affected-row-count discriminator and the level-by-level loser behaviour (`receipt.go:42-58`) |

The naming hazard the study flagged — *"in vv, 'receipt' already means `Commit`"* — is answered in
`doc.go:7-13`, in those words.

---

## 5. The hard states, driven and run

Ten states constructed in a scratch `_test.go` under `event/receipt`, run under `-race`, and the
file removed afterwards (`check-event-kernel: ok` and `gofmt -l . | wc -l` → `0` after removal).

| # | State | What happened |
|---|---|---|
| P1 | `Resolve` on a fresh connection **while the writer is open**, then again after that writer **rolled back** | Both `[standing unresolved]`, both carrying the ledger's horizon, and the two `Resolution` values compare `==`. The two absences are **indistinguishable**, which is INV-115's whole point. ✔ |
| P2 | a `Collided` claim's `Held` | `ErrCollision` correct and its message names nothing — **but `held.Receipt()` handed back the other operation's `Stream` (`"A-17"`), range `1..1`, fingerprint and `RecordedAt`.** → **GAP-1** |
| P3 | `Held.Complete` and `Held.Receipt()` on two goroutines | `WARNING: DATA RACE`, write at `claim.go:194` against a read at `claim.go:76`. → **GAP-2** |
| P4 | a `Ledger` whose `Complete` fails once | the first call returns the ledger's error; the **second** returns `ErrSpec` *"this claim has already been completed"* and does not wrap the ledger's error. The row was never written. → **GAP-3** |
| P5 | a `Find` answering a row with a **zero `RecordedAt`** beside a `Horizon` in **2099** | `[standing found]`, `err=<nil>` — the `ErrLedger` horizon check is skipped. → **GAP-4** |
| P6 | two `Claim`s of one key inside one unit | `[verdict recorded]` then `ErrIncomplete`. Correct, and named nowhere. → **GAP-7** |
| P7 | `Once` whose work fails, on a unit the caller commits anyway | verdict `Recorded`, error propagated, row `complete=false`, `Resolve` → `Incomplete`. Exactly §UC-222. ✔ |
| P8 | a repeat whose row names **another stream** with an equal fingerprint | `[verdict repeated]`, no error, `held.Receipt().Stream.Key == "SOMEBODY-ELSE"` while the claim named `"A-17"`. → **GAP-6** |
| P9 | `Complete` on a real 1-event claim with a **stray empty `Commit` from a different, already-committed unit** | **admitted**; the durable row reads `first=0 last=0 complete=true` for an operation that wrote an event. → **GAP-5** |
| P10 | `Find` answering `found == true` beside the zero `Receipt` | `ErrLedger`, *"a receipt for another key or for none at all"*. ✔ |

---

## 6. Contract conformance, both ways

`make api`'s `event/receipt` section is **exactly** [SPEC] §5.3 plus P-21's two names — ten types,
six functions, seven constants, four sentinels, no eleventh type and no seventh function. The four
sentinel texts are byte-identical to §5.3's. The `Ledger` interface's five methods and their
signatures are identical. `Receipt`'s seven fields are identical and in order.

Two additive behaviours §5.3 does not publish, both strictly safer and neither a drift I would
raise: `Once(ctx, spec, nil)` answers `ErrSpec` rather than panicking, and `Held.Complete`
refuses the zero `Held` before anything else. The one drift **into** the caller's hands is GAP-1.

`event/eventtest` did not move (excluded from the allowed set and the manifest confirms it); the
`event` section of the surface gained lines and lost none — INV-120 both arms.

---

## 7. Binding decisions

| Decision | Verdict |
|---|---|
| [[D-118]] a durable write inside the caller's bound transaction is the outbox, and no second durable-intent table | **Held.** The receipt is atomic with its append by a *checked* property, not a documented one: `sameUnit` (`claim.go:223-245`) compares `Ledger.Transaction(ctx)` with `Store.Transaction(ctx)` through `Authority.Same` at **both** write doors. *"'Atomic' across two handles is a sentence with no meaning"* is driven at `claim_test.go:122-134`. The ADR D-118 demands is scheduled as D-142 in S6 — **owed, not missing at S3** |
| [[D-126]] the store chooses no isolation level | **Held.** `grep -rn "SET TRANSACTION\|IsolationLevel\|Serializable" event/receipt/` → nothing. The `Ledger` doc states what each level does to a loser and chooses none |
| [[D-092]] anything continuous is a `runtime.Runner`; constructors start nothing | **Held.** No `go` statement; `startsNothing` green over the new files |
| [[D-101]] migrating is a deployment profile choice | **Held.** `eventpg` gains nothing; `SchemaVersion` unmoved; `grep -rn "SchemaVersion" event/eventpg/ | grep 2` unchanged and `event-kernel-moved` shows no `eventpg` file touched |
| [[D-128]] the log delivers in position order | **Not reached.** `receipt` reads no event — `TestAResolveReadsNoEventAndOffersNothingToAppendWith` asserts the store is asked exactly `["Transaction"]` and nothing else, re-run green |
| [[D-129]] a checkpoint is a store-minted cursor | **Not reached** — `receipt` touches no checkpoint |
| [[D-130]] / [[D-133]] | **Not reached** — no runner, no fence in this section |
| UC-032 §8 (never re-propose the same facts at the version the store now reports) | **Held**, structurally: `TestNoReceiptTypeExposesAnAppendToken` walks all ten exported types for an `event.At[…]` in any field or return, with a fixture control that reports two. Re-run green |

---

## 8. Tests — four mutations re-applied, none survived

Each applied to a non-test file, run under `-race`, reverted, and the file `sha256sum`-compared
against a saved original afterwards (identical, both files).

| Mutation | Result |
|---|---|
| `sameUnit` no longer compares the ledger's transaction with the store's (`_ = held`) | `--- FAIL: TestAClaimIsRefusedUnlessTheLedgerAndTheStoreAreOneTransaction` |
| the `!won && !held.Complete` → `ErrIncomplete` block deleted, so an unresolved row reads as a repeat | `--- FAIL:` × **4** — `TestARepeatIsAnsweredOnlyFromACompleteRow`, `TestOnceRunsTheWorkOnlyOnRecordedAndCompletesIt`, `TestEveryRefusalClaimHasIsReachableThroughOnce`, `TestNoRefusalOfAReceiptNamesAKeyOrAFingerprintsPreimage` |
| `claimAnswered`'s zero-key arm deleted — the section's own **first-pass survivor** | `--- FAIL: TestALedgerThatAnswersSomethingNoLedgerAnswersIsRefused`. The strengthening held: the *different sentences* assertion is what catches it |
| the horizon boundary moved past the instant rather than at it (`!issued.Before` → `!issued.After`) | `--- FAIL: TestAnAbsentRowIsUnresolvedAndAHorizonDecidesExpired` |

The two most important — INV-114's door and INV-117's `ErrIncomplete` — are rows 1 and 2. Neither
survived.

---

# Findings

### GAP-1 [medium][next] A collided `Claim` hands the caller the *other* operation's row through `Held.Receipt()`

- **Where:** `event/receipt/claim.go:115-118` —

  ```go
  taken := &claimed{spec: spec, authority: authority, verdict: verdictOf(spec, held, won), receipt: held}
  if taken.verdict == Collided {
      return Held{taken: taken}, fmt.Errorf("%w: …", ErrCollision)
  }
  ```

  — `held` is the stranger's row, and `Held.Receipt()` (`claim.go:72-77`) returns it unfiltered.
  The contract it drifts from is [SPEC] §5.3 / §1.2 *"the three verdicts"*
  (`EVENTSOURCE_P5_USECASES.md:1110-1111`): *"`Claim` returns `(Held, error)` with
  `errors.Is(err, ErrCollision)`, and the `Held` still carries **the verdict** for a caller that
  wants to branch."* The verdict; not the row.
- **What:** driven. A first operation under key `K` on stream `A-17`; a second under `K` on
  `B-42` with different records:

  ```
  P2 verdict=[verdict collided] err=receipt: this operation key was spent on another operation: …
  P2 held.Receipt() = key=[operation key] fingerprint=sha256:d55ad77b… stream=[stream receipts.account]
                      first=1 last=1 complete=true recordedAt=2026-09-12 09:00:01 +0000 UTC
  P2: the collided caller was handed the OTHER operation's stream "A-17" and range 1..1
  ```

  The **message** is careful — `TestNoRefusalOfAReceiptNamesAKeyOrAFingerprintsPreimage` proves it
  names neither the key, nor the stream key, nor the payload. The **return value** is not, and the
  stream key is what that test's own comment calls *"a customer's identifier"*
  (`errors_test.go:17-18`).
- **Why this severity:** `[medium]`, not `[high]`, because the published refusal is correct, the
  sentinel is right and the damage needs a caller mistake on top. The mistake is the sanctioned
  shape one step wrong: §5.3 invites a caller to branch on the verdict, and
  `if v := held.Verdict(); v == Repeated || v == Collided { return held.Receipt() }` compiles,
  reads naturally and answers tenant B with tenant A's account id and version range — over a key
  tenant B chose. INV-124's stated purpose (*"carries no key, no payload, no version, no
  position, no cursor and no identity"*) is defeated through the value channel while the error
  channel keeps it.
- **Why this timing:** `[next]`. The fix is one line — return the zero `Receipt` from
  `Held.Receipt()` when the verdict is `Collided`, or do not seat `held` in `claimed` on that path
  — but it is a published-surface behaviour change, so it belongs with S5's live collision table
  and §5.3's own sentence rather than inside a code-only section.
- **Close criteria:**
  - [ ] `Held.Receipt()` answers the zero `Receipt` on a `Collided` verdict, or [SPEC] §5.3 says
        in words that it answers the stranger's row and why that is safe.
  - [ ] A test drives `Claim` to `Collided` and asserts `held.Receipt() == receipt.Receipt{}`
        (or the documented alternative), with a control on `Repeated` asserting the row **is**
        there, so the two paths are distinguished.
  - [ ] `TestNoRefusalOfAReceiptNamesAKeyOrAFingerprintsPreimage` gains an arm that walks the
        **returned `Held`**, not only the error string, for the three needles.
- **Status:** open

### GAP-2 [medium][next] `Held` is half-synchronised: `completed` is an `atomic.Bool` and the field beside it is not, so `Receipt()` races `Complete`

- **Where:** `event/receipt/claim.go:57-63` —

  ```go
  type claimed struct {
      spec      ClaimSpec
      authority event.Authority
      verdict   Verdict
      receipt   Receipt
      completed atomic.Bool
  }
  ```

  — `completed` is atomic; `receipt` is written unguarded at `claim.go:194` (`taken.receipt =
  filled`) and read unguarded at `claim.go:76` (`Held.Receipt()`).
- **What:** driven, under `-race`:

  ```
  WARNING: DATA RACE
  Write at 0x00c000184e18 by goroutine 11:
    github.com/frostgrove/vv/event/receipt.Held.Complete()
        event/receipt/claim.go:194
  Previous read at 0x00c000184e18 by goroutine 12:
    (Held.Receipt())
  ```
- **Why this severity:** `[medium]`. The blast radius is one `Held`, which belongs to one
  transaction, which `database/sql` already forbids sharing between goroutines — so this is not
  the process-global shape `CLAUDE.md` says the `-race` suites exist for. What keeps it above
  `[low]` is that the `atomic.Bool` beside it says concurrency **was** considered: a reader is
  entitled to conclude `Held` is safe for concurrent use, and it is safe for exactly one of its
  three methods. A consumer whose own suite runs `-race` and who reads `Receipt()` from a status
  goroutine gets an undefined-behaviour report pointing at this framework.
- **Why this timing:** `[next]`. Either guard is two lines (a mutex, or stop mutating `claimed`
  and let `Complete` return the filled `Receipt`), but the second changes the published surface
  and the first needs the doc sentence that says which methods are safe.
- **Close criteria:**
  - [ ] Either `claimed.receipt` is written and read under the same guard `completed` uses, or
        `Held`'s doc comment states that a `Held` is single-goroutine and the `atomic.Bool` is
        explained as belt-and-braces rather than as a concurrency promise.
  - [ ] A test runs `Complete` and `Receipt()` concurrently under `-race` and is green.
- **Status:** open

### GAP-3 [medium][next] `Complete` burns the `Held` before the ledger write succeeds, so a failed completion reports "already completed"

- **Where:** `event/receipt/claim.go:183-195` — the `CompareAndSwap` runs **before**
  `taken.spec.Ledger.Complete(ctx, filled)`, and nothing resets it when that call returns an
  error.
- **What:** driven, with a `Ledger` whose `Complete` fails once:

  ```
  P4 first=the ledger statement failed
  P4 second=receipt: this operation cannot be recorded from this spec: this claim has already been
            completed, and an operation that appended twice under one key would otherwise record one range
            (errors.Is ErrSpec=true, errors.Is broken=false)
  ```

  The row was never written, and the second call says the opposite and drops the ledger's error.
- **Why this severity:** `[medium]`. Inside PostgreSQL the point is largely moot — a failed
  statement poisons the caller's transaction and the whole unit rolls back, which is the shape
  [SPEC] §1.2 relies on everywhere else. It is not moot for a `Ledger` over a backend where a
  single statement can fail recoverably, and there the operator is handed a sentence
  (*"already been completed"*) that contradicts the durable state (`Incomplete`) and an error
  class (`ErrSpec`, *the caller's*) that blames the wrong party for a ledger fault.
- **Why this timing:** `[next]`. Moving the CAS below the ledger write, or resetting it on
  failure, is three lines — and it needs the §5.3 sentence saying which of the two a second
  `Complete` after a failed one gets.
- **Close criteria:**
  - [ ] A `Complete` whose `Ledger.Complete` returns an error either leaves the `Held`
        re-completable, or [SPEC] §5.3 states that it does not and why.
  - [ ] A test drives a failing `Ledger.Complete` and asserts the second call's answer, whichever
        the contract chooses.
- **Status:** open

### GAP-4 [medium][next] `Receipt.RecordedAt` is validated at neither door, and its absence silently switches off the horizon `ErrLedger` check

- **Where:** `event/receipt/resolve.go:182` —

  ```go
  if !held.RecordedAt.IsZero() && horizon.After(held.RecordedAt) {
  ```

  — and `claim.go:253-269` (`claimAnswered`), which checks the key, the fingerprint, the stream
  and the range on the win path and never `RecordedAt`. The contract it fails is `receipt.go:19-22`:
  *"`RecordedAt` is the ledger's own database clock … It is **zero on the value handed to
  `Ledger.Claim` and filled on the one handed back**."*
- **What:** driven. A `Find` answering a complete row with a zero `RecordedAt`, beside a `Horizon`
  of 2099-01-01:

  ```
  P5 standing=[standing found] err=<nil> horizon=2099-01-01 00:00:00 +0000 UTC
  P5: a horizon in the year 2099 beside a row it still answers for was ACCEPTED because the row's
      RecordedAt is zero
  ```

  The section's own mutation campaign says *"a horizon later than a row the ledger answers for is
  trusted"* is caught. It is caught only when the row carries an instant.
- **Why this severity:** `[medium]`. `ErrLedger`'s stated rule is *"a ledger is trusted exactly as
  far as its answers are, which is the rule the kernel already applies to a store's strings and
  numbers"* — and the one field whose contract the framework can check unconditionally is the one
  it never checks. The real failure it lets through is P-17's named window: a ledger that forgets
  `recorded_at` in its `SELECT` list publishes an optimistic horizon unchallenged, and a swept row
  then reads as `Unresolved` for ever instead of `Expired` — the exact monotone-growth failure
  §"Retention" exists to close.
- **Why this timing:** `[next]`. Two `if`s. It changes what a non-conformant `Ledger` is told, so
  it belongs beside S5's four ledger decorators where the falsifying half lives.
- **Close criteria:**
  - [ ] A `Find` that answers `found == true` with a zero `RecordedAt` is `ErrLedger`, or
        `Receipt`'s doc says a zero one is conformant on that path.
  - [ ] A `Claim` that answers `won == true` with a zero `RecordedAt` is `ErrLedger`, or the same
        sentence covers it.
  - [ ] `TestALedgerThatAnswersSomethingNoLedgerAnswersIsRefused` gains the zero-`RecordedAt` arm
        with its own distinct sentence, held by the existing *different sentences* assertion.
- **Status:** open

### GAP-5 [medium][next] The empty-commit exemption admits an empty `Commit` minted in a different unit, and unlike UC-250's sibling hazard nothing writes it down

- **Where:** `event/receipt/claim.go:180-190` —

  ```go
  if !commit.Empty() && !commit.Authority().Same(taken.authority) { … }
  …
  if !commit.Empty() {
      filled.First, filled.Last = commit.First(), commit.Last()
  }
  ```

  — an empty `Commit` skips the authority comparison by design (`event/token.go:23-28`), and
  `Commit.Stream()` is the only thing left discriminating it.
- **What:** driven. An empty `Commit` for `A-17` minted in an **earlier, already-committed** unit,
  handed to the completion of a claim whose own append wrote one event to `A-17` in the current
  unit:

  ```
  P9 stray empty commit: empty=true stream=[stream receipts.account] authority.Valid=false
  P9 completing a real append's claim with a stray empty commit from another unit: <nil>
  P9: the row now records 0..0 for an operation that wrote 1 event(s)
  P9 durable row: first=0 last=0 complete=true
  ```

  Every later retry of that key is answered `Repeated` with an empty range and reports *done,
  nothing changed* — which is verbatim the damage §UC-222's **Must not** describes
  (*"a caller that answered its client from a receipt whose range is `0–0` has reported success
  for events that may or may not exist"*), reached through a `Complete` that was admitted rather
  than through a claim nobody resolved.
- **Why this severity:** `[medium]`. It needs caller malpractice, and it is genuinely
  **undecidable** on the current surface — an empty commit carries the invalid authority whether
  it was minted here or elsewhere, and `ClaimSpec` records no version to compare
  `Commit.Last()` against. What makes it a finding is not the hole but the silence: [SPEC] §1.2's
  own method for an undecidable hazard is to state it, pin it with an inverted control and name
  the day it closes — applied to §UC-250's third way and to the byte-reproducibility obligation,
  and not applied here. `Held.Complete`'s doc says *"the doc says which check is absent and why
  rather than leaving the gap to be found"*; the gap it leaves is this one.
- **Why this timing:** `[next]`. The sentence belongs in §5.3 and on the module page S6 writes,
  beside UC-250's; and if the phase wants it *closed* rather than stated, `ClaimSpec` would have
  to carry the token's version, which is a surface decision.
- **Close criteria:**
  - [ ] [SPEC] §5.3's `Held.Complete` comment and the module page name the third undecidable
        completion — an empty commit from another unit — beside UC-250's second append.
  - [ ] A case pins it in the `gate_relscope_test.go` inverted shape, asserting the wrong answer
        **is** there, so the day something closes it the control fails.
- **Status:** open

### GAP-6 [low][backlog] The repeat path never compares the row's `Stream` with the claim's

- **Where:** `event/receipt/claim.go:260-262` — `claimAnswered` returns `nil` for `!won` after
  checking the key alone, and `verdictOf` (`:272-281`) decides on the fingerprint alone.
- **What:** driven. A `Ledger` answering a complete row whose fingerprint equals the claim's and
  whose stream is somebody else's:

  ```
  P8 verdict=[verdict repeated] err=<nil> stream=[stream receipts.account] range=4..5
  P8: a repeat was answered from a row for stream "SOMEBODY-ELSE" while the claim named "A-17"
  ```

  The caller then answers its client `4..5` on a stream it never claimed.
- **Why this severity:** `[low]`. Unreachable with a conformant `Ledger`, because `digestOf`
  opens the preimage with the composed stream — a row for another stream cannot compare equal.
  It is a hardening gap in the `ErrLedger` door rather than a defect: the win path checks
  `held.Stream != spec.Stream` and the repeat path does not, and the study's nuance 8 (*the row
  records **which aggregate** the key was spent on*) is reproduced through the fingerprint rather
  than through the column.
- **Why this timing:** `[backlog]`. One `if`, no contract moves, no caller affected today.
- **Close criteria:**
  - [ ] `claimAnswered` refuses a `!won` row whose `Stream` is not the claim's with `ErrLedger`
        and its own sentence, or a comment says the fingerprint already covers it and names
        `digestOf`'s first field as the reason.
  - [ ] If the check is added, a distinct-sentence arm joins
        `TestALedgerThatAnswersSomethingNoLedgerAnswersIsRefused`.
- **Status:** open

### GAP-7 [low][backlog] A second `Claim` of one key inside one unit answers `ErrIncomplete`, and no document names that shape

- **Where:** `event/receipt/claim.go:112-114`, reached whenever the row the `SELECT` reads back is
  the claim's own staged, not-yet-completed insert.
- **What:** driven:

  ```
  P6 first=[verdict recorded]/<nil>
  P6 second=[verdict unstated]/receipt: this operation key holds a claim nobody resolved, …
             it is a defect report rather than a state to retry through
  ```
- **Why this severity:** `[low]`. The answer is *correct* — at that instant the row genuinely is
  incomplete — and the shape is caller error. But `ErrIncomplete`'s own text calls itself *"a
  defect report rather than an ordinary outcome"* and sends the reader to `Resolve` and then to a
  human, which is the wrong advice for the caller that simply wrote two `Once`s under one key.
- **Why this timing:** `[backlog]`. A sentence, in the module page S6 writes.
- **Close criteria:**
  - [ ] The module page or §5.3 names the in-unit re-claim as one of `ErrIncomplete`'s causes and
        says the fix is one key per append (§INV-126's own recipe).
- **Status:** open

### GAP-8 [low][backlog] INV-123's fourth falsifier — *a recording source asserting no `Begin`, `Commit` or `Rollback`* — did not ship

- **Where:** the plan's coverage matrix routes INV-123's checkpoint to **S3** and names four
  falsifiers; the section's Tests list and `event/receipt/*_test.go` carry no recording
  `crud.Source`. What shipped is the AST arm, `scripts/projection_test.go:181-194` (walks
  `../event/receipt`, floor 7 files, fixture control over all six shapes) plus `startsNothing`
  plus the dependency charge.
- **What:** read and confirmed by grep — no `Begin`/`Commit`/`Rollback` counter exists in the
  package's fixtures; `countedStore` (`receipt_test.go:361-421`) counts `event.Store` calls and
  `event.Store` has no `Begin`.
- **Why this severity:** `[low]`. The property is genuinely held — `opensATransaction` is a call
  walk, not a spelling walk, with a control that reports all six shapes, and I re-ran it green.
  Three of the four falsifiers carry it. The gap is the matrix promising a fourth.
- **Why this timing:** `[backlog]`. Either the arm or the matrix line; both are small and neither
  changes behaviour.
- **Close criteria:**
  - [ ] Either a recording `crud.Source` arm lands in `event/receipt`, or the coverage matrix's
        INV-123 row drops the fourth falsifier and says the AST walk subsumes it.
- **Status:** open

### GAP-9 [low][backlog] The flows reverse index gained S2's two files and not S3's seven

- **Where:** `docs/ai/flows/Index.md:620-625` — the working tree adds
  `event/projection/mark.go` and `event/projection/wait.go`, both S2's, and adds none of
  `event/receipt/{doc,key,fingerprint,receipt,claim,resolve,errors}.go`.
- **What:** `git diff docs/ai/flows/Index.md` shows exactly two added rows.
- **Why this severity:** `[low]`. The plan routes **FL-043** and every doc obligation to S6, so
  there is no flow for a receipt row to point at yet — which is a defensible reason to wait. What
  makes it worth a line is that S2 did not wait, so the index is now *half* current, and
  `CLAUDE.md`'s own sentence is *"an index that does not list a file is worse than a missing file
  — an agent trusts the index and stops looking."*
- **Why this timing:** `[backlog]`. S6 owns it; this is the note that it is owed and that the
  asymmetry is not an oversight to be re-discovered.
- **Close criteria:**
  - [ ] S6's FL-043 lands and all seven `event/receipt` non-test files carry a row in
        `docs/ai/flows/Index.md`'s reverse index.
- **Status:** open
