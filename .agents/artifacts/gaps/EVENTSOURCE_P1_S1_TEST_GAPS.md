# EVENTSOURCE_P1 — S1 (the vocabulary, the refusal partition and the store contract) — TEST GAPS

## Round 1 — econv test reviewer (clean context) — 2026-09-07

**What was graded.** `event/{refusal,walk,outcome,rendering,store,backing,concurrency,fuzz}_test.go`
— ten tests, one fuzz target, 1 138 test lines — against `event/{doc,identity,bounds,text,backing,authority,outcome,errors,store}.go`
(839 lines), the plan's §S1, §Contracts, §Coverage matrix and §Carried gaps C1–C10,
`.agents/artifacts/usecases/EVENTSOURCE_P1_USECASES.md` §INV-001/005/011/012/016/018/021/024/025/026/027/028/029/030/033/038/045
and §UC-034/048/060, `.agents/artifacts/gaps/EVENTSOURCE_P1_S1_GAPS.md` rounds 1–3, `CLAUDE.md`,
and `~/.claude/skills/econv/references/{gaps,universality,architecture,building-blocks,data-integrity,microkernel,restrictions,readability}.md`.

**Runs, all in this worktree.**

```
go test -race -count=1 ./event/                        ok  1.036s
go test -race -count=3 ./event/                        ok  1.089s
go test -race -count=5 ./event/                        ok  1.144s
go test -race -count=1 -shuffle=on ./event/  (x3)      ok  1.035 / 1.032 / 1.036s
go vet ./event/                                        EXIT=0
gofmt -l event                                         silent
go test -count=1 -cover ./event/                       98.3% of statements
go test -list '^(…the eleven…)$' ./event/ | grep -cE '^(Test|Fuzz)'   11
go test -run '^$' -fuzz Fuzz… -fuzztime 40s ./event/   9 697 797 execs, 84 corpus, PASS
```

**No flake, no order dependence, no leftover state.** `-shuffle=on` three times and `-count=5` are
clean. There is no `t.Parallel()`, no `time.Sleep`, no network, no filesystem write, no unseeded
randomness (the fuzz seeds are fixed and the corpus stays in the build cache — `event/testdata`
does not exist). The one wall-clock construct is `time.After(10 * time.Second)` in
`walk_test.go:121`, and it is a hang guard rather than a timing assertion, which is the right shape.
The suite's runtime is **1.0 s under `-race`**, matching the plan's claim. Unit only; nothing here
is an integration test.

**The checkpoint's own claims were re-executed and are honest.** The `-list` count is 11 against a
plan that says 11, every one of the eleven names exists, `go test -race -count=1 ./event/` is green,
and the import graph clause still returns exactly `crud, errs, event, utils`. No `skip`, no
`xfail`, no `t.Skip`, no commented-out assertion, no loosened tolerance anywhere in the eight files.
Errors are compared with `errors.Is` against exported sentinels throughout — the two `!=`
comparisons (`refusal_test.go:422`, `:448`, `walk_test.go:216`) are identity assertions on
`CauseOf`'s return, which is the one place identity is the property. Failure messages state what
broke in plain words and name the consequence; they are the best I have read in this repository.

**Mutation campaign.** **107 deliberate breakages**, each applied to the working tree, run under
`go test -count=1 ./event/`, and reverted; the tree was `diff -r`'d against a pre-campaign copy
afterwards and is byte-identical. **75 caught, 32 survived.** The full log is at the end. Every
finding below cites the mutation that produced it.

**What is genuinely strong, in one line each.** `TestTheRefusalVocabularyIsAPartition` runs
2 doors × 7 outcomes × 24 causes × 24 targets = 8 064 probes and asserts *exactly one* match per
refusal by counting rather than by sampling, with a `go/ast` three-multiset check that the table,
the declared `Err*` set and `vocabulary()`'s own identifier list agree — it caught 11 of my
mutations. `TestANilOrLyingCauseNeverPanicsTheKernel`'s fourth subtest ("the values these guards
refuse are still found when they are real") is a textbook control and it is what turns three
otherwise-vacuous guards into assertions. `TestAJoinedCauseCannotOutlastTheWalksBudget` counts
visits instead of measuring time, drives all six doors the kernel reads a foreign chain through,
and carries the left-deep chain as its control. `TestEveryRenderingNamesAClassAndNeverAValue`'s
"the values it must not render were there to be rendered" subtest is the anti-vacuity case that
most negative tests omit. `TestABackingAndAnAuthorityAreComparedAndNeverIdentical` asserts the
backing comparison *equals* `crud.SameDataSource` over six pairs rather than restating it, which is
§INV-016 proved instead of claimed. The fuzz target proves injectivity by reversibility rather than
by a pairwise sample and survived 9.7 M executions.

---

### GAP-T1 [high][immediate] `Compose`'s frozen wire rendering is asserted by nothing — three mutations that orphan every stream ever written survive the whole suite

- **Where:** `event/fuzz_test.go:11-69` (`decompose`, `FuzzComposeRendersAKeyThatIsLegalAndReversible`);
  the code is `event/identity.go:32-36` (`composeSeparator`, `composeEscape`, `composeDigits`) and
  `:47-84`. The freeze is §INV-005 (USECASES.md:2731-2746) and the plan states the rendering byte
  for byte at PLAN.md:521-533: *"`Compose("acme", "A-17")` is `acme/A-17`. `Compose("acme/evil", "A-17")`
  is `acme%2Fevil/A-17`"*, and calls it **frozen (§INV-005, D-125)**.
- **What:** the only S1 test of `Compose` is the fuzz target, and its expectation is computed by
  `decompose`, which reads `composeSeparator` and `composeEscape` **from the implementation** and
  parses the hex with `strconv.ParseUint(…, 16, 8)`, which accepts either case. The inverse is
  therefore self-consistent under any change to the rendering, and no test anywhere in `event/`
  contains a literal composed key. Three mutations survive `go test ./event/`:
  - **M69** `composeSeparator = '/'` → `':'` — every key ever written is now unreachable;
  - **M68** `composeDigits = "0123456789ABCDEF"` → `"…abcdef"` — every key containing an escaped
    byte is now unreachable;
  - **M87** the `default` arm escapes every byte instead of copying it — `acme/A-17` becomes
    `%61%63%6D%65/%41%2D%31%37`; still legal, still reversible, still injective, and the
    "an operator reading a store's key column sees the identity that produced it" argument at
    PLAN.md:546-549 is gone.
- **Why this severity:** high — a missing test for a stated invariant, and §INV-005 is the one
  whose violation is *silent*: "every aggregate reads as version 0 with a zero state, which §UC-009
  makes indistinguishable from a fresh install, and is then written to, producing two divergent
  histories for one identity" (USECASES.md:2740-2744). The bug that ships is a routine refactor of
  `escapePart` — reordering the `switch`, reusing a `strconv.AppendQuote`, switching to `%x` — with
  a green suite and a production database whose entire history is orphaned on deploy. `[SPEC]`'s
  own *Falsified by* asks for "a fixture that writes with one spelling and reloads with the other";
  the weaker form — a golden string — is not present either.
- **Why this timing:** immediate. The matrix parks §INV-005 on S2's
  `TestComposeRendersALegalKeyAndNeverCollides` (PLAN.md:2812), but that test as the plan specifies
  it (PLAN.md:567-573) asserts *legality* and *pairwise distinctness* — neither of which any of the
  three mutations above breaks. So the freeze is unproven in the plan as written, not merely
  unproven in S1. `Compose` ships in S1, S3's `eventmemory` and S5's `stream identity` will build
  fixtures on top of it, and the rendering is a public wire contract other sections are about to
  depend on.
- **Close criteria:**
  - [ ] A table in `event/` asserts byte-exact equality for at least the five renderings the plan
        already writes down: `Compose("acme","A-17") == "acme/A-17"`,
        `Compose("acme/evil","A-17") == "acme%2Fevil/A-17"`,
        `Compose("acme","evil/A-17") == "acme/evil%2FA-17"`, `Compose("рога","17") == "рога/17"`,
        and one escaped-byte case pinning the **upper-case** hex, e.g.
        `Compose("a\x00b") == "a%00b"`.
  - [ ] The literals are written as strings, not built from `composeSeparator`/`composeDigits`.
  - [ ] M69, M68 and M87 are re-applied and each turns the suite red; the report says so.
- **Status:** **closed** — round 2. `TestComposeRendersTheFrozenKey` in `event/identity_test.go`: a golden table of twelve renderings written as string literals, plus the `Compose()` / `Compose("")` pair and its refusal as empty. M69, M68 and M87 re-applied, each red at `the rendering is the one every stream was written under`.

### GAP-T2 [high][immediate] The kernel's bounded `Is` never meets a node that answers through its own `Is` method — `asksIs` can be deleted and the suite stays green

- **Where:** `event/errors.go:326-331` (`matches`) and `:360-363` (`asksIs`); no test in `event/`
  builds an error carrying an `Is(error) bool` method. The symmetric case on the other traversal
  **is** tested — `refusal_test.go:322-335` (`asOnlyFault`), used at `:369` and `walk_test.go:209`.
- **What:** **M103** replaces
  `finds(err, func(node error) bool { return sameError(node, target) || asksIs(node, target) })`
  with `finds(err, func(node error) bool { return sameError(node, target) })` — the whole custom-`Is`
  half of the kernel's bounded `errors.Is` — and `go test ./event/` is **green**. Every cause in
  the suite reaches its target by pointer identity through `Unwrap`; none reaches it through a
  matcher. The uncovered path is real and reachable: `matches` is called from `refuse` (the
  cancellation branches), from `retryable` (`crud.ErrUnavailable`) and from `refusal.Is`, and the
  ordinary Go idiom for a driver-error wrapper is `func (e *pgError) Is(target error) bool`.
- **Why this severity:** high — a missing test for a declared mechanism, and it is the exact twin
  of GAP-160, which was `[high][immediate]` in `EVENTSOURCE_P1_S1_GAPS.md` round 2 and whose whole
  argument (`event/errors.go:334-336`: *"the framework has one answer to 'does this chain carry a
  T'"*) is that the two traversals must not disagree. Concrete failure that would ship: a store
  classifies `Failure(NotWritten, driverErr)` where `driverErr.Is(crud.ErrUnavailable)` is true and
  nothing in its `Unwrap` chain is that sentinel; `retryable` answers false, `backendRefusal`
  declares no wrap, `port.KindOf` falls to `errs.KindInternal` and the transport renders **500
  instead of 503** — the caller who could have retried does not. That is GAP-160's own sentence
  with `Is` substituted for `As`.
- **Why this timing:** immediate. S3's `eventmemory` and S5's defect fixtures are the first
  producers of store errors, and the classification contract they will be written against is this
  one. A gap in the `Is` half now becomes a fixture written to the wrong contract.
- **Close criteria:**
  - [ ] A fixture beside `asOnlyFault` — say `isOnlyUnavailable`, an error whose `Is` answers for
        `crud.ErrUnavailable` and whose `Unwrap` returns nil — asserts
        `errors.Is(refuseAppend(Failure(NotWritten, it)), crud.ErrUnavailable)` is true, with
        `errors.New("…")` at the same position as the control that answers false.
  - [ ] The same fixture asserts a **cancellation** reached only through an `Is` method still
        travels as the bare sentinel out of `refuse`, and that it does **not** reach the caller
        through a `refusal` (the two halves of §UC-057's window rule).
  - [ ] M103 is re-applied and turns the suite red.
- **Status:** **closed** — round 2. `answersByIs` in `event/refusal_test.go` beside `asOnlyFault`, driven from two new subtests: `TestADeclaredWrapIsReachableByErrorsAsAndACauseIsNot/a class a cause answers for only through an Is method is still found` (with a cause answering for another class as the control) and `TestAContextCauseNeverTravelsThroughARefusal/a cancellation a cause answers for only through an Is method travels the same way` (both halves of §UC-057's window rule). M103 re-applied, red in both.

### GAP-T3 [high][immediate] Two of the kernel's four panic guards have no test, and the test named for the property covers neither

- **Where:** `event/errors.go:133-140` (`refusal.As`'s `defer recover`) and `:355-358`
  (`sameError`'s `defer recover`); the test that names the property is
  `walk_test.go:167-220` (`TestANilOrLyingCauseNeverPanicsTheKernel`).
- **What:** the code declares four guards against a foreign party's defect — `walk`'s recover,
  `findAs`'s nil check, `refusal.As`'s recover and `sameError`'s recover — and the comment at
  `event/errors.go:265-276` states them as a contract (*"a chain that loops, a matcher that panics
  and a matcher that answers for a value it never set are that party's defect and must not become
  the kernel's"*). Two are proved and two are not:
  - **M28** removing `walk`'s recover → **CAUGHT** (a nil `*errs.Fault` whose `Unwrap` dereferences;
    the panic trace goes through `retryable` → `matches` → `walkWithin`);
  - **M09** removing `findAs`'s nil check → **CAUGHT**;
  - **M27** removing `refusal.As`'s recover → **SURVIVED**;
  - **M29** removing `sameError`'s recover → **SURVIVED**.
  Nothing in the suite builds an error whose `As` or `Is` method panics, and nothing builds an
  error of an **uncomparable** dynamic type — `type rows []string` with an `Error()` method — which
  is what makes `node == target` panic inside `sameError`.
- **Why this severity:** high. GAP-162, one round ago, was `[high][immediate]` for exactly this
  failure shape and reported "four panics … on the read door's fail-safe path with the caller's
  transaction open". The two remaining guards protect the same goroutine on the same path, and the
  test whose name is *NeverPanicsTheKernel* asserts nil-and-lying but never *panicking*, so its name
  over-claims what it holds. A decorator that returns `Failure(Refused, causeWithAPanickingAs)`
  panics the request goroutine the moment `refusal.As`'s guard is refactored away, and the suite
  says nothing.
- **Why this timing:** immediate. S4 wires `refuse` into `Load` and `Append`, S5 drives it through
  twelve defect fixtures, and the guards are three-line `defer`s of the kind a readability pass
  deletes. Closing it costs two fixtures in a file that already has three.
- **Close criteria:**
  - [ ] A fixture whose `As(any) bool` panics is used as the cause of a `Failure(Refused, …)`, and
        `errs.AsFault` over the resulting refusal is asserted to return `false` rather than panic.
  - [ ] A fixture of an uncomparable dynamic type (`type rows []string` with `Error()`) is
        asserted to be handled by `refuse` at both doors and by `CauseOf` without panicking,
        including the case where it is compared against **itself** (the pair that panics).
  - [ ] Both fixtures carry the "still found when they are real" control the file already uses.
  - [ ] M27 and M29 are re-applied and each turns the suite red.
- **Status:** **closed** — round 2. `panickingMatcher`, `rows` and `answersForRows` in `event/walk_test.go`, driven from `TestANilOrLyingCauseNeverPanicsTheKernel/a matcher that panics is that party's defect and not the kernel's` and `/a cause nothing can compare does not end the walk`, each with the file's own "still found when they are real" control. M27 re-applied → the suite panics through `refusal.As`; M29 re-applied → red, because `walk`'s recover aborts the whole traversal where `sameError`'s recovers one node and the matcher behind it is still found.

### GAP-T4 [high][immediate] The `-race` concurrency test warms every path in the test goroutine before the readers start, so a lazily initialised package-level cache is invisible to it

- **Where:** `event/concurrency_test.go:49` — `expected := answers()` — before the loop at `:50-64`
  that starts the eight readers. The plan claims this test is "§INV-038's S1 rows and **§INV-013's
  runtime half**" (PLAN.md:2273-2274).
- **What:** every one of the fourteen answers in `answers()` (`:29-47`) is computed once in the
  test goroutine to build `expected`, so every lazily initialised value in the package is already
  populated when the readers begin. Measured:
  - **M107** — `inVocabulary` memoises into a package-level `gateCache []error`, populated on first
    use with no synchronisation — **SURVIVED** `go test -race -count=1 ./event/`;
  - **M108** — the same cache rewritten on **every** call — **CAUGHT**, five `DATA RACE` warnings.
  The detector only sees the write-every-time form. The lazy form, which is the one a real
  optimisation takes and the one §INV-013 exists to forbid, is invisible.
- **Why this severity:** high — a missing test for a stated invariant. The failure that would ship
  is a `vocabulary()` or a `Limits()` memoised without a `sync.Once` under the reasoning "it is
  computed from constants, so two goroutines racing to write the same value is harmless"; that is a
  data race in Go regardless, and under `-race` in a consumer's own suite it turns their build red
  with a stack inside `event`. `-count=5` does not help: each run repeats the same warm-then-read
  order.
- **Why this timing:** immediate. S3's `eventmemory` is the first value in this subsystem with real
  mutable state, and it will be tested by copying this file's shape. A concurrency test that cannot
  see a lazy initialisation is worse there than here.
- **Close criteria:**
  - [ ] `expected` is computed **inside** one of the reader goroutines, or the readers start before
        any answer is computed and agreement is checked pairwise afterwards — either way no path is
        warmed by the test goroutine first.
  - [ ] M107 (a lazy unsynchronised package-level cache in `inVocabulary`) is re-applied and the
        suite goes red under `-race`.
  - [ ] The test states, in its failure message or its shape, that it is the runtime half of
        §INV-013 and not only the "two goroutines read the same string" check.
- **Status:** **closed** — round 2. `event/concurrency_test.go` no longer computes its expectation in the test goroutine: eight readers wait on a barrier, each computes its own first answer, compares its own 200 repeats to it, and the eight are compared pairwise after `Wait`. `Compose` left the envelope literal so no path is warmed. M107 (a lazy unsynchronised package-level cache in `inVocabulary`) re-applied → `WARNING: DATA RACE` and red under `-race`.

### GAP-T5 [medium][immediate] `TestEveryRenderingNamesAClassAndNeverAValue` is negative-only for every rendering except `Stream` — four renderings may be made identical and the suite stays green

- **Where:** `event/rendering_test.go:101-131`; the positive half exists only at `:133-146` for
  `Stream`. The invariant is §INV-025 (USECASES.md:3282-3288), whose statement has two halves:
  what a refusal may not carry, **and** *"What it may name: the family, the wire type name, the
  revision, the rule that was broken and **the sentinel's own classification**"*.
- **What:** the inventory at `:41-77` collects ~540 rendered strings and asserts three things about
  each — non-empty, contains no forbidden value, contains no control character. It never asserts
  that a rendering identifies *which* thing it renders. Four mutations survive:
  - **M76** `refusal.Error()` returns the constant `"event: this operation was refused"` for all
    24 sentinels — **SURVIVED**;
  - **M78** `Authority.String()` returns `"[event backing]"` — **SURVIVED**;
  - **M79** `Backing.String()` returns `"[event authority]"` — **SURVIVED**;
  - **M80** `Support.String()` returns `"[support unstated]"` for all three states — **SURVIVED**;
  - **M57** `Backing.String()` never says `invalid` — **SURVIVED**.
  `Outcome.String` is the one exception and it is caught (**M32**, **M77**), because
  `outcome_test.go:47-49` counts distinct phrases. That is the assertion the other four lack.
- **Why this severity:** medium. Nothing a caller branches on breaks — `errors.Is` is unaffected —
  but the whole diagnostic value of the vocabulary does. The bug that ships is an operator paging
  through a log where 24 different refusals, three capability answers and two different opaque
  values all render the same phrase, which is the state §INV-025 was written to produce the opposite
  of. It is also the largest single gap between a test's name ("NamesAClass") and what it asserts.
- **Why this timing:** immediate — `Support`'s and `Backing`'s renderings are public contract that
  S3, S4 and S5 will be read against, and the fix is the four-line distinct-phrase count that
  `outcome_test.go` already demonstrates.
- **Close criteria:**
  - [ ] Every rendering in the inventory is asserted to contain a token that identifies its own
        kind, e.g. each of the 24 sentinels renders a distinct message, `Backing`/`Authority`/
        `Support` each render a phrase no other of the three produces.
  - [ ] `Support`'s three states are asserted to render three distinct phrases, the way
        `Outcome`'s seven already are.
  - [ ] `Backing{}.String()` is asserted to differ from a stated backing's.
  - [ ] M76, M78, M79, M80 and M57 are re-applied and each turns the suite red.
- **Status:** **closed** — round 2. `TestEveryRenderingNamesAClassAndNeverAValue/a rendering says which kind of thing it is and which one of them`: every refusal renders its own sentinel's message, the twenty-four sentinels render twenty-four distinct messages, the three capability answers render three, a stated backing differs from one no store stated, and the four rendering kinds share no phrase. M76, M78, M79, M80, M57 and M31 re-applied, each red.

### GAP-T6 [medium][immediate] The seam's value contract is untested — a field may leave `Limits`, `Capabilities` or `AppendRequest`, and `Key`/`Cursor` may become type aliases, with the suite green

- **Where:** `event/store_test.go:14-28` (`seamTypes`) and `:115-184`; the contract is
  `event/store.go:32-102` and PLAN.md's §Contracts for `event/store.go` and `event/identity.go`,
  including the export table at PLAN.md:575-581 whose justification for `Key`, `Version`,
  `Position` and `Cursor` is *"defined types rather than parsed structs"*.
- **What:** `TestTheStoreSeamHasEightMethodsAndNoneMutatesOrQueries` inventories **method names and
  method signatures** and nothing else. `seamTypes` names `AppendRequest`, `[]Envelope`, `Limits`
  and `Capabilities` as allowed types but never looks inside them. Survivors:
  - **M47** `Limits` loses `MaxKey`; **M48** loses `MaxRead`; **M49** `Capabilities` loses
    `MonotoneVisibility`; **M54** `AppendRequest` loses `Expected` — all **SURVIVED**;
  - **M83** `type Version uint64` → `int64`; **M84** `type Position uint64` → `int64` — **SURVIVED**;
  - **M85** `type Key string` → `type Key = string`; **M86** `type Cursor string` →
    `type Cursor = string` — **SURVIVED**. With those two aliases a `Cursor` and a `Key` become
    mutually assignable and any bare `string` is either, which deletes the compile-time distinction
    the plan's export table exists to buy.
  Only **M52** (`Envelope` loses `Position`) is caught, and by accident — `concurrency_test.go:23`
  happens to set the field.
- **Why this severity:** medium. `Limits`/`Capabilities`/`AppendRequest` field loss is caught by the
  compiler the moment S3 exists, so the exposure is short. The two aliases and the two signed types
  are not: they compile forever, and a signed `Position` on a store that writes an `int8` column, or
  a `Cursor` a caller passes where a `Key` was meant, is a defect no later section is written to
  catch either.
- **Why this timing:** immediate — S3 and S4 are about to be written against these values, and a
  contract nothing pins is a contract that drifts while it is being consumed.
- **Close criteria:**
  - [ ] A test asserts the declared field set of `Limits`, `Capabilities`, `Record`,
        `AppendRequest` and `Envelope` by name and by type, over `reflect` or `go/ast`, so a
        removal or a retype fails in `event/` rather than three sections later.
  - [ ] A test asserts `Key`, `Version`, `Position` and `Cursor` are **defined** types
        (`reflect.TypeOf(Key("")).Name() != ""` and `!= "string"`, or an `ast` check that the
        `TypeSpec` carries no `Assign` position) and that `Version`/`Position` are unsigned.
  - [ ] M47, M48, M49, M54, M83, M84, M85, M86 are re-applied and each turns the suite red.
- **Status:** **closed** — round 2. `TestTheSeamsValuesAreDefinedTypesWithTheDeclaredFields` in `event/store_test.go`, two subtests: the declared field set and field type of `Stream`, `Capabilities`, `Limits`, `Record`, `AppendRequest` and `Envelope` over `reflect`; and `Key`, `Cursor`, `Version`, `Position` asserted to be defined types (`Type.Name()` is the name, not `string`) that count upward. M47, M48, M49, M54, M83, M84, M85 and M86 re-applied, each red.

### GAP-T7 [medium][deferred] The six ceilings in `event/bounds.go` are asserted by nothing, in this section or any other

- **Where:** `event/bounds.go:9-16`; the derivations are PLAN.md:602-609, one paragraph per
  constant. `bounds.go` is one of S1's nine files (PLAN.md:1987).
- **What:** only `MaxNameBytes` is read by a test, and only relatively
  (`rendering_test.go:94` uses `MaxNameBytes+1`). Survivors: **M43** `MaxKeyBytes` `2<<10` →
  `2<<20` (past every index tuple the derivation cites); **M44** `MaxPayloadBytes` `1<<20` →
  `1<<10`; **M45** `MaxResidentBytes` → `0`; **M46** `MaxBatchCount` and `MaxPageCount` swapped —
  all **SURVIVED**. Nothing checks the relations the derivations rest on either: that
  `MaxResidentBytes >= MaxPayloadBytes`, that `MaxKeyBytes + MaxNameBytes + 8` stays inside the
  ~2 704-byte PostgreSQL btree tuple the row cites, or that `MaxBatchCount` is
  `MaxResidentBytes / jobs.DefaultPayloadBytes`.
- **Why this severity:** medium, and it is `universality.md`'s "magic thresholds with … no
  calibration note" arriving from the other side: the notes exist, in the plan, and nothing binds
  them to the numbers. A wrong `MaxKeyBytes` is invisible even in S4, because S4's checks are
  written in terms of the constant.
- **Why this timing:** deferred. S4 is where the ceilings are first *enforced*, and the natural
  place for the relation assertions is beside that enforcement. Nothing in S2 or S3 bends around
  this. It must be carried into `## Debt` rather than dropped, because after S4 nobody will look
  again.
- **Close criteria:**
  - [ ] A test in `event/` asserts each of the six relations the derivation table states, in the
        table's own terms — including `MaxResidentBytes / MaxPayloadBytes >= 64` (the recommended
        page) and `MaxKeyBytes + MaxNameBytes + 8 <= 2704`.
  - [ ] M43, M44, M45 and M46 are re-applied and each turns the suite red.
- **Status:** **deferred, carried** — round 2. Moved to the plan's `## Debt` as `[medium][deferred]`, owner **S4**, beside the enforcement of the ceilings, with the four relations the derivation table rests on named.

### GAP-T8 [medium][deferred] `Support`'s three-state distinction — the reason the type exists — is asserted by nothing

- **Where:** `event/store.go:8-30`, whose comment is *"Three states rather than two, because a
  capability nobody stated and one a store denied are different answers"*; `stated()` at `:30` is
  the only function in `event/` with **0.0 % coverage** (`go tool cover -func`).
- **What:** **M31** (`Support.String` drops the `Unsupported` arm), **M50** (`stated()` forgets
  `Unsupported`), **M51** (`stated()` answers true for everything) and **M80** all **SURVIVED**.
  `Support` appears in the suite only as `Supported.String()` inside
  `concurrency_test.go:40` and as `Support(value).String()` in the rendering inventory, neither of
  which distinguishes the three answers.
- **Why this severity:** medium. §INV-043 ("an unstated capability is refused, at both doors") is
  the invariant this type serves and it is S4/S5's, so nothing is wrong today; what is missing is
  any S1 assertion that the three answers are three.
- **Why this timing:** deferred — the plan already records `stated` as S4's only uncalled function
  (PLAN.md:2289-2294, GAP-167's second criterion) and closing it here would test a function with no
  caller. It belongs in S4 beside `Bind`'s and `Read`'s reads of it, and must be carried in
  `## Debt`.
- **Close criteria:**
  - [ ] `Unstated`, `Unsupported` and `Supported` are asserted to render three distinct phrases and
        to answer `stated()` as false/true/true.
  - [ ] M31, M50 and M51 are re-applied and each turns the suite red.
- **Status:** **partly closed, remainder carried** — round 2. The rendering half is closed by GAP-T5's distinct-phrase count (M31 and M80 are red). `stated()` has no caller in S1, so its false/true/true answer is carried to the plan's `## Debt` as `[medium][deferred]`, owner **S4**, beside `Bind`'s and `Read`'s reads of it.

### GAP-T9 [low][immediate] `refuse(nil)` is the one statement in the refusal path with no test

- **Where:** `event/errors.go:204-206`; `go tool cover -func` reports `refuse` at **88.9 %**, and
  the missing statement is the `return nil`.
- **What:** **M30** removes the guard, so `refuse(nil)` returns a live `ErrUncertain`/`ErrBackend`
  refusal over a nil cause — **SURVIVED**. Every S4 call site is `return refuse(store.Append(...), appendDoor)`
  shaped, so the guard is on the **happy path of every successful append and read**, and its removal
  makes every successful operation return an error.
- **Why this severity:** low — S4's first successful-append test will fail loudly the moment it is
  written, so this cannot ship far. It is listed because it is the only uncovered statement in the
  section's central function and because it is one line to close.
- **Why this timing:** immediate — one assertion, and S4 depends on it on its first line.
- **Close criteria:**
  - [ ] `refuseAppend(nil) == nil` and `refuseRead(nil) == nil` are asserted.
  - [ ] M30 is re-applied and turns the suite red.
- **Status:** **closed** — round 2. `TestAnOutcomeOutsideTheVocabularyNormalises/a door that was handed no failure refuses nothing`. M30 re-applied, red.

### GAP-T10 [low][deferred] The walk's budget has no absolute assertion; above ~4 096 the only detector is a ten-second wall clock

- **Where:** `event/walk_test.go:69-151`; every assertion is written in terms of `causeHops`
  (`:82`, `:87`, `:134`), so the budget scales with the mutation.
- **What:** **M96** `causeHops = 64` → `4096` — **SURVIVED**. **M97** `= 65536` and **M41**
  `= 1 << 20` are caught, but by the cyclic-chain case timing out or the process being killed, not
  by an assertion — the run reported `signal: terminated` after 12 s rather than a named failure.
  So the budget can be widened 64× silently, and beyond that the detector is the suite's one
  wall-clock construct.
- **Why this severity:** low. 4 096 visits is still bounded and still cheap; the property the
  finding names — "the work is bounded by the budget and not by the depth" — is genuinely proved,
  and it caught **M10** (budget per path, 262 143 visits against 64) and **M36** and **M105**.
- **Why this timing:** deferred — a nice-to-have absolute row, and the relative form is the right
  shape for the property that matters.
- **Close criteria:**
  - [ ] One assertion pins `causeHops` to a stated absolute value with the reason ("a chain deeper
        than N is a defect in the party that built it"), so a silent widening is a visible diff.
  - [ ] M96 is re-applied and turns the suite red.
- **Status:** **deferred, carried** — round 2. Moved to the plan's `## Debt` as `[low][deferred]`, owner **S5**.

### GAP-T11 [low][deferred] Three small edges: the `MaxNameBytes` boundary, `Compose` at an arity other than three, and a dead `Log`-embedding branch

- **Where:** `event/rendering_test.go:94` (only `MaxNameBytes+1` is driven, never
  `MaxNameBytes` exactly); `event/fuzz_test.go:54` (`f.Fuzz` takes exactly three strings, so
  `Compose()`, `Compose("a")` and the two-part/four-part cases are never composed);
  `event/store_test.go:137-143` (`if !store.Implements(log) && !reflect.PointerTo(store).Implements(log)`).
- **What:** (1) the on-the-boundary family — the longest name that must still render — is asserted
  nowhere, so `checkText`'s `>` could become `>=` at `Stream.String`'s call site undetected;
  (2) the plan's own injectivity argument names one colliding pair, `Compose()` and `Compose("")`,
  and nothing exercises either; **M67** (join with no separator) is caught only because the
  compiler flags the now-unused `index`; (3) the `Log` branch can only be entered when `Store` has
  already lost a `Log` method, which the `NumMethod` check three lines above catches first —
  **M91** (`Store` spells `Log`'s four methods inline instead of embedding) **SURVIVED**, so the
  "a store is a log" relation is asserted by nothing.
- **Why this severity:** low — all three are edges of properties that are otherwise well covered.
- **Why this timing:** deferred; S2 owns `Compose`'s table test and S4 owns the text rule's doors.
- **Close criteria:**
  - [ ] A family of exactly `MaxNameBytes` is asserted to render its own name.
  - [ ] `Compose()` and `Compose("")` are asserted to render the same empty key, and the empty key
        is asserted to be refused by the kernel text rule — the plan's own "both are refused as
        empty at every door" (PLAN.md:556-558).
  - [ ] The `Log` relation is asserted directly — `var _ Log = (Store)(nil)` in a compile position,
        or an `ast` check that `Store` embeds `Log` — and M91 turns the suite red.
- **Status:** **partly closed, remainder carried** — round 2. The second edge is closed: `TestComposeRendersTheFrozenKey` composes one, two and three parts and pins the `Compose()` / `Compose("")` pair together with its refusal as empty. The `MaxNameBytes` boundary (owner **S4**) and the `Log` relation (owner **S6**) are carried to the plan's `## Debt` as `[low][deferred]`.

### GAP-T12 [low][deferred] The two `go/ast` checks assert on the source text of an unexported function and on one file name

- **Where:** `event/refusal_test.go:102-145` (`namesDeclaredInErrorsFile`, `namesListedInTheGate`);
  `event/store_test.go:78-113` (`exportedInterfaces`).
- **What:** `namesListedInTheGate` reads the identifiers inside the function literally named
  `vocabulary`, so any refactor that keeps the behaviour and changes the shape fails the test —
  **M106** (memoising `vocabulary()` behind a `buildVocabulary()`) is reported as *"the partition
  gate and the vocabulary have drifted apart"*, which is a false statement about a behaviourally
  identical program. `namesDeclaredInErrorsFile` reads only `errors.go`, so an `Err*` sentinel
  declared in any other file of the package is invisible to the partition table and to the
  three-multiset check that exists to stop exactly that.
- **Why this severity:** low. This is GAP-163's deliberate closure and it is worth more than it
  costs — it caught **M71** (a sentinel dropped from the gate) and **M102** (the gate answering
  false) — but it is an assertion on internals per the rubric's item 4, and the one-file scope is a
  real hole in the check's own claim.
- **Why this timing:** deferred — S6's doc and surface pass is the moment to widen it.
- **Close criteria:**
  - [ ] `namesDeclaredInErrorsFile` walks every non-test `.go` file under `event/`, the way
        `exportedInterfaces` already does, so a sentinel declared elsewhere joins the table.
  - [ ] The gate check reads `vocabulary()`'s **returned value** where it can and its source only
        for the drift claim, or its failure message says "the gate's source list" rather than
        asserting a behavioural drift it did not observe.
- **Status:** **deferred, carried** — round 2. Moved to the plan's `## Debt` as `[low][deferred]`, owner **S6**.

---

## Coverage of S1's assigned matrix rows

Every row S1's `Covers` claims, checked against a test that would fail on regression. **No row is
without one**, which is the section's strongest result and why nothing above is `[critical]`.

| Item | Test | Verdict |
|---|---|---|
| UC-034 the map | `TestAnOutcomeOutsideTheVocabularyNormalises` (2 doors × 7 outcomes, + an out-of-range value at both doors) | proved; M04/M05/M60–M62/M73 all caught |
| UC-048 `Refused` | `TestTheRefusalVocabularyIsAPartition/a cause the framework already maps still arrives` + `/a decorator's own error stays matchable` | proved; M33 and M02/M03 caught |
| UC-060 the classification half | `TestAnOutcomeOutsideTheVocabularyNormalises`, `TestADeclaredWrapIsReachableByErrorsAsAndACauseIsNot` | proved |
| INV-001 append-only (method inventory) | `TestTheStoreSeamHasEightMethodsAndNoneMutatesOrQueries` | proved; M26/M92 caught, with two probe interfaces as the control |
| INV-011 the message table | `TestEveryRenderingNamesAClassAndNeverAValue` | proved; the positive half was **GAP-T5**, closed in round 2 |
| INV-012 not a CRUD collection | same | proved |
| INV-016 `Equal`, never `==` | `TestABackingAndAnAuthorityAreComparedAndNeverIdentical/the comparison is the framework's own` | proved; M18/M38/M56 caught |
| INV-018 the contract | contract clause only; S4 proves it | correctly deferred by the plan |
| INV-021 the clauses | comment only; S2/S3/S5 prove it | correctly deferred |
| INV-024 the partition | `TestTheRefusalVocabularyIsAPartition` (8 064 probes, exactly-one-match count, `go/ast` set check) | proved; 11 mutations caught |
| INV-025 no data in a refusal | `TestEveryRenderingNamesAClassAndNeverAValue` + `TestAContextCauseNeverTravelsThroughARefusal/a store's text` | proved; M21/M22/M70 caught |
| INV-026 a conflict carries no version | `…/a conflict names no version and no accessor reaches one` | proved (message + a `reflect` sweep of every method's numeric returns) |
| INV-027 the seam declares no transaction control | `TestTheStoreSeamHasEightMethods…` (`Begin`/`Commit`/`Rollback`/`Savepoint` in `forbiddenVerbs`) | proved |
| INV-028 an authority **is** its transaction | `TestABackingAndAnAuthorityAreComparedAndNeverIdentical/an authority is its transaction` | proved; M19/M20/M58/M59 caught. §GAP-152's value-typed identity is knowingly left to S5 |
| INV-029 cancellation identity, uncertainty outranks | `TestAContextCauseNeverTravelsThroughARefusal` (4 subtests, both windows, a control) | proved; M01/M06 caught |
| INV-030 all eight required | `TestTheStoreSeamHasEightMethods…/the seam is eight required methods` | proved for the count; the `Log` relation is **GAP-T11**, carried to `## Debt` (S6) |
| INV-033 `Compose` legality + injectivity | `FuzzComposeRendersAKeyThatIsLegalAndReversible`, `TestComposeRendersTheFrozenKey` | legality and reversibility proved over 9.7 M inputs; the **freeze** was **GAP-T1**, closed in round 2 by a golden table |
| INV-038 the inert rows under `-race` | `TestTheKernelsValuesAnswerTheSameFromManyGoroutines` | the values are exercised; the runtime half was **GAP-T4**, closed in round 2 by removing the warm-up |
| INV-045 a store classifies, the kernel maps | `TestAnOutcomeOutsideTheVocabularyNormalises/every classification maps the same way at both doors` + `/a classification a decorator wrapped is still the store's` | proved; M72 caught |
| C2 `Refused` + `ErrRefused` | partition test, subtests 5 and 6 | proved |
| C3 the context cause is unreachable | `TestAContextCauseNeverTravelsThroughARefusal` | proved |
| C5 the `Is`/`As` split | `TestADeclaredWrapIsReachableByErrorsAsAndACauseIsNot` (5 subtests) | proved; M07/M08/M23/M24/M25/M64/M65/M88/M89/M90/M93/M104 all caught |
| C10.3, C10.5 | contract comment only | correctly deferred |

**No test in the eight files lacks a UC or INV behind it.** No scope creep found.

**Doubles discipline.** Zero mocks. Every fixture is a hand-written error type of ten lines or less
(`joinedCause`, `cyclicCause`, `wrappingCause`, `permissiveMatcher`, `asOnlyFault`, `connection`,
`identityHolder`, `mutatingLog`, `typedLog`), each exists to be an *input* the kernel reads rather
than a stand-in for something the project owns, and no assertion is made against a fixture the test
configured. `crud.SameDataSource` and `errs.AsFault` are called for real, never patched. This is
the best-behaved part of the suite.

**Universality.** Nothing is fitted to one aggregate. The domain strings (`accounts.account`,
`acme/A-17`, `"the ledger database"`) appear only as opaque inputs, never as branch conditions, and
every table is driven off `declaredVocabulary()`, `classifiedOutcomes()`, `doors()` or a `range 256`
sweep rather than a sample. The one universality-shaped risk is the **inverse** of a hardcode —
GAP-T1's absent golden — and it is graded there.

---

## Mutation log — 107 breakages, 75 caught, 32 survived

Applied to the working tree one at a time, `go test -count=1 ./event/`, reverted; `diff -r` against
a pre-campaign copy afterwards reports the tree byte-identical, `gofmt -l event` silent,
`go vet ./event/` clean, `go test -race -count=1 ./event/` green.

### Survived — 32

| # | Mutation | Where | Finding |
|---|---|---|---|
| M27 | `refusal.As` drops its `defer recover` | `errors.go:134-138` | GAP-T3 |
| M29 | `sameError` drops its `defer recover` | `errors.go:356` | GAP-T3 |
| M30 | `refuse` drops its `err == nil` guard | `errors.go:204-206` | GAP-T9 |
| M31 | `Support.String` drops the `Unsupported` arm | `store.go:21-22` | GAP-T8 |
| M43 | `MaxKeyBytes` `2<<10` → `2<<20` | `bounds.go:12` | GAP-T7 |
| M44 | `MaxPayloadBytes` `1<<20` → `1<<10` | `bounds.go:10` | GAP-T7 |
| M45 | `MaxResidentBytes` → `0` | `bounds.go:15` | GAP-T7 |
| M46 | `MaxBatchCount` and `MaxPageCount` swapped | `bounds.go:13-14` | GAP-T7 |
| M47 | `Limits` loses `MaxKey` | `store.go:46` | GAP-T6 |
| M48 | `Limits` loses `MaxRead` | `store.go:48` | GAP-T6 |
| M49 | `Capabilities` loses `MonotoneVisibility` | `store.go:35` | GAP-T6 |
| M50 | `Support.stated` forgets `Unsupported` | `store.go:30` | GAP-T8 |
| M51 | `Support.stated` answers true for everything | `store.go:30` | GAP-T8 |
| M54 | `AppendRequest` loses `Expected` | `store.go:70` | GAP-T6 |
| M57 | `Backing.String` never says `invalid` | `backing.go:27-29` | GAP-T5 |
| M68 | escape hex becomes lower case | `identity.go:35` | **GAP-T1** |
| M69 | `composeSeparator` `'/'` → `':'` | `identity.go:33` | **GAP-T1** |
| M75 | `Authority.Valid` ignores its backing | `authority.go:41` | equivalent mutant — `NewAuthority` already refuses an invalid backing, so no reachable value differs |
| M76 | `refusal.Error()` returns one constant for all 24 | `errors.go:99` | GAP-T5 |
| M78 | `Authority.String` renders `"[event backing]"` | `authority.go:44` | GAP-T5 |
| M79 | `Backing.String` renders `"[event authority]"` | `backing.go:30` | GAP-T5 |
| M80 | `Support.String` renders one phrase for all three | `store.go:19-28` | GAP-T5, GAP-T8 |
| M83 | `type Version uint64` → `int64` | `identity.go:10` | GAP-T6 |
| M84 | `type Position uint64` → `int64` | `identity.go:12` | GAP-T6 |
| M85 | `type Key string` → `type Key = string` | `identity.go:8` | GAP-T6 |
| M86 | `type Cursor string` → `type Cursor = string` | `identity.go:14` | GAP-T6 |
| M87 | `Compose` escapes every byte | `identity.go:73-75` | **GAP-T1** |
| M91 | `Store` spells `Log`'s four methods inline instead of embedding | `store.go:138-139` | GAP-T11 |
| M96 | `causeHops` `64` → `4096` | `errors.go:263` | GAP-T10 |
| M100 | `type Outcome uint8` → `uint16` | `outcome.go:3` | GAP-T6 (noted, not itemised) |
| M103 | `matches` stops consulting `asksIs` | `errors.go:330` | **GAP-T2** |
| M107 | `inVocabulary` memoises into a lazy package-level cache | `errors.go:121` | **GAP-T4** |

### Caught — 75, with the test that caught each

| # | Mutation | Caught by |
|---|---|---|
| M01 | `refusal.Is` drops the context suppression | `TestAContextCauseNeverTravelsThroughARefusal` |
| M02 | `refusal.Is` also reaches the cause | `TestTheRefusalVocabularyIsAPartition` |
| M03 | `refusal.Is` drops the `inVocabulary` gate | `TestTheRefusalVocabularyIsAPartition` |
| M04 | `Failure` stops normalising an out-of-range outcome | `TestAnOutcomeOutsideTheVocabularyNormalises` |
| M05 | both doors take one fail-safe default | `TestAnOutcomeOutsideTheVocabularyNormalises` |
| M06 | `refuse` stops recognising a bare cancellation | `TestAContextCauseNeverTravelsThroughARefusal` |
| M07 | a backend refusal never declares the retryable class | `TestADeclaredWrapIsReachableByErrorsAsAndACauseIsNot` |
| M08 | `retryable` never finds a fault | `TestADeclaredWrapIsReachableByErrorsAsAndACauseIsNot` |
| M09 | `findAs` drops its nil guard | `TestANilOrLyingCauseNeverPanicsTheKernel` (panic) |
| M10 | the walk budget is spent per path (262 143 visits vs 64) | `TestAJoinedCauseCannotOutlastTheWalksBudget` |
| M11 | `Stream.String` renders any family | `TestEveryRenderingNamesAClassAndNeverAValue` |
| M12 | the text rule stops refusing control characters | `TestEveryRenderingNamesAClassAndNeverAValue` |
| M13 | `Compose` stops escaping the separator | `FuzzCompose…` (seed corpus) |
| M14 | `Compose` stops escaping the escape byte | `FuzzCompose…` |
| M15 | `Compose` stops escaping invalid UTF-8 | `FuzzCompose…` |
| M16 | `NewBacking` accepts an identity nil by any route | `TestABackingAndAnAuthority…` |
| M17 | `NewBacking` accepts an uncomparable identity | `TestABackingAndAnAuthority…` |
| M18 | `Backing.Equal` becomes `==` | `TestABackingAndAnAuthority…` |
| M19 | `Authority.Same` ignores the backing | `TestABackingAndAnAuthority…` |
| M20 | `Authority.Valid` always true | `TestABackingAndAnAuthority…` |
| M21 | a classification renders its cause | `TestEveryRenderingNamesAClassAndNeverAValue` |
| M22 | a refusal renders its cause | `TestEveryRenderingNamesAClassAndNeverAValue` |
| M23 | `causeAsWrap` promotes any cause | `TestAJoinedCauseCannotOutlastTheWalksBudget` |
| M24 | an over-a-bound refusal carries no fault | `TestADeclaredWrapIsReachableByErrorsAsAndACauseIsNot` |
| M25 | `ErrKey` declares no request class | `TestADeclaredWrapIsReachableByErrorsAsAndACauseIsNot` |
| M26 | the `Store` seam gains a delete | `TestTheStoreSeamHasEightMethods…` |
| M28 | `walk` drops its `defer recover` | `TestANilOrLyingCauseNeverPanicsTheKernel` (panic) |
| M32 | `Outcome.String` collapses `Closed` into the default | `TestAnOutcomeOutsideTheVocabularyNormalises` |
| M33 | a policy refusal maps to `ErrBackend` | `TestTheRefusalVocabularyIsAPartition` |
| M34 | a bad cursor maps to `ErrBackend` | `TestTheRefusalVocabularyIsAPartition` |
| M35 | `CauseOf` answers nothing | `TestADeclaredWrapIsReachableByErrorsAsAndACauseIsNot` |
| M36 | the walk stops following a joined branch | `TestAJoinedCauseCannotOutlastTheWalksBudget` |
| M37 | `Authority.MarshalJSON` succeeds | `TestABackingAndAnAuthority…` |
| M38 | `nilByAnyRoute` only sees a nil interface | `TestABackingAndAnAuthority…` |
| M39 | the text rule stops refusing invalid UTF-8 | `TestEveryRenderingNamesAClassAndNeverAValue` |
| M40 | the text rule stops applying its cap | `TestEveryRenderingNamesAClassAndNeverAValue` |
| M41 | `causeHops` → `1 << 20` | the cyclic-chain guard (a hang, not a named assertion — see GAP-T10) |
| M42 | `causeHops` → `2` | `TestAJoinedCauseCannotOutlastTheWalksBudget` |
| M52 | `Envelope` loses `Position` | the compiler (`concurrency_test.go:23`) |
| M55 | the `Store` seam drops `Transaction` | `TestTheStoreSeamHasEightMethods…` |
| M56 | `Backing.valid` answers true for an unstated backing | `TestABackingAndAnAuthority…` |
| M58 | `NewAuthority` accepts an invalid backing | `TestABackingAndAnAuthority…` |
| M59 | `NewAuthority` accepts a nil transaction | `TestABackingAndAnAuthority…` |
| M60 | an unclassified append failure becomes a conflict | `TestAContextCauseNeverTravelsThroughARefusal` |
| M61 | `Conflict` maps to `ErrUncertain` | `TestAContextCauseNeverTravelsThroughARefusal` |
| M62 | `Closed` maps to `ErrRefused` | `TestAContextCauseNeverTravelsThroughARefusal` |
| M63 | `ErrSealed` no longer wraps `ErrDeclaration` | `TestTheRefusalVocabularyIsAPartition` |
| M64 | `ErrConflict` no longer wraps `crud.ErrConflict` | `TestADeclaredWrapIsReachableByErrorsAsAndACauseIsNot` |
| M65 | `ErrUncertain` silently wraps the retryable class | `TestADeclaredWrapIsReachableByErrorsAsAndACauseIsNot` |
| M66 | `Compose` stops escaping a control rune | `FuzzCompose…` |
| M67 | `Compose` joins with no separator | the compiler |
| M70 | `Stream.String` renders the key too | `TestEveryRenderingNamesAClassAndNeverAValue` |
| M71 | `vocabulary()` loses `ErrConflict` | `TestTheRefusalVocabularyIsAPartition` |
| M72 | `refuse` reads the classification by a bare type assertion | `TestANilOrLyingCauseNeverPanicsTheKernel` |
| M73 | `Failure` normalises with `>=` | `TestAContextCauseNeverTravelsThroughARefusal` |
| M74 | `checkText` stops refusing an empty value | `TestEveryRenderingNamesAClassAndNeverAValue` |
| M77 | every classification renders one phrase | `TestAnOutcomeOutsideTheVocabularyNormalises` |
| M81 | `Failure` drops the store's cause | 4 tests |
| M82 | `newRefusal` drops the cause | 3 tests |
| M88 | `refusal.Is` answers true for everything outside the vocabulary | `TestADeclaredWrap…`, `TestAJoinedCause…` |
| M89 | `refusal.As` answers for the cause too | 3 tests |
| M90 | `CauseOf` answers the wrap instead of the cause | 3 tests |
| M92 | `Log` gains a delete | `TestTheStoreSeamHasEightMethods…` |
| M93 | `tooLarge` names no numbers in its fault | `TestADeclaredWrapIsReachableByErrorsAsAndACauseIsNot` |
| M94 | the two doors' fail-safe defaults are swapped | 3 tests |
| M95 | `escapeByte` writes one hex digit | `FuzzCompose…` |
| M97 | `causeHops` → `65536` | the cyclic-chain guard |
| M98 | `Stream.Family` checked against a 1 MiB cap | `TestEveryRenderingNamesAClassAndNeverAValue` |
| M99 | `Stream.String` applies no cap | `TestEveryRenderingNamesAClassAndNeverAValue` |
| M101 | the walk visits nothing | 4 tests |
| M102 | `inVocabulary` answers false always | `TestTheRefusalVocabularyIsAPartition` |
| M104 | `findAs` never consults an `As` method | `TestADeclaredWrap…`, `TestANilOrLying…` |
| M105 | `withinBudget` always answers true | `TestAJoinedCauseCannotOutlastTheWalksBudget` |
| M106 | `vocabulary()` memoised behind a `buildVocabulary()` | `TestTheRefusalVocabularyIsAPartition` (by source shape — see GAP-T12) |
| M108 | the gate rewrites package-level state on every call | `-race` (5 `DATA RACE` warnings) |

*(M53 — `Record` loses its revision — was skipped: the anchor text is not unique in `event/store.go`.)*

---

## Round 1 verdict

**Four `[high][immediate]` findings, none `[critical]`.** Every UC and INV S1's `Covers` claims has
a test that would fail on regression, the eleven named tests exist and run in 1.0 s under `-race`,
the suite is deterministic across `-count=5` and three `-shuffle=on` runs, and 75 of 107
deliberate breakages go red. Nothing is dishonest: no skip, no widened tolerance, no test written
to match a wrong implementation, and the checkpoint transcript in the plan reproduces.

What blocks the gate is four holes with the same shape — a property the code implements
deliberately, stated in a comment or an invariant, that no assertion reaches: the **frozen key
rendering** (GAP-T1), the **custom-`Is` half of the bounded traversal** (GAP-T2), **two of the four
panic guards** (GAP-T3), and the **runtime half of §INV-013** (GAP-T4, defeated by one line of
warm-up in the concurrency test). All four are cheap — a golden table, two fixtures and a moved
statement — and all four are in the section that writes the contract every later section is
measured against.

---

## Round 2 — closing the review's `[immediate]` findings — 2026-09-07

**No production code changed.** Every finding was a property `event/` already
implemented and no assertion reached, so the whole of round 2 is test code plus
the doc updates the closures oblige.

**Closed:** GAP-T1, GAP-T2, GAP-T3, GAP-T4, GAP-T5, GAP-T6, GAP-T9 — the four
`[high][immediate]`, both `[medium][immediate]` and the one `[low][immediate]`.
**Carried to the plan's `## Debt`:** GAP-T7 (S4), GAP-T8's `stated()` half (S4),
GAP-T10 (S5), GAP-T11's two remaining edges (S4 and S6), GAP-T12 (S6).

**Two tests added**, so the section's count clause moves from 11 to 13 and the
plan's phase-5 checkpoint, its two name lists and the coverage matrix rows for
§INV-005, §INV-018 and §INV-033 were updated in the same change:
`TestComposeRendersTheFrozenKey` (`event/identity_test.go`, new file) and
`TestTheSeamsValuesAreDefinedTypesWithTheDeclaredFields` (`event/store_test.go`).
The other five closures are subtests of tests that already existed, so no name
moved.

**Mutation evidence — 22 breakages, all 22 red.** Each was applied to the working
tree one at a time, run under `go test -race -count=1 ./event/`, and reverted;
`diff -r` against a pre-campaign copy afterwards reports the tree byte-identical.
Every one of them is a mutation round 1's own campaign recorded as **surviving**.

| # | Mutation | Now caught by |
|---|---|---|
| M69 | `composeSeparator` `'/'` → `':'` | `TestComposeRendersTheFrozenKey/the rendering is the one every stream was written under` |
| M68 | escape hex becomes lower case | same |
| M87 | `Compose` escapes every byte | same |
| M103 | `matches` stops consulting `asksIs` | `TestADeclaredWrapIsReachableByErrorsAsAndACauseIsNot/a class a cause answers for only through an Is method is still found` **and** `TestAContextCauseNeverTravelsThroughARefusal/a cancellation a cause answers for only through an Is method travels the same way` |
| M27 | `refusal.As` drops its `defer recover` | `TestANilOrLyingCauseNeverPanicsTheKernel/a matcher that panics is that party's defect and not the kernel's` — `panic: a matcher a store wrote wrong [recovered, repanicked]` |
| M29 | `sameError` drops its `defer recover` | `TestANilOrLyingCauseNeverPanicsTheKernel/a cause nothing can compare does not end the walk` |
| M30 | `refuse` drops its `err == nil` guard | `TestAnOutcomeOutsideTheVocabularyNormalises/a door that was handed no failure refuses nothing` |
| M107 | `inVocabulary` memoises into a lazy package-level cache | `TestTheKernelsValuesAnswerTheSameFromManyGoroutines` — `WARNING: DATA RACE` under `-race` |
| M76 | `refusal.Error()` returns one constant for all 24 | `TestEveryRenderingNamesAClassAndNeverAValue/a rendering says which kind of thing it is and which one of them` |
| M78 | `Authority.String` renders `"[event backing]"` | same |
| M79 | `Backing.String` renders `"[event authority]"` | same |
| M57 | `Backing.String` never says `invalid` | same |
| M80 | `Support.String` renders one phrase for all three | same |
| M31 | `Support.String` drops the `Unsupported` arm | same |
| M47 | `Limits` loses `MaxKey` | `TestTheSeamsValuesAreDefinedTypesWithTheDeclaredFields/every value the seam is written in carries the fields the contract names and no others` |
| M48 | `Limits` loses `MaxRead` | same |
| M49 | `Capabilities` loses `MonotoneVisibility` | same |
| M54 | `AppendRequest` loses `Expected` | same |
| M85 | `type Key = string` | same, **and** `/the four identity types are types of their own and count upward` |
| M86 | `type Cursor = string` | `/the four identity types are types of their own and count upward` |
| M83 | `type Version uint64` → `int64` | same |
| M84 | `type Position uint64` → `int64` | same |

**Runs after the campaign, on the restored tree.**

```
go test -race -count=2 ./event/...     ok  1.058s
go test -race -count=1 -shuffle=on ./event/  (x3)   ok  1.032 / 1.033 / 1.034s
go test -list … | grep -cE '^(Test|Fuzz)'           13
go vet ./event/...                     EXIT=0
gofmt -l .                             silent
make unit                              EXIT=0, zero FAIL lines
make check                             nine arms, all ok
```

### Where round 1 was argued with

Nowhere on substance. Two notes on scope rather than disagreement:

1. **GAP-T5's first close criterion** asks that "every rendering in the inventory
   is asserted to contain a token that identifies its own kind". What was written
   is the criterion's own worked example — distinctness — rather than a
   per-rendering token map: a token map would have to name each of the ~540
   inventoried strings, which is a second copy of the implementation and the
   assertion on internals the review's own rubric item 4 names. Distinctness is
   what the five mutations the finding names actually break, and all five are red.
2. **GAP-T8** is graded `[medium][deferred]` and its close criteria mix two
   properties: the three renderings (S1's, and closed here as a by-product of
   GAP-T5) and `stated()`'s three answers (S4's, because the function has no
   caller until `Bind` and `Read` read it). Only the second half is carried.

---

## Round 3 — econv test reviewer (clean context, re-audit after round 2) — 2026-09-07

**What was graded.** The thirteen tests and one fuzz target in
`event/{refusal,walk,outcome,rendering,store,backing,concurrency,identity,fuzz}_test.go`
(1 796 test lines) against `event/{doc,identity,bounds,text,backing,authority,outcome,errors,store}.go`,
the plan's §S1, §Contracts, §Coverage matrix, §Debt and §Carried gaps,
`.agents/artifacts/usecases/EVENTSOURCE_P1_USECASES.md` §INV-024/025/026/029/030/038/045 and
§UC-034/048/060, this file's rounds 1–2, `CLAUDE.md`, and
`~/.claude/skills/econv/references/{gaps,universality,architecture,building-blocks,data-integrity,microkernel,restrictions,readability}.md`.

**Runs, all in this worktree.**

```
go test -race -count=1 ./event/                       ok  1.035s   (suite runtime: 1.0 s)
go test -race -count=3 ./event/                       ok  1.090s
go test -race -count=1 -shuffle=on ./event/  (x3)     ok  1.037 / 1.037 / 1.037s
go vet ./event/...                                    EXIT=0
gofmt -l .                                            silent
go test -count=1 -cover ./event/                      99.4% of statements
go tool cover -func                                   the one uncovered function is Support.stated (0.0%)
go test -list '^(…the thirteen…)$' | grep -cE '^(Test|Fuzz)'   13
go test -run '^$' -fuzz Fuzz… -fuzztime 20s ./event/  4 791 249 execs, 85 corpus, PASS
```

**No flake, no order dependence, no leftover state.** No `t.Parallel`, no `time.Sleep`, no
network, no filesystem write, no `os.Getenv`, no unseeded randomness; `event/testdata` does not
exist after a fuzz campaign. The one wall-clock construct stays `time.After(10 * time.Second)` at
`walk_test.go:121`, a hang guard rather than a timing assertion. No `t.Skip`, no `xfail`, no
commented-out assertion, no widened tolerance. Unit only.

**Round 2's own claims were re-verified, not taken.** Every one of the twenty-two mutations round 2
tabulated as "now caught" was re-applied here; all twenty-two are red again, including M-I5
(`inVocabulary` memoising into a lazy package-level cache), which is green under a bare
`go test` and red under `-race` on three consecutive runs — and `-race` is what both the phase-4
and the phase-5 checkpoint command run, so the claim holds as written. The `-list` count is 13
against a plan that says 13, and every one of the thirteen names exists.

**Mutation campaign — 103 deliberate breakages.** Each was applied to the working tree one at a
time, run under `go test -count=1 ./event/`, and reverted; `diff -r` against a pre-campaign copy
reports the tree byte-identical, and `go test -race -count=1 ./event/` is green on the restored
tree. Two are excluded from the score: **M-F5** removed a check whose only other use of `reflect`
was the same statement, so it failed to compile rather than failing a test (re-run as **M-F5b**,
red), and **M-B5** rewrote `Compose`'s separator placement from a `index > 0` prefix to an
`index < len(parts)-1` suffix, which is byte-for-byte the same rendering. Of the **101 scored,
80 are caught and 21 survive**. Eight of the twenty-one are already recorded and carried in the
plan's `## Debt` (GAP-T7's five ceilings, GAP-T8's `stated()`, GAP-T10's `causeHops`, GAP-T11's
`Log`-embedding), and they are confirmed still open. **Thirteen are new**, and they are the six
findings below. The full log is at the end of this round.

**What is genuinely strong, and is stronger than round 1 found it.** The partition test's
`go/ast` three-multiset check turns a shrunken `vocabulary()` red at the first sentinel
(M-I6); the intra-class edge list catches an `ErrSealed` that stops wrapping `ErrDeclaration`
(M-I7) and an `ErrPayload` that joins the request class (M-I9); the door map is pinned row by
row, so swapping the two doors, deleting the `Refused` arm and re-pointing `BadCursor` or
`Closed` are each red at a named row (M-K1, M-J4, M-J5, M-J6); `TestANilOrLyingCauseNeverPanicsTheKernel`
carries the only control in the suite that makes three panic guards non-vacuous, and removing
any of the four recovers is red (M-A7, M-A8, M-A9, M-A10); the frozen-key golden table round 2
added kills seven distinct rendering mutations including a nibble swap and a dropped control-rune
escape (M-B1…M-B7); and `TestEveryRenderingNamesAClassAndNeverAValue`'s distinctness map kills
every phrase collision I could write (M-I1…M-I4, M-H1, M-G4, M-G5). Coverage of the section's
own UC and INV rows is complete: for every row S1's `Covers` claims, I found at least one
mutation that turns a named test red. There is no missing-UC and no missing-INV finding in this
round.

---

### GAP-T13 [critical][immediate] A declared class wrap is asserted only on the bare sentinel, never on a refusal that carries it — a conflict silently renders 500 instead of 409

- **Where:** `event/errors.go:105` (the mutated statement); `event/refusal_test.go:345-392`
  (`TestADeclaredWrapIsReachableByErrorsAsAndACauseIsNot/every declared class wrap is reachable
  and was not replaced`), specifically `:353` and `:366`
- **What:** `refusal.Is` reaches the sentinel's own declared class wrap through
  `matches(this.sentinel, target)`. Changing that one call to `sameError(this.sentinel, target)`
  — the identity comparison that already exists two functions below — leaves the **entire suite
  green**:

  ```
  M-A1  if matches(this.sentinel, target) {  ->  if sameError(this.sentinel, target) {
        go test -count=1 ./event/   ok
        go test -race -count=1 ./event/  ok
  ```

  Measured with a throwaway probe, before and under the mutation (probe deleted, tree restored):

  ```
                                       before   under M-A1
  errors.Is(conflict refusal, crud.ErrConflict)     true     false
  errors.Is(oversized refusal, crud.ErrBadRequest)  true     false
  porthttp.Status(conflict refusal)                  409       500
  porthttp.Status(oversized refusal)                 413       413   (the fault, not the wrap)
  ```

  The subtest named for this property asserts the class wrap on the **bare sentinel value** only —
  `errors.Is(ErrConflict, crud.ErrConflict)` at `:366`, `errors.Is(member.err, crud.ErrBadRequest)`
  at `:353` — and a bare sentinel is not what a caller ever receives from a door. Every wrap that
  travels on a refusal's `sentinel` field is untested. The one class wrap the subtest *does* test
  through a refusal is `crud.ErrUnavailable`, and that one travels on the `wrapped` field
  (`backendRefusal`), which is a different statement.
- **Why this severity:** §UC-022 and §UC-034 turn on a caller distinguishing "the stream moved,
  re-load and decide again" from "the server broke". `ErrConflict` is built as
  `newRefusal(ErrConflict, nil, this.cause)` — the sentinel field is the **only** route to
  `crud.ErrConflict`, there is no fault and no wrap. Lose it and every optimistic-concurrency
  conflict in every application built on this framework renders 500. A 500 is the status clients
  and gateways retry blindly; a 409 is the one they must not. That is the primary write scenario
  broken and, downstream, the double write §INV-045's fail-safe default exists to prevent — and
  the suite that is supposed to freeze this contract says nothing.
- **Why this timing:** S1's whole purpose is to freeze the refusal contract (§Disagreements D2),
  and S4 is about to render these refusals through `porthttp` while S5's `refusal classes`
  section asserts what a decorated store's refusal answers. Both are written against a class-wrap
  route that this section's gate cannot see. §INV-024's own *Falsified by* asks for "a test per
  declared class wrap asserting the wrapped class is reachable and was not replaced"; what exists
  tests the declaration, not the reachability.
- **Close criteria:**
  - [ ] `TestADeclaredWrapIsReachableByErrorsAsAndACauseIsNot` asserts, for every refusal the
        kernel builds whose sentinel carries a class wrap, that `errors.Is(refusal, <the class>)`
        is true — at minimum `refuseAppend(Failure(Conflict, cause))` → `crud.ErrConflict` at both
        doors, and `tooLarge(...)` → `crud.ErrBadRequest`
  - [ ] the control is present: a refusal whose sentinel carries **no** class wrap (`ErrBackend`,
        `ErrCursor`, `ErrClosed`) answers `crud.ErrConflict` and `crud.ErrBadRequest` false
  - [ ] the list of (sentinel, class) pairs is derived from or cross-checked against
        `declaredVocabulary()` rather than hand-listed a second time, so a sentinel that gains or
        loses a wrap cannot slip past
  - [ ] M-A1 (`matches` → `sameError` on the sentinel arm of `refusal.Is`) turns the suite red,
        and the failure message names the status a caller would have been given
- **Status:** open

### GAP-T14 [high][immediate] The store seam's per-method signature is pinned by nothing — five signature rewrites survive the suite

- **Where:** `event/store_test.go:188-217`
  (`TestTheStoreSeamHasEightMethodsAndNoneMutatesOrQueries/the seam is eight required methods and
  no more`) and `event/store_test.go:14-28` (`seamTypes`); the contract under test is
  `event/store.go:111-122` (`Log`) and `:138-182` (`Store`)
- **What:** the test asserts (a) the **set of method names** is exactly eight, (b) no name
  contains a forbidden verb, and (c) every argument and result type is drawn from an allow-list.
  It never asserts what any one method's signature **is**. Five signature rewrites are green:

  ```
  M-H11  Close() error                                   -> Close()                                    SURVIVED
  M-H9   Transaction(ctx) (Authority, error)             -> Transaction(ctx) (Backing, error)          SURVIVED
  M-H8   ReadStream(ctx, Stream, after Version)          -> ReadStream(ctx, Stream, after Cursor)      SURVIVED
  M-H7   ReadAll(ctx, after Cursor) ([]Envelope, Cursor, error)
                                                         -> (…, Version, error)                        SURVIVED
  M-H10  Append(ctx, req AppendRequest) error            -> Append(ctx, s Stream, req AppendRequest)   SURVIVED
  M-H12  Backing() Backing                               -> Backing() Capabilities                     SURVIVED
  ```

  Each type involved is in `seamTypes`, so the allow-list waves it through. The value-side
  contract *is* pinned — `TestTheSeamsValuesAreDefinedTypesWithTheDeclaredFields` kills a lost
  `Limits.MaxKey`, an added `AppendRequest.Retries` and a `Record.Revision` that becomes a string
  (M-H3, M-H5, M-H13) — so the hole is exactly the interface half.
- **Why this severity:** these are the signatures a third-party store author in another repository
  compiles against, and the plan freezes them at the end of phase 1. `Close()` without an `error`
  deletes §INV-032's channel for a close failure. `Transaction` answering a `Backing` deletes
  §INV-028 — an authority *is* its transaction, and a backing is shared by every transaction on
  it, so two concurrent writers would compare `Same`. `ReadStream(after Cursor)` deletes
  §INV-009's dense per-stream versions from the read door. `ReadAll` answering a `Version`
  deletes §INV-035's persistable resume point. None of these is caught here; the first thing that
  would notice is a compile error in S3 or S4, which is exactly the "later work has to be
  rewritten" that `gaps.md` calls immediate.
- **Why this timing:** the seam is this section's headline deliverable and the freeze point for
  every later section and for `eventpg` in phase 2. A gate that cannot see a signature change is
  not guarding the thing the section exists to produce.
- **Close criteria:**
  - [ ] `TestTheStoreSeamHasEightMethodsAndNoneMutatesOrQueries` gains a table of the eight
        methods' **exact** signatures — in and out types in order — compared against
        `reflect.Type.Method(i).Type`, with the receiver-free `In`/`Out` lists written as literals
        rather than derived from the seam itself
  - [ ] M-H7, M-H8, M-H9, M-H10, M-H11 and M-H12 each turn the suite red at a named row
  - [ ] the existing allow-list check stays as the second, weaker net (it is what catches a type
        the contract never named at all)
- **Status:** open

### GAP-T15 [high][immediate] No enum's zero value is pinned; every assertion derives its expectation from the constant it is testing, so both fail-safe defaults can be renumbered with the suite green

- **Where:** `event/outcome.go:5-13` and `event/store.go:13-17` (the two `iota` blocks);
  `event/outcome_test.go:13-28` and `:83-93` (`classifiedOutcomes`, which names constants rather
  than numbers); `event/rendering_test.go:165-171` and `:186-189`
- **What:** two renumbering mutations are green:

  ```
  M-J2  const ( Unclassified Outcome = iota ; Conflict ; … )
        ->    ( Conflict     Outcome = iota ; Unclassified ; … )                 SURVIVED
  M-J1  const ( Unstated Support = iota ; Unsupported ; Supported )
        ->    ( Supported Support = iota ; Unstated ; Unsupported )              SURVIVED
  ```

  Every assertion in the suite is written in terms of the identifiers, so it moves with them:
  `outcome_test.go:19` computes `want := Outcome(value)` from the same constant it is checking,
  and `classifiedOutcomes()` says `{"Unclassified", Unclassified, nil}` — a row that is true
  whatever number `Unclassified` holds. The plan states both zero values as contract clauses in
  §Contracts: line 765 `Unclassified Outcome = iota // the zero value: the door's fail-safe
  default` and line 1025 `Unstated Support = iota // the zero value; Bind AND Read refuse it
  (INV-043)`. Neither is asserted anywhere.
- **Why this severity:** the zero value carries the fail-safe meaning in both enums, and it is the
  only thing that makes "a party that said nothing" safe. Under M-J2 a `failure` whose `outcome`
  field was never set — a store's own struct field, a decorator that rebuilds a classification, a
  value compiled against a later vocabulary — reads as **`Conflict`** rather than as the door's
  default. At an append door that turns "we do not know whether the write landed" into "it
  certainly did not, re-decide and append again", which is §D.14's silent double write, reached
  by the exact route §INV-045's fail-safe default exists to close. Under M-J1 a
  `Capabilities{}` — a store that returned the zero value, or a decorator that forgot to forward —
  reads as **every capability supported**, so §INV-043 ("an unstated capability is refused, at
  both doors") fails open: a store that never said it has transactions is admitted into the
  transactional path.
- **Why this timing:** both numbers are public contract and are frozen at the end of this phase;
  S3 (`eventmemory` states its capabilities), S4 (`Bind` and `Read` read `stated()`) and S5 (the
  conformance suite's three words) are all about to be written against them, and a store author in
  another repository will build composite literals over them.
- **Close criteria:**
  - [ ] a test asserts `var zero Outcome; zero == Unclassified` and, through the door, that
        `refuseAppend(&failure{cause: err})` — a classification whose outcome field was never set —
        is `ErrUncertain` and `refuseRead` of the same is `ErrBackend`
  - [ ] a test asserts `var zero Support; zero == Unstated`, and that `Capabilities{}` answers
        `Unstated` on all four fields
  - [ ] M-J1 and M-J2 each turn the suite red, and the failure message names the fail-open
        consequence rather than the number
- **Status:** open

### GAP-T16 [high][immediate] §INV-030's second half — "no optional interface in `event`" — is not asserted, and the walk that could assert it is already in the file

- **Where:** `event/store_test.go:241-257`
  (`TestTheStoreSeamHasEightMethodsAndNoneMutatesOrQueries/no exported interface in the package
  names a mutation or a query`), which calls `exportedInterfaces(t)` at `:78-113`
- **What:** the source walk inventories **every** exported interface declared under `event/` and
  then checks only that no method name contains a forbidden verb. Adding a second, optional
  interface whose method names are innocent is green:

  ```
  M-L2  type Snapshotter interface { Snapshot(ctx context.Context, s Stream) ([]byte, error) }
        added to event/store.go                                                  SURVIVED
  ```

  §INV-030's *Falsified by* in the use cases is literally "A method inventory showing **no
  optional interface** in `event`", and S1's `Covers` claims INV-030. The inventory exists; the
  assertion it was built for is missing. The set of exported interfaces is available at
  `:242` as `declared` and is used only for the two required-name checks at `:243-247`.
- **Why this severity:** this is the microkernel law, not a style rule. An optional interface in
  `event/` is the readmission of the capability-discovery tunnel that [[D-115]] was corrected to
  close and that [ES] obligation 3 forbids: the moment the kernel writes
  `if t, ok := store.(Transactional)`, a policing decorator that does not forward that interface
  is walked past for the whole transactional path, and §UC-048's refusing decorator stops being in
  the path at all. The regression is architectural and silent — nothing fails, a decorator just
  stops being consulted.
- **Why this timing:** S3, S4 and S5 all add types under `event/` and `event/eventmemory/`, and
  this is the section whose gate is supposed to make that addition visible. Once a second
  interface exists and the kernel reads it, the fix is a rewrite of the seam.
- **Close criteria:**
  - [ ] the subtest asserts the exported-interface set of `event/` is exactly `{Store, Log}`, with
        a message naming the tunnel it prevents
  - [ ] M-L2 turns the suite red
  - [ ] the existing forbidden-verb check over the same set is kept
- **Status:** open

### GAP-T17 [medium][deferred] A `failure` that grows an `Unwrap` survives, so the one property `event/outcome.go`'s comment calls deliberate is asserted by nothing

- **Where:** `event/outcome.go:34-43` (the comment and the type); the tests that would have to see
  it are `event/outcome_test.go:99-110` and `event/refusal_test.go:532-549`
- **What:** `outcome.go:34` states "A classified failure deliberately does not unwrap to its
  cause, so a classified cancellation cannot read as a bare one however the kernel orders its
  questions." Adding the method is green:

  ```
  M-G3  func (this *failure) Unwrap() error { return this.cause }   SURVIVED
  ```

  `refuse` finds the `*failure` before it asks anything else, so nothing at the door changes —
  which is precisely why the property is defence in depth and why only a direct assertion can
  see it.
- **Why this severity:** `Failure` is exported and is the whole store-to-kernel error channel, so
  the value travels through decorators before the kernel sees it. With an `Unwrap`,
  `errors.Is(storeErr, context.Canceled)` becomes true for a classified unconfirmed commit, and
  `errs.AsFault(storeErr)` starts answering from the store's incidental cause — a decorator that
  branches on cancellation first would abandon a write nobody confirmed. It is a second lock on
  §INV-029 and §INV-030 rather than the only one, which is what keeps it below `[high]`.
- **Why this timing:** no observable at any door changes today, and the assertion is one line in a
  test that already exists. It belongs with S3, where a real store first builds `Failure` values a
  decorator forwards.
- **Close criteria:**
  - [ ] a subtest asserts `Failure(<each outcome>, cause)` answers `errors.Is(·, cause)` false and
        `errs.AsFault(·)` false, for a cause carrying a fault and for a cause that is
        `context.Canceled`, with the refusal built from it as the control that the cause is still
        reachable through `CauseOf`
  - [ ] M-G3 turns the suite red
- **Status:** open

### GAP-T18 [medium][deferred] `tooLarge`'s fault message is asserted with two order-blind `Contains` calls, so the bound and the count can be swapped

- **Where:** `event/refusal_test.go:389`; the code is `event/errors.go:167-171`
- **What:**

  ```
  M-I10  Message(fmt.Sprintf("%s is %d bytes against a bound of %d", rule, actual, bound))
         ->                                                          rule, bound, actual     SURVIVED
  ```

  The assertion is `strings.Contains(fault.Message, "1048576") || … "2097152"`, and both substrings
  are present in either order. The test is written so that it cannot distinguish the two arguments
  it exists to check.
- **Why this severity:** this message is what a client reads on a 413. Swapped, it tells a caller
  whose 2 MiB payload was refused that "the payload is 1048576 bytes against a bound of 2097152",
  i.e. that it was within the bound — so the caller retries the same payload forever. Wrong
  user-visible text, no contract impact on the kernel.
- **Why this timing:** module-internal message detail; S4 is where `tooLarge` acquires its callers
  and its rule strings, and the assertion belongs beside them.
- **Close criteria:**
  - [ ] the assertion becomes an equality against the full expected message, or two `Contains`
        calls over the ordered phrases (`"is 2097152 bytes"`, `"bound of 1048576"`)
  - [ ] M-I10 turns the suite red
- **Status:** open

### GAP-T19 [low][deferred] The six nil routes assert only that *something* refused, so three of them are proved by the comparability rule rather than by the nil rule

- **Where:** `event/backing_test.go:103-134` (`an identity that is nil by any route is refused
  where it is minted`), assertion at `:115`; the code is `event/backing.go:40-50`
- **What:**

  ```
  M-E5  case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
        ->   reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer      SURVIVED
  ```

  `NewBacking` runs `nilByAnyRoute` and *then* the comparability check, and a nil slice, a nil map
  and a nil func are all uncomparable — so they are refused either way and the assertion
  (`errors.Is(err, ErrWrongStore)`) cannot tell which rule fired. Only the nil interface, the nil
  pointer and the nil channel exercise `nilByAnyRoute` on their own. The suite already knows how to
  distinguish the two rules — `:145-149` asserts the two refusals do not share their words — and
  the nil-route table does not use it.
- **Why this severity:** the outcome a caller sees is identical, and `nilByAnyRoute`'s other
  caller (`findAs`) only ever asks about pointer types. Cosmetic today; it becomes real if the
  order of the two checks is ever reversed or a comparable nil-able kind is added.
- **Why this timing:** no contract impact, and the fix is one assertion in an existing table.
- **Close criteria:**
  - [ ] each nil route asserts the refusal's message is the **nil** one and not the comparability
        one, using the same distinction `:145-149` already draws
  - [ ] M-E5 turns the suite red
- **Status:** open

### Carried findings re-confirmed still open

Each was already graded `[deferred]` in round 1 and copied into the plan's `## Debt`. Re-measured
here, each still survives, so none of them was closed silently:

| Debt item | Mutation re-run in this round | Result |
|---|---|---|
| GAP-T7 | `MaxKeyBytes 2<<10 → 8` (M-C2), `MaxPayloadBytes 1<<20 → 1` (M-C3), `MaxBatchCount 1024 → 1` (M-C4), `MaxPageCount 4096 → 1` (M-C5), `MaxResidentBytes 64<<20 → 1` (M-C6) | all five SURVIVED. `MaxNameBytes 128 → 8` (M-C1) is now **caught**, incidentally — `rendering_test.go:208` renders the 16-byte family `accounts.account`, which pins `MaxNameBytes ≥ 16` from below |
| GAP-T8 | `Support.stated() → true` (M-H2) | SURVIVED; `stated` remains the one 0.0 %-covered function in `event/` |
| GAP-T10 | `causeHops 64 → 4096` (M-K4) | SURVIVED |
| GAP-T11 (`Log` relation) | `Store` spells `Log`'s four methods inline instead of embedding it (M-L4) | SURVIVED |

---

## Round 3 mutation log — 103 applied, 101 scored, 80 caught, 21 survived

`M-F5` is excluded (compile break, re-run as `M-F5b`); `M-B5` is excluded (semantically
equivalent rendering). `M-I5` is listed as caught because it is red on three of three `-race`
runs, which is what both checkpoints run; under a bare `go test` it is green.

**Survived (21).** `M-A1` sentinel arm `matches`→`sameError` · `M-C2`…`M-C6` five ceilings ·
`M-E5` `nilByAnyRoute` drops `reflect.Slice` · `M-G3` `failure` gains `Unwrap` ·
`M-H2` `stated()`→`true` · `M-H7` `ReadAll` answers a `Version` · `M-H8` `ReadStream` takes a
`Cursor` · `M-H9` `Transaction` answers a `Backing` · `M-H10` `Append` gains a `Stream`
argument · `M-H11` `Close` answers nothing · `M-H12` `Backing()` answers a `Capabilities` ·
`M-I10` `tooLarge` swaps actual and bound · `M-J1` `Supported` becomes `Support`'s zero value ·
`M-J2` `Conflict` becomes `Outcome`'s zero value · `M-K4` `causeHops` 64→4096 ·
`M-L2` an optional `Snapshotter` interface joins the package · `M-L4` `Store` stops embedding
`Log`.

**Caught (80), by the test that reported them.**

| Test | Mutations it turned red |
|---|---|
| `TestTheRefusalVocabularyIsAPartition` | `M-A2` `refusal.Is` drops the `inVocabulary` gate · `M-A16` `Refused` stops promoting its cause · `M-I6` `vocabulary()` drops `ErrCursor` · `M-I7` `ErrSealed` stops wrapping `ErrDeclaration` · `M-J7` `upcastRefusal` loses its cause |
| `TestADeclaredWrapIsReachableByErrorsAsAndACauseIsNot` | `M-A6` `matches` stops consulting `asksIs` · `M-A12` `retryable` ignores the fault kind · `M-A13` `backendRefusal` never marks retryable · `M-A17` `CauseOf` answers for anything · `M-I8` `ErrKey` drops its bad-request class · `M-I9` `ErrPayload` joins the request class · `M-I11` `tooLarge` carries no fault · `M-J8` `refuseTransaction` promotes its cause · `M-K6` `asksIs` answers for any node with an `Is` method · `M-K7` `findAs` stops consulting a foreign `As` |
| `TestAContextCauseNeverTravelsThroughARefusal` | `M-A3` `refusal.Is` drops the cancellation guard |
| `TestAnOutcomeOutsideTheVocabularyNormalises` | `M-A5` `refuse` drops the nil guard · `M-A14` the append door defaults to `ErrBackend` · `M-A15` `Conflict` maps to `ErrBackend` · `M-G1` `Failure` drops the normalisation · `M-G2` `failure.Error` renders its cause · `M-G4` the default arm renders empty · `M-G5` `Refused` and `Closed` share a phrase · `M-G6` `Refused` and `Closed` renumbered · `M-J3` `Failure` normalises to `Conflict` · `M-J4` the `Refused` arm is deleted · `M-J5` `BadCursor` maps to `ErrBackend` · `M-J6` `Closed` maps to `ErrUncertain` · `M-K1` the two doors are swapped · `M-K2` `refuse` never finds a classification · `M-K3` `walk` visits nothing |
| `TestAJoinedCauseCannotOutlastTheWalksBudget` | `M-A4` `causeAsWrap` always promotes · `M-A11` the budget is spent per path · `M-K5` `walkWithin` stops at the first branch of a join · `M-K8` `withinBudget` always answers whole |
| `TestANilOrLyingCauseNeverPanicsTheKernel` | `M-A7` `refusal.As` drops its recover · `M-A8` `sameError` drops its recover · `M-A9` `walk` drops its recover · `M-A10` `findAs` drops the nil guard |
| `TestComposeRendersTheFrozenKey` | `M-B1` separator `'/'`→`':'` · `M-B2` lower-case escape hex · `M-B3` `'%'` no longer escaped · `M-B4` `'/'` no longer escaped · `M-B6` control runes no longer escaped · `M-B7` `escapeByte` swaps the nibbles · `M-D1` `checkText` drops the empty rule · `M-D5` `checkText` always passes |
| `TestEveryRenderingNamesAClassAndNeverAValue` | `M-B8` `Stream.String` renders any family · `M-B9` always unnameable · `M-B10` the key cap replaces the name cap · `M-C1` `MaxNameBytes`→8 · `M-D2` `checkText` drops the cap rule · `M-D3` drops the control rule · `M-D4` drops the UTF-8 rule · `M-H1` `Support.String` merges two answers · `M-I1` `refusal.Error` returns one constant · `M-I2` `Backing.String` renders the authority phrase · `M-I3` `Backing.String` never says invalid · `M-I4` `Authority.String` renders the backing phrase · `M-L1` a refusal grows a `Version` accessor |
| `TestABackingAndAnAuthorityAreComparedAndNeverIdentical` | `M-E1` `NewBacking` drops the nil check · `M-E2` drops the comparability check · `M-E3` `Equal` uses `reflect.DeepEqual` · `M-E4` `valid()` always true · `M-E6` `nilByAnyRoute` only checks the interface · `M-E7` the two refusals share their words · `M-F1` `Authority.Same` drops the backing arm · `M-F2` `Valid` always true · `M-F3` `NewAuthority` accepts an invalid backing · `M-F4` drops the nil check · `M-F5b` skips the comparability check · `M-F6` an authority serialises |
| `TestTheSeamsValuesAreDefinedTypesWithTheDeclaredFields` | `M-H3` `Limits` loses `MaxKey` · `M-H4` `Capabilities` loses `SharedBacking` · `M-H5` `AppendRequest` gains `Retries` · `M-H13` `Record.Revision` becomes a string · `M-H14` `Envelope` loses `RecordedAt` · plus round 2's `M-83`…`M-86` re-run |
| `TestTheStoreSeamHasEightMethodsAndNoneMutatesOrQueries` | `M-H6` `Store` gains `DeleteStream` · `M-L3` `Transaction` renamed `BeginTransaction` |
| `TestTheKernelsValuesAnswerTheSameFromManyGoroutines` | `M-I5` `inVocabulary` memoises into a lazy package-level cache — `WARNING: DATA RACE`, red on 3 of 3 `-race` runs, green without `-race` |

**Tree state after the campaign.** `diff -r event <pre-campaign copy>` reports no difference;
`go test -race -count=1 ./event/` ok 1.036s, `go vet ./event/...` EXIT=0, `gofmt -l .` silent.
Two throwaway probe files (`event/zz_probe_test.go`, twice) were created to measure GAP-T13's
HTTP statuses and deleted; `git status --porcelain event/` shows only the untracked directory it
showed before this round.

## Round 3 verdict

**Four `[immediate]` findings — one `[critical]`, three `[high]` — and three `[deferred]`.**
The suite is honest, deterministic, fast (1.0 s under `-race`), and covers every UC and INV row
S1's `Covers` claims with at least one killing mutation; round 2's twenty-two closures all
reproduce. What blocks the gate is one shape repeated four times: **a property the code carries
on a value rather than in a branch, which no assertion reads**. The class wrap on a refusal's
sentinel (GAP-T13, and it is worth 409 vs 500 on the framework's primary write conflict), the
signature of each of the eight seam methods (GAP-T14), the numeric zero value of the two enums
whose zero value *is* the fail-safe (GAP-T15), and the emptiness of the package's exported
interface set (GAP-T16). All four are cheap — a table, a signature list, two equality assertions
and one set comparison — and all four are in the section that freezes the contract every later
section, and `eventpg` in another repository, will be measured against.
