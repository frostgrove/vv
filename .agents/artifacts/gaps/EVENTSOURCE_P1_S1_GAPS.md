# EVENTSOURCE_P1 — implementation S1 — GAPS

## Round 1 — econv-implementation-reviewer (clean context) — 2026-09-07

Reviewed against the **code**, not the plan's prose: `event/` at its current state —
9 files, **729 lines**, no `_test.go` files. Read in full: `.agents/artifacts/plans/EVENTSOURCE_P1_PLAN.md`
(§ Carried gaps C1–C10, § Microkernel classification, § Contracts for the nine S1 files, § S1,
§ Architecture metrics), `.agents/artifacts/usecases/EVENTSOURCE_P1_USECASES.md` §2.3, §D.14,
§INV-016/019/021/024/025/027/028/029/030/045, UC-045, UC-053, `CLAUDE.md`, and
`~/.claude/skills/econv/references/{microkernel,architecture,building-blocks,data-integrity,universality,readability,restrictions,gaps}.md`.
Every number and every transcript below was produced by running in this worktree, from a throwaway
`event/zz_review_probe_test.go` and a throwaway out-of-tree `zzreviewstore` package, **both deleted
after the run** (`ls event/` shows nine `.go` files, no tests).

**Clean checks, with the numbers.**

*Checkpoint, reproduced verbatim.* `go build ./... && go vet ./event/ && go test -race -count=1 ./event/`
→ `?   github.com/frostgrove/vv/event  [no test files]`, `EXIT=0`; `gofmt -l event` silent;
`go list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' ./event/` prints exactly
`utils, crud, errs, event` and the plan's four-package string comparison returns true. The pasted
S1 phase-4 transcript is **real**. `make check` is green on all nine arms (`check-deps`,
`check-tiers`, `check-utils`, `check-triplets`, `check-todo`, `check-replaces`, `check-tidy`,
`check-otel-schema`, `check-workspace`).

*Plan/status honesty.* S1 is `[~]` carrying `MISSING:` with seven named phase-5 tests, and the
phase-4 block is what is claimed green. No `[x]` on a phase-4 block. `event/` holds zero test
files, which is the split working, not a hidden hole.

*Architecture metrics, counted.* Longest file `event/errors.go` **262** lines (threshold 400);
next `store.go` 173, `identity.go` 75, `outcome.go` 61, `backing.go` 50, `authority.go` 46,
`text.go` 43, `bounds.go` 16, `doc.go` 3. **43 functions**, longest `walkWithin` at **23** lines,
maximum nesting depth **4** (`walkWithin`'s `for → switch → for → if`). Internal (`frostgrove/vv`)
imports per file: `errors.go` **2** (`crud`, `errs`), `backing.go` 1, `authority.go` 1, the other
six **0** — threshold 5, no breach. Exported top-level symbols: identity 6, bounds 6, text 0,
backing 2, authority 2, outcome 9, errors 25, store 11 = **61**, plus 9 methods; the count and its
justification match the plan's per-file table row for row. **0 import cycles**; **0** packages in
the tree import `event` (`grep -rn '"github.com/frostgrove/vv/event' --include=*.go . | grep -v '^./event/'`
→ no hits), so the stated blast radius is exact. **Fakes needed to unit-test any object in S1: 0** —
every value is inert or a pure function.

*Microkernel purity, with the command and the result.*
`grep -rniE "eventmemory|eventpg|eventtest|postgres|pgx|sqlite|mysql|redis|memory" event/` → **no
hits, exit 1**. There is exactly one `.(type)` switch in the section (`errors.go:218`) and it is
over the two standard `Unwrap` interfaces, not over a store. No registry, no `if name ==`, no
`init()`, no import side effect. **The zero-diff test was executed, not asserted:** I wrote an
out-of-package `zzreviewstore` implementing all eight `Store` methods, constructing `Backing`,
`Authority`, `Envelope`, `Cursor`, `Version`, `Position`, `Limits`, `Capabilities` and returning
`event.Failure` of **all seven** `Outcome` values; `go build` and `go vet` both clean, with **zero**
`event/` diffs. Phase 2's `eventpg` needs no file under `event/` to change.

*Purity of the section.* `grep -rnE '\btime\.Now|rand\.|os\.(Getenv|Open|Read|Write)|log\.|fmt\.Print|http\.|net\.|sync\.|go func' event/`
→ **no hits**. `panic(` **0**; `recover(` **2**, both inside the defensive error walk. Package-level
state: **25** `var`s, all `errors.New`/`fmt.Errorf` sentinels declared once and never reassigned
(no assignment to any of them outside its declaration block). No clock, no randomness, no I/O, no
telemetry, no env read — the S1 "Degradation: none" claim holds by construction.

*Sentinel partition, measured.* 24 sentinels. A pairwise `errors.Is` sweep over all 24 × 23 = **552
ordered pairs of the bare sentinels** found exactly **two** edges — `ErrSealed → ErrDeclaration`
and `ErrCodecType → ErrDeclaration` — which is §2.3's closed intra-class list, unchanged. (The
partition does *not* survive one refusal shape; that is GAP-147.) `ErrCursor` sits under `wiring`
and `ErrTooLarge` under `request`, both as C5 decided; `ErrRefused` and `ErrSample` are present.

*The `Outcome` map, driven at both doors.* `Failure(Outcome(200), …)` normalises to `Unclassified`
in the constructor (`f.outcome == 0` measured) and takes the fail-safe default —
`ErrUncertain` from the append door, `ErrBackend` from the read door. A `%w`-wrapped
`Failure(Conflict, …)` is still found (`errors.As`, not a type assertion) and maps to `ErrConflict`
→ `crud.ErrConflict`. `Failure(NotWritten, …)` whose cause carries `crud.ErrUnavailable` answers
`errors.Is(err, crud.ErrUnavailable) == true`; `Failure(Unconfirmed, …)` with the identical cause
answers **false** — GAP-135 is closed exactly as C3/C5 specify, and I could not find a second door
into it. `Outcome.String()` renders `[outcome unclassified]` for 0, 7, 200 and 255 and never a
number.

*`errs` traversal.* `tooLarge("the payload", 2<<20, 1<<20)` → `errors.As(err, &*errs.Fault)` **true**,
`errs.AsFault(err)` **true**, `Kind == too_large`, message `"the payload is 2097152 bytes against a
bound of 1048576"` — no payload, no key. So C5's 413 path is live, and `errors.Is(err, ErrTooLarge)`
is still true.

*`Compose`.* 18 part lists exercised — empty, `/`, `%`, NUL, `0x7F`, a C1 rune, invalid UTF-8, a
literal U+FFFD, Cyrillic, `%2F`, interior empties. **Every output passed the kernel's own
`checkText`**, and there is exactly **one** collision in the table: `Compose()` and `Compose("")`
both render `""`, which is the pair the contract names and both doors refuse as empty. `%` escapes
before `/` so `"%2F"` → `"%252F"` and `"/"` → `"%2F"` stay distinct; uppercase-only hex means no
second spelling of an escape. Injective by construction, as claimed.

*`Backing`/`Authority`.* `Backing{}.Equal(Backing{})` is **false** (a zero backing matches nothing,
including itself); `Authority{}.Same(Authority{})` **false**; `Authority{}.Valid()` **false**.
`NewBacking` refuses `nil`, a typed-nil pointer, a slice and a struct containing a slice, each with
`ErrWrongStore` and a message naming the rule and never the identity. `MarshalJSON` refuses.
`NewAuthority(b, tx)` twice over one identity compares `Same` — §INV-028's savepoint claim, assertable
today.

---

### GAP-147 [high][immediate] `Failure(Refused, cause)` promotes the store's cause to a **declared wrap**, so a decorator whose cause is one of this vocabulary's own sentinels breaks §INV-024's partition — reproduced, and nothing in the kernel, the plan's defect inventory or the suite checks it

- **Where:** `event/errors.go:166` (`case Refused: return newRefusal(ErrRefused, this.cause, this.cause)`),
  `event/errors.go:82-87` (`refusal.Is` reaches `wrapped`), `event/outcome.go:49-54` (`Failure`
  accepts any `cause` and normalises only the `Outcome`).
- **What:** [SPEC] §2.3 states the one obligation a cause carries — *"a store's own error must not
  itself be a sentinel of this vocabulary. If it were, `errors.Is` from a refusal of one class could
  reach a sentinel of another through the cause, and §INV-024's partition would be false through a
  back door … this is a **checkable prohibition**"* — and §INV-024's *Falsified by* asks for "a store
  returning one of this vocabulary's sentinels as its own cause asserted to fail the `refusal
  classes` section". Round 4's GAP-135 closed that back door for every sentinel by stopping `Is` at
  `sentinel` and `wrapped` — **except `ErrRefused`, which C2 defines as the one row whose cause *is*
  its wrap.** Nobody checks the prohibition: not `Failure`, not `refuse`, and the plan's C8
  eleven-defect inventory and its `refusal classes` test description (PLAN.md:148-151) carry only
  the `ErrRefused`/`ErrUncertain` pair, not this case. Measured:

  ```
  refuseAppend(Failure(Refused, ErrConflict))     → Is(ErrRefused)=true  Is(ErrConflict)=true  Is(crud.ErrConflict)=true
  refuseAppend(Failure(Refused, ErrUncertain))    → Is(ErrRefused)=true  Is(ErrUncertain)=true
  refuseAppend(Failure(Refused, ErrDeclaration))  → Is(ErrDeclaration)=true
  ```

- **Why this severity:** `high`. Concrete: §UC-048's quota decorator — the actor C2 exists for —
  writes the natural thing, `return event.Failure(event.Refused, fmt.Errorf("tenant over quota: %w", event.ErrConflict))`,
  because it wants a 409 and C2 tells it to state its status in the cause. §UC-022's caller loop is
  `for { at, s := repo.Load(...); ...; if err := repo.Append(...); errors.Is(err, event.ErrConflict) { continue } }`.
  That loop now spins on a policy refusal that will never clear, holding the caller's transaction
  open, forever — a livelock with no error surfaced. Wrapping `event.ErrUncertain` instead is the
  mirror: a store-class refusal where **nothing was written** makes a caller take the
  "reconcile a possibly-duplicated append" branch, which under [SPEC] non-goal 35 (no idempotency
  key) means a human reads a history to decide whether a fact exists that never did. The same
  channel also lets an `*errs.Fault{KindNotFound}` picked up incidentally from a nested driver stack
  set a policy refusal's HTTP status to 404 — which is the exact hazard PLAN.md:809-811 argues is
  unacceptable for every *other* sentinel and then exempts for this one. The kernel is the only
  party that can enforce a partition it is the one to promise.
- **Why this timing:** the enum value, the sentinel and the map are S1's whole deliverable and are
  frozen at S6's `make api` baseline; §INV-024 is in S1's own `Covers` list. Closing it later means
  either changing what `ErrRefused` answers (breaking, by §2.3's own growth rule) or discovering it
  in phase 2 through `eventpg`'s decorators.
- **Close criteria:**
  - [ ] A cause that `errors.Is`-matches **any** of the 24 sentinels is refused or stripped where a
        refusal is built, so `errors.Is` from a refusal of one class cannot reach a sentinel of
        another for any `Outcome` including `Refused`; the answer chosen (refuse the `Failure` at
        construction, drop the wrap, or map to `Unclassified`) is written on `event/errors.go` with
        its argument.
  - [ ] A test in `event/` — the S1-named `TestTheRefusalVocabularyIsAPartition` is the natural home —
        runs the 24 × 23 pairwise ground-truth table **over refusals built from a store's cause**, not
        only over the bare sentinels, with `Failure(Refused, <each of the 24>)` in the table, and it
        fails before the fix.
  - [ ] The plan's C8 defect inventory and the `refusal classes` section gain the defect §INV-024's
        *Falsified by* already names: a store returning one of this vocabulary's sentinels as its own
        cause, asserted to fail that section.
  - [ ] `event/errors.go`'s traversal comment (`:56-73`) states what happens to a cause that is a
        sentinel of this vocabulary, so the next reader does not have to derive it.
- **Status:** **closed** — round 5. The answer chosen is **drop the wrap**, not refuse the
  `Failure` and not map to `Unclassified`: the decorator's classification is still true (nothing was
  written, and it refused rather than tried), so changing the outcome would lie about the store, and
  refusing at construction would give `Failure` a second return value on the one path a store must
  not have to error-check. `event/errors.go` now holds the twenty-four sentinels in an unexported
  `vocabulary()` — a function, not a package-level slice, so §INV-013 stays literal — and
  `causeAsWrap(cause)` returns the cause only when `errors.Is` from it reaches none of
  them; both rows whose wrap is a cause read it — `ErrRefused` (C2) **and `ErrUpcast`**, which the
  finding did not name and which carries an application's error through the identical channel. A
  non-promoted cause stays readable through `CauseOf`, so nothing an operator's log line wants is
  lost. `crud.ErrForbidden`, `crud.ErrConflict`, `crud.ErrUnavailable` and every `*errs.Fault` are
  foreign to this vocabulary and still arrive, so C2's mechanism is intact — asserted as a control.
  The argument is written on `causeAsWrap` and referenced from the `refusal` type comment.
  Reproduced first: with the promotion in place a probe printed 41 cross-class edges, headed by
  `a [outcome refused] refusal over cause event: the declaration is malformed also matches event:
  the declaration is malformed`; with it removed the same probe is green. PLAN.md carries the
  decision in `#### event/errors.go` (the `wrapped` table's `ErrRefused` row, the `Outcome` map row,
  and a paragraph with the §UC-022 livelock and the §UC-048 mirror), the C8 inventory grows to
  **twelve** with the decorator-returns-a-sentinel defect against `refusal classes`, and C2's
  `refusal classes` test gains its third case. `TestTheRefusalVocabularyIsAPartition` is respecified
  to run the 24 × 23 table **over refusals built from a store's cause** at both doors.

---

### GAP-148 [medium][immediate] A store's cancellation error is returned **verbatim**, so its message text — which will name a key, a version or a cursor — is what the caller and the log line get, falsifying §INV-025

- **Where:** `event/errors.go:147-149` (`if matches(err, context.Canceled) || matches(err, context.DeadlineExceeded) { return err }`);
  second site `event/outcome.go:56-61` (`failure.Error()` concatenates the cause's text).
- **What:** [SPEC] §INV-025: *"No error this subsystem produces carries a stream key, a payload or a
  fragment of one, a version, a position, a cursor, an identity value, a driver's message text or a
  codec's message text"*, falsified by *"an injected driver failure whose distinctive text must not
  appear in the rendered message"*. PLAN.md:964-965 states the store's side as *"a store refuses for
  cancellation with the **bare** `context.Canceled` or `context.DeadlineExceeded`"* — but the kernel
  does not require bare; it matches anything that `errors.Is`-es to a context sentinel and returns
  **that value**, wrapper and all. Measured:

  ```
  refuseAppend(fmt.Errorf("store: %w", context.DeadlineExceeded)).Error()
    → "store: context deadline exceeded"
  Failure(NotWritten, errors.New(`pq: password authentication failed for user "admin"`)).Error()
    → `event: the store reported [outcome not written]: pq: password authentication failed for user "admin"`
  ```

- **Why this severity:** `medium`. `porthttp` never renders `err.Error()` (grep for `.Error()` under
  `port/porthttp/` and `port/` returns nothing), so this is not a wire leak — it is a **log** leak,
  which is `restrictions.md`'s "secrets or whole documents in logs". The triggering store is the
  ordinary one, not a hostile one: `return fmt.Errorf("read %s at v%d: %w", s.Key, after, ctx.Err())`
  is what a competent implementer writes, and every cancelled read then puts the stream key and the
  version into the caller's error string and the operator's log — the two values §INV-025 names first.
  The second site is smaller but the same channel: `event.Failure` is an exported constructor and a
  decorator that logs the value it is forwarding prints the driver's text, DSN credentials included,
  as above. Not `high` because both channels stop at a log line and the kernel's own refusals are
  clean.
- **Why this timing:** the pass-through is three lines in the one function the whole store→kernel
  channel goes through, and it is frozen at S6. §INV-025 is in S1's `Covers` list, and the S1 test
  that would catch it (`TestEveryRenderingNamesAClassAndNeverAValue`) is described as running "over
  every `String()` in the section" — which does not reach a forwarded store error at all.
- **Close criteria:**
  - [ ] The cancellation window returns the **bare** sentinel (`context.Canceled` /
        `context.DeadlineExceeded`) rather than the store's value, or `event/errors.go` states in
        writing why a store's text is permitted to travel here and §INV-025 is amended to carry the
        exception.
  - [ ] `TestEveryRenderingNamesAClassAndNeverAValue` (or `TestAContextCauseNeverTravelsThroughARefusal`)
        drives a store that wraps `ctx.Err()` with a message containing a key, a version and a cursor,
        and asserts none of the three appears in the returned error's `Error()`, with the bare-sentinel
        control asserting `errors.Is` still matches.
  - [ ] `failure.Error()`'s inclusion of the cause text is either removed or justified on
        `event/outcome.go` as a store-implementer-only value that no kernel path renders.
- **Status:** **closed** — round 5, both channels, both by removal rather than by an exception.
  `refuse` now answers the **bare** `context.Canceled` or `context.DeadlineExceeded` rather than the
  store's value, in two separate windows so a chain carrying one is not answered with the other.
  §INV-025 needs no amendment: the store contract already said *bare* (PLAN.md's `event/store.go`
  clause), and the kernel enforcing what the contract asks for is cheaper than trusting every store
  to. `failure.Error()` renders the outcome and nothing of the cause, for the reason the finding
  gives — `Failure` is an exported constructor, so a decorator that logs the value it is forwarding
  is the live channel, and a driver's text is where the DSN credentials are. The store keeps its own
  cause; it built it. Reproduced first: `refuseRead(fmt.Errorf("read orders/A-17 at v9: %w",
  context.DeadlineExceeded)).Error()` was `"read orders/A-17 at v9: context deadline exceeded"` and
  is now `"context deadline exceeded"` with `errors.Is` unchanged; `Failure(NotWritten,
  errors.New("pq: password authentication failed for user \"admin\"")).Error()` was the whole string
  and is now `"event: the store reported [outcome not written]"`. PLAN.md's `Outcome` map row for
  cancellation and the `#### event/outcome.go` block both state it, and the assertions are
  respecified onto `TestAContextCauseNeverTravelsThroughARefusal` and
  `TestEveryRenderingNamesAClassAndNeverAValue`.

---

### GAP-149 [medium][immediate] The hop budget bounds the walk's **depth**, not its work: a branching `errors.Join` chain from a store, a decorator or an upcaster hangs the request goroutine inside the kernel — measured 2.68 s at depth 26, and it doubles per level

- **Where:** `event/errors.go:197` (`const causeHops = 64`), `:212-233` (`walkWithin`), specifically
  `:223` — `walkWithin(child, visit, budget)` passes the budget **by value** into every sibling
  branch.
- **What:** the comment at `:199-202` claims the defence: *"The kernel reads errors it did not
  build — a store's cause, a decorator's refusal, an application's upcast error — so the walk carries
  a hop budget and a recover: a chain that loops and a matcher that panics are that party's defect
  and must not become the kernel's."* Loops and panics are indeed bounded. Branching is not: with
  `Unwrap() []error`, each child recurses with the *same* remaining budget, so the visited-node count
  is `b^depth`, not `budget`. `cache/errors.go:86-125` — the shape PLAN.md:769-773 names as the model
  — decrements a **shared `*int`** (`boundedErrorIs(err, target, budget *int)`, `*budget--`) and does
  bound total work. That difference was lost in the port. Measured with a balanced binary
  `Unwrap() []error` tree:

  ```
  depth 26, target absent → matches() returned false after 2.68 s
  ```

  At depth 40 that is ~2^14 × 2.68 s ≈ 12 hours on one request goroutine.
- **Why this severity:** `medium`. `matches` and `faultIn` run on **every** classified failure at
  every door — `refuse`, `retryable`, `backendRefusal`, `refusal.Is` — so the walk is on the hot
  path of every store error. The producing input is not exotic: a store that fans an append across
  shards and joins per-shard errors, each of which joined per-row errors, is a two-level tree; a
  library that does it recursively is not. Nothing bounds the depth a store may hand over, and the
  budget exists precisely because the kernel does not trust that party. Not `high` because a
  *balanced* deep join tree is uncommon (the frequent left-deep `err = errors.Join(err, e)` loop
  stays linear, verified by inspection), and the failure is a hang rather than a wrong answer.
- **Why this timing:** one-line fix (`budget *int`, decrement through the pointer, exactly
  `cache/errors.go`'s shape) in a file that S6 freezes, and the comment currently asserts a property
  the code does not have.
- **Close criteria:**
  - [ ] `walk`/`walkWithin` bound the **total** number of visited nodes, not the depth — a shared
        counter across branches, matching `cache/errors.go:96-125`.
  - [ ] A test builds a balanced `Unwrap() []error` tree deeper than the budget and asserts the walk
        returns within a bound, with the linear left-deep chain as its control.
  - [ ] `event/errors.go:199-202`'s comment says which quantity is bounded.
- **Status:** **closed** — round 5. `walk` allocates the budget and `walkWithin` decrements it
  through a `*int`, so the counter is shared across every branch — `cache/errors.go:96-125`'s own
  shape, which is what the port lost. The comment now says the budget bounds the number of errors
  visited and not the depth reached, and names the producing input (a store that joins per-shard
  failures of per-row failures). Reproduced first, and counted rather than timed, because a timing
  assertion is what would make the phase-5 test flaky: a balanced `Unwrap() []error` tree of depth
  18 cost **524 287 visits against a budget of 64** before the fix and 64 after, with the left-deep
  chain's 11 visits unchanged as the control. The eighth S1 test,
  `TestAJoinedCauseCannotOutlastTheWalksBudget`, is added to the `MISSING:` list and to the phase-5
  checkpoint, whose count clause moves from 7 to 8.

---

### GAP-150 [medium][immediate] `Stream.String()` — the one rendering §INV-025 names — prints a store-supplied `Family` verbatim, so newlines and control bytes from an `Envelope` reach the log line the refusal was written to be safe in

- **Where:** `event/identity.go:21` (`func (this Stream) String() string { return "[stream " + this.Family + "]" }`);
  the value that reaches it from outside is `Envelope.Stream` (`event/store.go:78-79`), which a store
  constructs.
- **What:** the kernel has the predicate (`event/text.go:21-39` `checkText`, and `MaxNameBytes`) and
  does not apply it here. PLAN.md:1056-1060 is explicit that a *type name* from a store travels in a
  refusal "only when it passed that rule … a refusal over a type name that failed the rule names the
  stream and the byte count and nothing else" — so the stream **is** what a refusal falls back to,
  and it is unchecked. Measured:

  ```
  Stream{Family: "orders\n\tFAKE LOG LINE: admin logged in"}.String()
    → "[stream orders\n\tFAKE LOG LINE: admin logged in]"
  checkText(that family, MaxNameBytes) → "contains a control character"
  ```

- **Why this severity:** `medium`. For a kernel-minted stream the family is a declared identifier and
  is safe; the reachable path is C4's own verification — the foreign-stream page and the mis-ordered
  page (S4) refuse over an envelope a *store* built, and `Envelope.Stream.Family` is a store's data
  under the trust boundary PLAN.md:1045-1049 states ("the kernel trusts a store's bytes and strings
  exactly as far as it trusts its numbers, which is not at all"). A restored dump, a shared database
  another service writes, or a store with its own grants can therefore inject a forged line into an
  operator's log through the framework's own refusal. A 1 MiB family name is the same door with the
  volume knob. Not `high` because the rendering site in a refusal is S4's, so nothing surfaces it yet.
- **Why this timing:** the mechanism is here and it is one guard; the consumers land in S4 and S5,
  and adding the guard after they are written means revisiting each refusal site. `Stream.String` is
  also exported and frozen at S6.
- **Close criteria:**
  - [ ] `Stream.String()` renders a classification when the family fails the kernel's own text rule
        (a bracketed "[stream unnameable]" or the byte count), and never emits a control byte or more
        than `MaxNameBytes`.
  - [ ] A test asserts `Stream{Family: <newline>}.String()` and `Stream{Family: <MaxNameBytes+1 bytes>}.String()`
        carry neither, with a legal family as the control — the natural home is
        `TestEveryRenderingNamesAClassAndNeverAValue`.
  - [ ] `event/store.go`'s trust-boundary paragraph names `Envelope.Stream.Family` alongside
        `Envelope.Type` as store data that is checked before it is used for anything, including a
        rendering.
- **Status:** **closed** — round 5. `Stream.String` reads `checkText(this.Family, MaxNameBytes)` and
  answers `"[stream unnameable]"` when it fails, which is the classification form the section's
  other renderings already use (`[event backing invalid]`, `[outcome unclassified]`,
  `[support unstated]`) rather than a byte count, so no store number travels either.
  `event/store.go`'s `Envelope` comment names `Stream.Family` beside the type name as data checked
  before any use, a rendering included, and PLAN.md says the same in the trust-boundary list and in
  `#### event/identity.go`. Reproduced first:
  `Stream{Family: "orders\n\tFAKE LOG LINE: admin logged in"}.String()` returned the forged line
  verbatim and a `MaxNameBytes+1` family rendered 138 bytes; both are now the classification, with
  `"[stream accounts.account]"` unchanged as the control. The assertions land on
  `TestEveryRenderingNamesAClassAndNeverAValue`.

---

### GAP-151 [medium][immediate] The store contract drops the three `AppendRequest` clauses [SPEC] §D.14 writes, including the one that says `Expected: 0` means "never written" and **never** "any"

- **Where:** `event/store.go:63-71`. `Stream` and `Expected` carry no clause at all; only `Records`
  does.
- **What:** [SPEC] §D.14:4973-4980 declares three, verbatim on the fields:
  `Stream Stream // already validated by the kernel`,
  `Expected Version // the token's version; 0 means "never written", never "any"`,
  `Records []Record // already encoded, already inside every bound`. The plan's own contract block
  (PLAN.md:875-879) reproduces `AppendRequest` without them, and the code follows the plan. This is a
  drift from [SPEC] in the one file whose whole purpose is to be read by an implementer in another
  package — §D.14's opening is *"This is A5's whole surface. If it is not here, a store neither
  implements it nor may rely on it."*
- **Why this severity:** `medium`. `Expected` is the field the omission matters for. A store author
  reading a bare `Expected Version` writes the C-idiomatic guard
  `if req.Expected != 0 && current != req.Expected { conflict }` — treating the zero value as "no
  expectation" is the single most natural reading of an unsigned zero in Go, and it is what
  `AppendRequest{Stream: s, Records: r}` produces by default for anyone constructing one. The result
  is that **two concurrent creations of the same fresh aggregate both succeed**: a silent lost update
  at exactly the point §UC-022's optimistic concurrency is supposed to bite, on the one version where
  the conflict is invisible in a re-read afterwards. The missing `Records` clause is smaller (the
  bounds statement is duplicated in the `Limits` comment) and `Stream`'s is informational.
  Not `high` because `eventtest`'s `expected version` section should catch it for any store that runs
  the suite — but a decorator, a vendored store and phase 2's first draft are all outside that.
- **Why this timing:** it is the seam's own documentation, frozen at S6, and it is cheaper to write
  now than to discover from a store that read it wrong. It is also a plan-vs-spec drift that should
  be recorded in one direction or the other before the contract freezes.
- **Close criteria:**
  - [ ] `AppendRequest.Expected` carries [SPEC] §D.14's clause verbatim or in the file's own words:
        the token's version, `0` means "never appended to", never "any".
  - [ ] `AppendRequest.Stream` and `AppendRequest.Records` carry §D.14's remaining two clauses, or
        the plan records in `## Disagreements` why they were dropped.
  - [ ] `eventtest`'s `expected version` section asserts the **version-0** case explicitly — two
        appends at `Expected: 0` against one fresh stream, the second refused `ErrConflict` — so the
        misreading fails a section rather than passing one.
- **Status:** **closed** — round 5. All three clauses are on the fields in `event/store.go`, in the
  file's own words rather than verbatim, with `Expected`'s carrying the consequence the finding
  names: zero means the stream has never been appended to and never *at any version*, because two
  creations of one fresh aggregate both carry zero and exactly one may win — the one conflict a
  re-read afterwards cannot see. `Records` keeps its ownership clause and gains §D.14's
  already-encoded-and-inside-every-bound half. PLAN.md's `AppendRequest` code block carries the three
  as trailing comments, matching §D.14 line for line, plus the paragraph explaining why `Expected` is
  the clause a store gets wrong from the field alone (`if req.Expected != 0 && …` is the idiomatic
  Go guard and `AppendRequest{Stream: s, Records: r}` produces the zero by default). The
  `#### The section inventory` block now states the `expected version` version-zero case.

---

### GAP-152 [medium][deferred] `NewAuthority` admits a **value**-typed transaction identity, so two live transactions can compare `Same` and §UC-030's checkable atomicity claim is silently false

- **Where:** `event/authority.go:23-34` (`NewAuthority(over Backing, transaction any)`; the only
  checks are `over.valid()`, nil-by-any-route, and `Comparable()`).
- **What:** §INV-028 states the derivation as *"the transaction's **own identity, by reference**"*
  and claims the wrong shape is structurally excluded: *"a store that 'mints fresh entropy per call'
  is **not a shape this contract admits at all**, rather than one it fails a test for."* The contract
  as written admits far more than a reference. `NewAuthority(b, 7)` compiles; a store that identifies
  its transactions by a per-connection counter, a sequence number, a UUID **string**, or a small
  value struct passes every check, and two distinct live transactions that happen to carry equal
  values compare `Same`. §INV-028's own "why holding the identity is safe" argument — *"a live
  `Authority` keeps its transaction reachable, so its address cannot be reused while the authority
  exists"* — is an argument about a **reference** and does not hold for a value.
- **Why this severity:** `medium`. §UC-030's whole point is that "two subsystems wrote in one
  transaction" is a claim a caller can **check**, by comparing two authorities. Under a value
  identity the check answers `true` when the claim is false, and there is no second signal: the
  append succeeded, the receipt is well formed, and the two writes are in two transactions. That is
  an unobservable integrity failure, which is the worst shape in `data-integrity.md`. It is `medium`
  and not `high` because the store must choose a value identity to reach it, and `eventtest`'s
  `transactions` section is specified (§INV-028's *Falsified by*, the `Begin`-twice case) to assert
  two live transactions are **not** `Same`, so any store that runs the suite with
  `Transactions: Supported` is caught.
- **Why this timing:** `deferred` — the declared remedy is S5's `transactions` section, which exists
  in the plan and lands two sections from here. The kernel-side half (refusing a non-reference
  identity, or stating the obligation on the constructor) is small and can ride with S5 or S6.
- **Close criteria:**
  - [ ] `event/authority.go` states the obligation where an implementer reads it: the identity must
        be the transaction's own reference, not a value derived from it, and why (address reuse is
        prevented by the retention, and only by it).
  - [ ] Either `NewAuthority` refuses a non-reference kind, or the plan records the decision not to
        with the argument (a value struct naming a connection and a sequence is a legitimate shape,
        so a blanket refusal is a real cost).
  - [ ] `eventtest`'s `transactions` section's `Begin`-twice case is written and is listed in the
        defect inventory, so the suite half is not a promise.
- **Status:** **deferred, carried into PLAN.md `## Debt`** — round 5, and the finding's own
  `[deferred]` timing is accepted. Criterion 2 is answered now rather than left open: `NewAuthority`
  does **not** refuse a non-reference kind, because a value struct naming a connection and a
  sequence is a legitimate identity for a store whose transactions are not pointers, and a blanket
  refusal would exclude it for a hazard the suite already catches. That decision and its cost are
  written at PLAN.md `#### event/authority.go`. Criteria 1 and 3 — the obligation stated on the
  constructor where an implementer reads it, and the `Begin`-twice case written into the
  `transactions` section — ride with S5 or S6 and are the Debt row.

---

### GAP-153 [low][immediate] Plan/code drift: `NewBacking` and `NewAuthority` refuse a non-comparable identity, and the plan's contract rows do not declare that refusal

- **Where:** `event/backing.go:16-18`, `event/authority.go:30-32`; PLAN.md:645-680
  (`#### event/backing.go`) and PLAN.md:681-703 (`#### event/authority.go`), which name only the
  nil-by-any-route predicate.
- **What:** the code is **right** and the plan is the one that drifted — [SPEC] §D.14:5030-5034 and
  §INV-028's *Falsified by* both require it (*"`NewAuthority` asserted to refuse a non-comparable
  identity and one that is nil by any route"*). The plan's § Contracts section says "someone must be
  able to disagree with the API from this document alone", and a second refusal reason with its own
  message is API.
- **Why this severity:** `low`. No behavioural defect: without the check `crud.SameDataSource`
  already answers `false` for an uncomparable pair, so the failure mode would be a store whose
  backing never matches itself — refused at the first door with `ErrWrongStore` instead of at
  construction. Refusing early is strictly better. The cost is a plan that no longer describes the
  code it produced.
- **Why this timing:** `immediate` because the plan is the artifact the next section is written
  against and the fix is two rows.
- **Close criteria:**
  - [ ] PLAN.md's `#### event/backing.go` and `#### event/authority.go` contract blocks state the
        comparability refusal and its sentinel.
  - [ ] The S1 phase-5 test `TestABackingAndAnAuthorityAreComparedAndNeverIdentical` includes an
        uncomparable identity for both constructors, with a comparable one as its control.
- **Status:** **closed** — round 5, in the plan, since the finding is that the code was right.
  PLAN.md's `#### event/backing.go` and `#### event/authority.go` blocks now state the comparability
  refusal, its sentinel (`ErrWrongStore`, wiring class, its own message) and the finding's own
  argument for why refusing at construction beats the alternative it replaces — a backing that never
  matches itself, refused a door later with an error the store cannot act on. The uncomparable case
  for both constructors, with a comparable control, is written into the phase-5 description of
  `TestABackingAndAnAuthorityAreComparedAndNeverIdentical`.

---

### GAP-154 [low][deferred] `event/` is 24 % comment against 0.5 % in `jobs` and 0.6 % in `cache`, and several of the comments restate the code they sit on

- **Where:** measured across the section — `event/store.go` **85/173 = 49 %**, `errors.go` 48/262,
  `outcome.go` 10/61, `identity.go` 9/75, `bounds.go` 6/16, `text.go` 6/43; **176/729 = 24 %** total.
  Tree comparison: `jobs` 170/35 963 = 0 %, `cache` 113/18 367 = 0 %, `storage` 33/2 173 = 1 %,
  `tenancy` 262/2 882 = 9 %.
- **What:** `CLAUDE.md` — *"Comments are exceptional. Write one only for a genuinely complex function
  of roughly 40+ lines … Short and ordinary code must explain itself through names and structure …
  do not restate code. Removing a comment that fails this rule is expected cleanup."* Most of the
  volume **earns its keep** and I am not asking for it to go: the ownership obligations on
  `Record.Payload` and `Envelope.Payload`, the three-answer `Transaction` contract, the read-shape
  clauses, the two-traversal invariant on `refusal`, and the `nilByAnyRoute` rationale are all
  invariants the code cannot make visible, and [SPEC] §D.14 writes several of them verbatim. Two are
  not: `event/text.go:15-20` restates an 18-line function's four guards in the same order
  ("non-empty, valid UTF-8, no NUL and no other control character, within its cap") before saying the
  two things that are not obvious (no normalisation; a closed phrase set), and
  `event/identity.go:29-37`'s middle sentence ("Each part is escaped and the escaped parts are joined
  with a separator") restates a ten-line function around the freeze clause that does earn its place.
- **Why this severity:** `low`. No behavioural consequence; the risk is the ordinary one — a comment
  that restates code is the first thing to go stale, and the density sets the norm for S2–S5, which
  are five times this size.
- **Why this timing:** `deferred` — cleanup, and it is cheapest as one pass at S6 when the
  contract text and `docs/modules/en/event.md` are written together and the duplication between them
  becomes visible.
- **Close criteria:**
  - [ ] The restating halves of `event/text.go:15-20` and `event/identity.go:29-37` are removed,
        keeping the clauses that state what the code cannot (no normalisation, the closed phrase set,
        the freeze).
  - [ ] S2–S5 do not exceed the density of the clauses [SPEC] §D.14 and the ownership invariants
        actually require; the plan states which comments are contract text and are therefore expected.
- **Status:** **deferred, carried into PLAN.md `## Debt`** — round 5, at the finding's own timing.
  The two restating halves are named there by file so the S6 pass has a list rather than a
  percentage. Round 5's own closures added contract text to four files — the `AppendRequest`
  clauses, `Stream.String`'s trust note, `causeAsWrap`'s argument and the walk's bounded quantity —
  each of which is an invariant the code cannot make visible and none of which restates code; the
  section is now 792 lines. That the number moved up before the cleanup pass is expected and is
  the reason the row says *which comments*, not *how many*.

---

### GAP-155 [low][deferred] The six kernel ceilings carry no derivation anywhere a reader of this repository is told to look — the table exists only in a planning artifact under `.agents/`

- **Where:** `event/bounds.go:9-16`. Six exported constants, one trailing half-line each
  (`// the ceiling on Limits().MaxPayload`), one six-line package comment that explains the *shape*
  of the enforcement and no number's origin.
- **What:** PLAN.md:565-575 carries the full §D.13-form table — derivation, what it protects, what a
  deployment does when the number is wrong — and PLAN.md:566 says "Each carries its derivation … in
  §D.13's form". It is not in the tree: `docs/` has no `modules/en/event.md`, no flow and no ADR for
  `event` yet (`grep -rln "frostgrove/vv/event" docs/` finds only the roadmap), and `CLAUDE.md`
  points every reader at `docs/`, not at `.agents/artifacts/`. `universality.md`'s question — *where
  does this value come from, and what happens on an input where it is wrong* — is answerable for all
  six, but only from an artifact the next agent is not told to read. The one worth naming in the
  answer is `MaxKeyBytes = 2 << 10`, derived from "the narrowest index tuple among the engines phase 2
  targets" (PostgreSQL btree ~2704 B, MySQL InnoDB 3072 B): a **SQL** limit installed as a ceiling in
  a store-agnostic kernel, which a document store or a file-per-stream store then inherits with no
  way to publish a larger `MaxKey`.
- **Why this severity:** `low`. Every number is defensible today and each is a *ceiling* a store
  publishes below, so nothing is wrong on any input; the exposure is the ordinary one — the next
  person to meet `MaxKeyBytes` has nothing in the repository to weigh it against and either edits it
  (which PLAN.md:572 forbids in terms) or works around it.
- **Why this timing:** `deferred` — S6 is where `docs/modules/en/event.md` and the ADRs land, and the
  plan already schedules it.
- **Close criteria:**
  - [ ] `docs/modules/en/event.md` carries the six-row derivation table, and the RU page beside it.
  - [ ] The `MaxKeyBytes` row states what a non-SQL store does with a SQL-derived ceiling, and
        `event/bounds.go` names the escape hatch (a digest in the application's own mapper) rather
        than leaving "never by editing `event/bounds.go`" only in the plan.
  - [ ] `scripts/docs_test.go`'s symbol arm covers the constants the page names, so the table cannot
        drift from the code.
- **Status:** **deferred, carried into PLAN.md `## Debt`** — round 5, at the finding's own timing.
  S6 already schedules `docs/modules/en/event.md` and its RU page; the Debt row names the three
  obligations so they cannot be written without the `MaxKeyBytes` question — what a non-SQL store
  does with a SQL-derived ceiling, and where the escape hatch is — being answered on the page.

---

### GAP-156 [low][deferred] `Cursor("")` has no stated meaning at either end of `ReadAll`, so two conformant stores can disagree about what the first read is

- **Where:** `event/identity.go:14` (`type Cursor string`), `event/store.go:107-112`
  (`ReadAll(ctx, after Cursor)` — "Envelopes at ascending positions after the cursor … A cursor minted
  over another backing, one this store cannot parse, and one in a format it no longer accepts are all
  `Failure(BadCursor, …)`").
- **What:** the zero `Cursor` is what a caller starting a projection has and what a store returns if
  it forgets to mint one, and the contract says nothing about it. Three readings are all consistent
  with the text as written: "from the beginning", "unparseable → `Failure(BadCursor, …)`", and
  "nothing to resume from → empty page". [SPEC] §UC-053 and §INV-035 state only that resuming from a
  cursor the store returned skips nothing.
- **Why this severity:** `low`. Any disagreement is loud rather than silent — a store that refuses
  `""` fails on the very first `ReadAll`, and a store that returns `""` fails §UC-039's tiling
  assertion in `global paging` — so it is a store-author trap, not a correctness hole.
- **Why this timing:** `deferred` — one clause, cheapest written with the `resumption` and
  `global paging` sections in S5.
- **Close criteria:**
  - [ ] `event/store.go`'s `ReadAll` clause states what the zero `Cursor` means as an argument and
        whether a store may return one.
  - [ ] `eventtest`'s `resumption` section starts a read from `Cursor("")` explicitly, so the answer
        is certified rather than assumed.
- **Status:** **deferred, carried into PLAN.md `## Debt`** — round 5, at the finding's own timing.
  The clause is one line on `ReadAll` and is cheapest written with the `resumption` and
  `global paging` sections in S5, where the case that certifies it is written in the same edit.

---

## Round 1 dispositions — 2026-09-07

| Gap | Severity | Disposition |
|---|---|---|
| GAP-147 | `[high][immediate]` | **closed** — `causeAsWrap` gates the two rows whose wrap is a cause (`ErrRefused` **and `ErrUpcast`**); reproduced at 41 cross-class edges and re-run green |
| GAP-148 | `[medium][immediate]` | **closed** — the bare context sentinel from `refuse`, and `failure.Error()` renders no cause |
| GAP-149 | `[medium][immediate]` | **closed** — the hop budget is shared through a `*int`; 524 287 visits → 64 |
| GAP-150 | `[medium][immediate]` | **closed** — `Stream.String` answers `[stream unnameable]` for a family that fails the text rule |
| GAP-151 | `[medium][immediate]` | **closed** — §D.14's three `AppendRequest` clauses on the fields, `Expected: 0` with its consequence |
| GAP-152 | `[medium][deferred]` | **deferred** to PLAN.md `## Debt`; criterion 2 answered in the plan (no blanket refusal, with the cost) |
| GAP-153 | `[low][immediate]` | **closed** in the plan — the comparability refusal and its sentinel are in both contract blocks |
| GAP-154 | `[low][deferred]` | **deferred** to PLAN.md `## Debt` — the S6 comment pass, with the two restating halves named |
| GAP-155 | `[low][deferred]` | **deferred** to PLAN.md `## Debt` — the six-row derivation table lands with `docs/modules/en/event.md` in S6 |
| GAP-156 | `[low][deferred]` | **deferred** to PLAN.md `## Debt` — the `Cursor("")` clause lands with `resumption` in S5 |

Nothing was rejected. **One thing the round did not find**, recorded because it was fixed under
GAP-147 and is the same defect: `upcastRefusal` also promoted its cause to the declared wrap, so an
application upcaster returning `event.ErrConflict` carried a **history**-class refusal into the
**write** class by the identical route. It is one `causeAsWrap` call and it is in the same edit.

Five of the six code closures were **reproduced before they were fixed and re-run after**, from a
throwaway `event/zz_probe_test.go` since deleted; each fix was then reverted in place to watch the
probe go red, and restored. GAP-151 and GAP-153 are contract text and have no runtime behaviour to
break. The re-run checkpoint is pasted in PLAN.md § S1.

---

## Deliberate divergences from [SPEC] found in the code and **not** raised as findings

Recorded so a later reader does not re-open them: each is a plan decision the code implements
faithfully, with the spec edit scheduled in S6.

- `errors.Is` does **not** reach a refusal's cause (`event/errors.go:82-87`), against [SPEC] §2.3's
  *"the store's own error rides along as the wrapped cause, so `errors.Is` reaches it"* and §INV-025's
  mechanism paragraph. Round 4's GAP-135, decided in PLAN.md:786-802; `CauseOf` is the named reader.
  S6's edit list carries the spec change.
- `ErrCursor` is **wiring** class (`event/errors.go:32`), against [SPEC] §2.3's store row and
  §UC-053's `Refusal` line. C5, PLAN.md:231-236.
- `Backing.Valid` is unexported (`event/backing.go:33`), against [SPEC] §D.14:5037. PLAN.md:679; no
  reader outside `event` loses anything, since `Backing.Equal(b, b)` answers the same question.
- Twenty-four sentinels and seven `Outcome` values, against [SPEC]'s twenty-two and six. C1, C2, C5.
- `docs/` carries nothing about `event` yet — no `docs/modules/en/event.md`, no flow, no ADR, and
  `docs/api/surface.md` has no `event` section. `CLAUDE.md` says doc drift is a defect and is fixed
  in the same change; the plan puts all six doc obligations in S6 and S1's checkpoint does not claim
  them. Noted, not raised, because the phase is not finished.

**Two more, added by round 6 and scheduled in the same S6 edit list:**

- The whole **request class** declares `crud.ErrBadRequest` (`event/errors.go:42-45`),
  which [SPEC] §2.3's class table does not carry. Without it *"a transport answers a client error"*
  was true of one member and false of three, which is GAP-157.
- §2.3's *"checkable prohibition"* on a store returning one of this vocabulary's sentinels as its
  cause is **not checked**; it is made harmless, because a wrap never answers for a target inside
  this vocabulary (`event/errors.go:101-128`). Checking it cost the decorator's and the upcaster's
  own error its `errors.Is` reachability, which is GAP-159. §INV-024's *Falsified by* clause moves
  with it, and the conformance defect it named leaves `defects.go`.

## Round 2 — econv-implementation-reviewer (clean context, re-audit after round 1's fixes) — 2026-09-07

Re-read against the **code** at its current state: `event/` — 9 files, **792 lines**, still no
`_test.go`. Read in full: `.agents/artifacts/plans/EVENTSOURCE_P1_PLAN.md` (§ Carried gaps C1–C10,
§ Microkernel classification, § Contracts `#### event/{identity,bounds,text,backing,authority,outcome,errors,store}.go`,
§ S1, § Architecture metrics, § Debt), `.agents/artifacts/usecases/EVENTSOURCE_P1_USECASES.md`
(§2.3, §D.14, §UC-051, §UC-048, §UC-022, §INV-016/024/025/028/045), round 1 of this file, `CLAUDE.md`,
and `~/.claude/skills/econv/references/*.md`. Every number below was produced by running in this
worktree from throwaway probes (`event/zz_review_probe_test.go`, `event/zz_probe2_test.go`,
`zzstatusprobe/`), **all three deleted after the run** — `ls event/` shows nine `.go` files and no
test file, and `git status` shows no probe.

**Round 1's five code closures re-verified against the code, not the disposition table.**

| Round-1 gap | Re-verified how | Result |
|---|---|---|
| GAP-147 `[high][immediate]` | the 24 × 23 pairwise table run **over refusals built from a store's cause** — `refuse(Failure(<each of 7 outcomes>, <each of 24 sentinels>), <both doors>)`, 336 refusals, every one probed against all 24 | **0 cross-class edges.** `Failure(Refused, event.ErrConflict)` → `Is(ErrRefused)=true`, `Is(event.ErrConflict)=false`, `Is(crud.ErrConflict)=false`. `upcastRefusal(fmt.Errorf("app: %w", ErrConflict))` → `Is(ErrUpcast)=true`, `Is(ErrConflict)=false`. Controls intact: `Failure(Refused, crud.ErrForbidden)` still answers `Is(crud.ErrForbidden)=true`, and `Failure(Refused, errs.Forbidden()…Fault())` still answers `errs.AsFault` → `kind=forbidden`. **Genuinely closed**, and the mechanism is a gate on the promotion (`causeAsWrap`), not a reworded comment. Its collateral cost is GAP-159 below |
| GAP-148 `[medium][immediate]` | `refuseRead(fmt.Errorf("read orders/A-17 at v9: %w", context.DeadlineExceeded))` and `Failure(NotWritten, errors.New("pq: password authentication failed for user \"admin\""))` | `"context deadline exceeded"` — the key and the version are gone, `Is(DeadlineExceeded)=true`, `Is(ErrBackend)=false`; `failure.Error()` is `"event: the store reported [outcome not written]"`. Second window: `Failure(Unconfirmed, context.Canceled)` → `Is(ErrUncertain)=true`, `Is(Canceled)=false`. **Closed** |
| GAP-149 `[medium][immediate]` | a balanced `Unwrap() []error` DAG at depths 18/22/24, and a cyclic `Unwrap() error` chain | the kernel's own `matches()` is **1.9 µs at depth 24** and survives a cycle. But `refuseAppend(dag)` is **8.6 ms / 136 ms / 555 ms** at depths 18/22/24 and the cyclic chain **hangs**. **Closed only for `walk`** — see GAP-158 |
| GAP-150 `[medium][immediate]` | `Stream{Family: "orders\n\tFAKE LOG LINE"}.String()`, a `MaxNameBytes+1` family, and `Stream{}` | all three render `"[stream unnameable]"` (19 bytes); `Stream{Family: "accounts.account"}` still renders `"[stream accounts.account]"`. **Closed** |
| GAP-151 `[medium][immediate]` | read `event/store.go:63-80` | all three §D.14 clauses are on the fields; `Expected`'s carries "Zero means the stream has never been appended to and never means 'at any version'". **Closed** |
| GAP-153 `[low][immediate]` | read PLAN.md `#### event/backing.go` / `#### event/authority.go` | both blocks now state the comparability refusal and `ErrWrongStore`; the code refuses `[]int{1}` with `"a backing identity is compared, so it must be comparable"`. **Closed** |
| GAP-152/154/155/156 `[deferred]` | read PLAN.md `## Debt` lines 3053-3103 | all four carried with their reasons, none dropped. **Correctly deferred.** GAP-152 re-measured live: `NewAuthority(b, 7)` twice → `Same()=true` |

**Clean checks, with the numbers.**

*Checkpoint, re-executed verbatim from PLAN.md:1934.* `go build ./... && go vet ./event/ && go test -race -count=1 ./event/`
→ `? github.com/frostgrove/vv/event [no test files]`, `EXIT=0`; `gofmt -l event` silent; the
four-package import-graph string comparison returns
`github.com/frostgrove/vv/crud,github.com/frostgrove/vv/errs,github.com/frostgrove/vv/event,github.com/frostgrove/vv/utils`
and is **true**. The transcript pasted at PLAN.md:1996-2016 is real. `make check` green on all
nine arms (`check-deps`, `check-tiers`, `check-utils`, `check-triplets`, `check-todo`,
`check-replaces`, `check-tidy`, `check-otel-schema`, `check-workspace`). `go test -race ./event/...`
exercises nothing, which is the phase-4/phase-5 split and not a hole.

*Architecture metrics, counted.* Files: `errors.go` **307**, `store.go` 182, `identity.go` 84,
`outcome.go` 61, `backing.go` 50, `authority.go` 46, `text.go` 43, `bounds.go` 16, `doc.go` 3 —
**792 total**, longest 307 against a 400 threshold. **45 functions**; longest `walkWithin` at **22**
lines, then `escapePart` 21, `checkText` 19; maximum nesting depth **4**. Internal
(`frostgrove/vv`) imports per file: `errors.go` **2**, `backing.go` 1, `authority.go` 1, the other
six **0** — threshold 5. Exported surface, counted with `go doc -all`: **16 types, 5 functions,
24 sentinels, 16 constants, 9 methods** = 61 top-level + 9 methods. That matches the plan's per-file
table **row for row** (identity 6, bounds 6, text 0, backing 2+2, authority 2+4, outcome 9,
errors 25, store 11); `Store` has exactly **8** methods, the declared breach. **0 import cycles**;
**0** packages import `event`. Fakes to unit-test any S1 object: **0**.

*Microkernel purity, with the command and the result.*
`grep -rniE "eventmemory|eventpg|eventtest|postgres|pgx|sqlite|mysql|redis" event/` → **no hits,
exit 1**. `grep -n "func init(\|registry\|Register" event/*.go` → **no hits**. Two type assertions
in the section (`errors.go:263` over the two standard `Unwrap` interfaces, `errors.go:290` for
`*errs.Fault`) and no assertion over a store. Phase 2's `eventpg` still needs zero `event/` diffs:
every seam value is a plain struct with exported fields, a defined string/uint64, an exported
constant, or an opaque value with an exported minting constructor.

*Universality sweep.* Every string literal in `event/` is a sentinel message, a bracketed
classification (`[stream unnameable]`, `[event backing]`, `[outcome not written]`, `[support
unstated]`) or a format string; **no aggregate name, no family, no wire type, no fixture path, no
env read**. Numeric literals are the six ceilings (GAP-155, deferred) and `causeHops = 64`, whose
precedent is `cache/errors.go:92 budget := 64`. `parts[2]`-shaped assumptions: none — `Compose` is
variadic and total.

*Purity and determinism.* `grep -rnE '\btime\.Now|rand\.|os\.(Getenv|Open|Read|Write)|log\.|fmt\.Print|http\.|net\.|sync\.|go func' event/` → **no hits**. `panic(` 0; `recover(` 2, both in the
defensive walk. No map iteration anywhere; `vocabulary()` returns an ordered slice and
`causeAsWrap` short-circuits in that order, so the section is deterministic. 24 package-level
`var`s, none reassigned.

*Compose, re-run.* 10 part lists including `/`, `%`, `%2F`, NUL, `0x7F`, invalid UTF-8 and
Cyrillic — every output passes the kernel's own `checkText`, and the only collision is the pair the
contract names (`Compose()` and `Compose("")`, both `""`, both refused as empty at every door).

---

### GAP-157 [high][immediate] Three of the four **request**-class sentinels declare no class wrap, so `ErrKey` renders **500** — the operator page §UC-051 chose the request class specifically to prevent

- **Where:** `event/errors.go:35-38` (`ErrKey`, `ErrEncode`, `ErrSample` are `errors.New` with no
  wrap; only `ErrTooLarge` gets one, and only at `event/errors.go:140-144` `tooLarge`), and
  `event/errors.go:96-105` (`refusal.Is`/`refusal.As` reach `sentinel` and `wrapped`, and `wrapped`
  is `nil` for all three). PLAN.md:879's `wrapped` table row: *"every other sentinel | nothing"*.
- **What:** [SPEC] §2.3 states the request class as *"The data this operation was given cannot be
  used. The ordinary cause is a request; **a transport answers a client error**"*, and §UC-051 gives
  the reason in terms: *"`ErrKey`, **request** class. The wiring class would report the commonest
  cause — a request that omitted an id — as an operator failure and turn ordinary bad input into a
  page (GAP-52). A blank or over-long identity arrives with a request, and [[D-040]]'s line is that
  **the class decides the status**."* In the code the class decides nothing: the **sentinel's
  declared wrap** decides, through `port.KindOf` → `errs.AsFault` then `sentinelKind`'s six
  `errors.Is` branches. Measured over all 24 sentinels with `porthttp.KindOf`/`porthttp.Status`:

  ```
  request  ErrKey      kind=internal  status=500      request  ErrTooLarge (bare)  kind=internal  status=500
  request  ErrEncode   kind=internal  status=500      write    ErrConflict         kind=conflict  status=409
  request  ErrSample   kind=internal  status=500      (all 19 others)              kind=internal  status=500
  ```

  `ErrTooLarge`'s 413 comes from `tooLarge()`'s `*errs.Fault{KindTooLarge}`, verified separately
  (`errors.Is(err, ErrTooLarge)=true`, `errs.AsFault` → `kind=too_large`, message *"the payload is
  2097152 bytes against a bound of 1048576"*). No such construction exists for the other three, in
  S1 or anywhere in the plan — `grep -n "ErrKey" PLAN.md` returns six hits and none of them names a
  wrap.
- **Why this severity:** `high`. Concrete: an HTTP handler takes `?id=` and its mapper renders
  `event.Compose(tenant, id)`; a client sends `?id=`, the key is empty, `Load` refuses with `ErrKey`
  before any statement (§UC-051, §D.5's `Load` order), and `porthttp.Status(err)` answers **500**.
  The client is told the server broke and cannot correct its own request; the operator's 5xx alert
  fires on ordinary bad input. That is GAP-52's failure verbatim, re-created one layer down after
  §2.3 moved the sentinel out of the wiring class to avoid it. It also makes §2.3's class table
  false as a caller-facing document — a caller who reads *"a transport answers a client error"* and
  writes no per-sentinel branch gets 500 for three of the four members — and it makes C5's own
  argument (PLAN.md:229-233, *"the request class would answer a client 413"*) true of exactly one
  sentinel. The remedy is dependency-free: `event` already imports `crud`, and
  `port/kind.go:87` has a `crud.ErrBadRequest` branch, so `ErrKey` declaring
  `crud.ErrBadRequest` as its wrap is one line and renders 400.
- **Why this timing:** `immediate`. The declared-wrap set is S1's deliverable and [SPEC] §2.3's own
  growth rule makes it breaking to change later — a caller who has written
  `errors.Is(err, crud.ErrBadRequest)` and one who has not are affected in opposite directions once
  a wrap appears. S4 raises `ErrKey` at four doors (PLAN.md:1447, 1452, 1507) and S4's
  `TestARequestClassRefusalRendersAClientStatusAndAHistoryClassOneDoesNot` is written to assert
  exactly one request-class sentinel (`ErrTooLarge` → 413) and one history-class one
  (`ErrPayload` → 500), so it would go green over this gap.
- **Close criteria:**
  - [ ] `event/errors.go` states, for **every** sentinel in the request class, what a transport
        answers for it — either by giving `ErrKey` (and `ErrEncode`, `ErrSample` if they belong) a
        declared class wrap that `port.KindOf` maps to a client status, or by recording on
        `event/errors.go` and in [SPEC] §2.3 that the request class does **not** promise a client
        status and why §UC-051's argument survives that.
  - [ ] S4's `TestARequestClassRefusalRendersAClientStatusAndAHistoryClassOneDoesNot` asserts the
        status of **every** request-class sentinel, not one, with a history-class and a wiring-class
        sentinel as its 500 controls — so "renders a client status" cannot be true of one member and
        false of three.
  - [ ] The plan's `wrapped` table (PLAN.md:872-879) and §2.3's class table agree with the code
        about which sentinels reach a transport class, and the count of declared class wraps in
        `event/errors.go:12-17` ("closed at five") is updated with them.

- **Status:** **closed** — round 6, by the first branch: `ErrKey`, `ErrEncode`,
  `ErrSample` **and `ErrTooLarge`** each declare `crud.ErrBadRequest` on the
  sentinel itself, in the `fmt.Errorf` that builds it, which is `ErrConflict →
  crud.ErrConflict`'s own existing shape one class over. Reproduced first — all
  four rendered `kind=internal status=500`, bare and inside a refusal, while
  `tooLarge()`'s fault rendered 413 and `ErrConflict` 409 — and after the change
  all four render **400**, with 413 and 409 unchanged. `ErrTooLarge` keeps its
  fault and still renders 413, because `port.KindOf` asks `errs.AsFault` before
  `sentinelKind`; only a bare `ErrTooLarge` falls back to 400, which is the
  point. **On the sentinel and not at the door** because S4 raises `ErrKey` at
  four of them and a wrap that must be remembered four times is a wrap that will
  be forgotten once; it also makes the promise true of a decorator or a store
  that returns the sentinel itself. `tooLarge()` now returns the plain sentinel
  with the fault as its wrap rather than `fmt.Errorf("%w: %s", ErrTooLarge,
  rule)` — three colons in one message read worse than the rule name does in the
  fault, which already carries `"the payload is 2097152 bytes against a bound of
  1048576"` and is the channel a transport renders. PLAN.md's class table, its
  `wrapped` table and the count ("eight declared class wraps") say it, with the
  measurement; the S4 test description now names **every** request-class member
  with a history-class and a wiring-class sentinel as its 500 controls.

---

### GAP-158 [medium][immediate] GAP-149's fix bounded the kernel's own walk and left three **stdlib `errors.As`** calls on the same foreign chains — one of them the first statement executed on every store error, where a cyclic chain hangs forever

- **Where:** `event/errors.go:181` (`errors.As(err, &classified)` inside `refuse`, on a store's own
  error), `event/errors.go:104` (`errors.As(this.wrapped, target)` inside `refusal.As`, on a
  promoted foreign cause — the `ErrRefused` and `ErrUpcast` rows), `event/errors.go:109`
  (`errors.As(err, &refused)` inside the exported `CauseOf`). The bounded walk they bypass is
  `event/errors.go:237-278`.
- **What:** the comment the fix put on the walk (`event/errors.go:239-246`) claims the property for
  the kernel and not for one function: *"The kernel reads errors it did not build — a store's cause,
  a decorator's refusal, an application's upcast error — so the walk carries a hop budget and a
  recover: **a chain that loops** and a matcher that panics are that party's defect and must not
  become the kernel's."* `refuse` never reaches the walk before deciding, and PLAN.md:1917 states the
  closure as *"the walk's hop budget bounds **visits rather than depth**, so a branching
  `errors.Join` chain cannot hang the request goroutine"*. Measured, same balanced
  `Unwrap() []error` DAG the round-1 finding used, plus a cyclic `Unwrap() error`:

  ```
  depth 18   kernel matches()  9.4 µs   |  refuseAppend(dag)    8.66 ms
  depth 22   kernel matches()  2.1 µs   |  refuseAppend(dag)  135.96 ms
  depth 24   kernel matches()  1.9 µs   |  refuseAppend(dag)  555.19 ms   (doubling per level)
  cyclic Unwrap() error:  kernel matches() survived;  errors.As HUNG (>2 s, no bound, no recover)
  errs.AsFault over an ErrRefused whose promoted cause is a depth-22 DAG:  139.20 ms
  ```

  The same split has a second, quieter consequence: `Is` is bounded at 64 hops and `As` is not, so
  the two traversals disagree past the budget. `Failure(Refused, <100-layer chain carrying
  event.ErrConflict>)` was measured as `promoted=true` — `causeAsWrap` could not see the sentinel at
  depth 100 — while `errors.Is(err, event.ErrConflict)` stayed `false` because `refusal.Is` is
  bounded by the same 64. So the prohibition GAP-147 closed is enforced **to depth 64 only**, and a
  declared wrap deeper than 64 (a `crud.ErrForbidden` a decorator meant as a 403) is invisible to
  `Is` while an `*errs.Fault` at the same depth is still found by `As` and still sets the status.
- **Why this severity:** `medium`, the same grade round 1 gave the identical failure mode. `refuse`
  runs on **every** error from every door, and `refusal.As` runs on every transport rendering
  (`port.KindOf` → `errs.AsFault` → `refusal.As`). The cyclic case is the sharp one: a store or
  decorator whose error type returns itself from `Unwrap` wedges the request goroutine inside the
  kernel permanently, holding the caller's ambient transaction open under [[D-118]] — which is
  exactly the outcome the budget and the `recover` were written for, named in their own comment. Not
  `high` because both producing inputs are defects in the party that built the chain (a shared-subtree
  DAG 24 levels deep, or a self-referential `Unwrap`), and the ordinary left-deep
  `errors.Join(err, e)` loop stays linear under both traversals.
- **Why this timing:** `immediate`. The closure is recorded as complete in round 1's disposition
  table and in PLAN.md § S1, so nothing later will look again; `event/errors.go` freezes at S6; and
  the phase-5 test the plan added for it (`TestAJoinedCauseCannotOutlastTheWalksBudget`, described at
  PLAN.md:1990-1994 as counting the walk's visits) measures the traversal that was already fixed and
  cannot see the three that were not.
- **Close criteria:**
  - [ ] Every traversal the kernel performs over an error it did not build is bounded and
        panic-guarded — including the `*failure` lookup in `refuse`, the declared-wrap traversal in
        `refusal.As` and the lookup in `CauseOf` — or `event/errors.go` states which traversals are
        deliberately unbounded and why the loop and the branch cannot reach them.
  - [ ] `TestAJoinedCauseCannotOutlastTheWalksBudget` covers `refuse` and `refusal.As`, not only
        `matches`, and adds a **cyclic** `Unwrap() error` chain with a bound on completion; the
        left-deep chain stays the control.
  - [ ] `event/errors.go:239-246`'s comment says which calls the budget covers, so it no longer
        claims a property of "the kernel" that three of its own calls do not have.
  - [ ] The 64-hop budget's effect on a **declared wrap** is stated where `causeAsWrap` and
        `refusal.Is` are read: past the budget a wrap is neither detected nor reachable, and `As`
        does not share the bound.

- **Status:** **closed** — round 6, all four criteria. The kernel now performs
  exactly three traversals over a chain it did not build — `matches`, `findAs`
  and `withinBudget` — and calls no stdlib `errors.Is` or `errors.As` on one.
  `findAs[T]` is the bounded `errors.As`: it visits under the shared `*int`
  budget and the same `recover`, and at each node asks what the stdlib asks —
  is this node a `T`, or does it answer for one through `As(any) bool`. It
  replaces `errors.As(err, &classified)` in `refuse`, `errors.As(err, &refused)`
  in `CauseOf`, and `faultIn`. The **fourth** call, the stdlib `errors.As` inside
  `refusal.As`, is kept and bounded at the other end: `causeAsWrap` promotes a
  cause to a wrap only when `withinBudget` could read it to the end, so the one
  stdlib traversal left runs over a chain known to terminate inside `causeHops`.
  That is also criterion 4's answer, and it is stronger than stating the
  disagreement: past the budget the cause is not promoted at all, so `Is` and
  `As` agree — both answer false — instead of one seeing a `crud.ErrForbidden`
  the other cannot. `refusal.As` also carries a `recover`, because a foreign
  `As` method may panic and it is called from stdlib `errors.As` in a caller's
  goroutine. Reproduced first: `refuseAppend` and `CauseOf` over a cyclic
  `Unwrap() error` chain **never returned** (>2 s, killed), and
  `refuseAppend(balanced depth-22 join tree)` cost **813 ms**; after, the cyclic
  chain returns and the same tree costs **25.6 µs**. The walk's comment names
  the three traversals it covers, says the stdlib one is bounded at promotion
  time instead, and states what no budget can bound — the work a foreign `Is` or
  `As` method does inside one visit. `TestAJoinedCauseCannotOutlastTheWalksBudget`
  is respecified to cover `refuseAppend`, `refuseRead`, `CauseOf`, `retryable`
  and a rendering through `errs.AsFault`, and to add the cyclic chain, with the
  left-deep chain still the control.

---

### GAP-159 [medium][immediate] GAP-147's fix drops the **whole** cause, so an upcaster's or a decorator's own error loses `errors.Is` reachability whenever it co-carries a vocabulary sentinel — and the obligation is stated on the store seam only, never on the upcaster's

- **Where:** `event/errors.go:131-138` (`causeAsWrap` returns `nil` for the entire cause when
  `errors.Is` from it reaches any of the twenty-four), `event/errors.go:146` (`upcastRefusal`),
  `event/errors.go:206` (the `Refused` arm).
- **What:** [SPEC] §2.3 lists `ErrUpcast → the application's own error` as one of the declared class
  wraps and gives the reason: *"The one place an application-supplied pure function may say 'no', and
  **the application must be able to match its own error with `errors.Is`**."* The gate is
  all-or-nothing, so an application error that *also* carries a vocabulary sentinel takes its own
  matchability down with it. Measured:

  ```
  cause = fmt.Errorf("%w: %w", errAppUnknownShape, event.ErrRevision)
  upcastRefusal(cause)  → Is(ErrUpcast)=true  Is(errAppUnknownShape)=FALSE  Is(ErrRevision)=false
  control, cause = fmt.Errorf("%w", errAppUnknownShape)
                        → Is(errAppUnknownShape)=true
  decorator: Failure(Refused, fmt.Errorf("%w (quota %w)", errAppUnknownShape, event.ErrTooLarge))
                        → Is(ErrRefused)=true  Is(errAppUnknownShape)=FALSE
  ```

  The producing input is not contrived for the upcaster: an upcaster whose complaint *is* "this
  revision is not one I can read" reaches for `event.ErrRevision`, which the vocabulary offers by
  that exact name, and Go 1.20's multi-`%w` makes co-wrapping the ordinary way to say two things at
  once. And where a **store's** obligation not to return a vocabulary sentinel is written down
  ([SPEC] §2.3, *"a checkable prohibition"*, restated on `event/errors.go:119-130`), the
  **upcaster's** equivalent is written nowhere: not on the seam, not in [SPEC]'s §UC-016/§INV-024,
  and the plan's twelfth C8 defect (PLAN.md:326-328) covers only *"the decorator that returns one of
  this vocabulary's own sentinels as its cause"* against `refusal classes`.
- **Why this severity:** `medium`. Silent and total: `errors.Is(err, orders.ErrUnknownShape)` answers
  `false`, so the application's own recovery branch never fires, the refusal renders 500, and the
  only diagnostic is `CauseOf` — which no `errors.Is` call site is going to reach for, because the
  caller has no reason to believe its own error was dropped. Not `high` because the caller still gets
  a correctly-classified `ErrUpcast`/`ErrRefused` and the cause is not lost, only unreachable by the
  one traversal §2.3 promises for it; and because the alternative the round-1 closure rejected (a
  promoted sentinel) is worse.
- **Why this timing:** `immediate`. It is a consequence of the round-1 fix, the wrap set freezes at
  S6, and the remedy space includes a shape change (refuse the co-wrap loudly at `Failure`/upcast
  time, or strip only the offending edge) that gets more expensive once S2's upcaster seam and S5's
  suite are written against the current one.
- **Close criteria:**
  - [ ] The upcaster seam states the same obligation the store seam states — an upcaster's error must
        not itself reach a sentinel of this vocabulary — where an implementer reads it
        (`event/codec.go` or `event/fact.go` in S2, and [SPEC] §2.3's cause paragraph), or
        `causeAsWrap`'s comment records that the application's own sentinel is collateral and why
        that is accepted.
  - [ ] A test in `event/` asserts the co-wrap case in both directions: an application error that
        co-carries a vocabulary sentinel, and the same error alone as its control — so the loss is a
        pinned decision and not an emergent one.
  - [ ] `eventtest`'s defect inventory gains the **upcaster** half of the twelfth defect, or the plan
        records why the store half alone is enough.

- **Status:** **closed** — round 6, by removing the collateral rather than
  documenting it. The finding's remedy space named "strip only the offending
  edge", which is impossible over a foreign error tree, and "refuse the co-wrap
  loudly", which gives `Failure` a second return value on the one path a store
  must not have to error-check. The third answer is to move the gate from the
  **promotion of the cause** to the **target of the traversal**: `refusal.Is`
  answers a target that is one of the twenty-four from `sentinel` alone and never
  from `wrapped`. `causeAsWrap`'s vocabulary scan is gone; `inVocabulary` is what
  replaces it, and it is a cheap identity check over `vocabulary()` rather than a
  walk of the cause.
  That is a **stronger** enforcement of §INV-024 than round 5's, not a weaker
  one: it holds for every row, not only the two whose wrap is a cause, and it
  cannot be defeated by a cause the walk cannot see to the bottom of. Measured
  whole after the change — 7 outcomes × 24 causes × 2 doors = **336 refusals**,
  each probed against all 24 sentinels, **336 matches in total**, so exactly one
  each and **no cross-class edge**; `Failure(Refused, event.ErrConflict)` still
  answers `Is(event.ErrConflict)=false`, which is §UC-022's livelock closed.
  And the collateral is gone: `upcastRefusal(fmt.Errorf("%w: %w", errApp,
  event.ErrRevision))` answers `Is(errApp)=true`, `Is(event.ErrRevision)=false`,
  where before the fix the first was **false**. C2's controls are intact —
  `Failure(Refused, crud.ErrForbidden)` → 403, `Failure(Refused, *errs.Fault)` →
  `errs.AsFault` kind `forbidden`, and a cause wrapping `event.ErrConflict` now
  renders **409** through `crud.ErrConflict`, which is the status the decorator
  asked for and is not a member of this vocabulary.
  Criterion 1 is answered on `inVocabulary` in `event/errors.go`, where the
  mechanism is, rather than on an S2 seam that does not exist yet — and no
  obligation is stated on the upcaster, because there is no longer one to state:
  the kernel makes the mistake harmless instead of asking the party not to make
  it. Criterion 3 falls out of that: the twelfth conformance defect **leaves**
  `defects.go` (a defect with no observable consequence cannot fail the section
  named for it) and is replaced by the decorator that forwards the append and
  then reports `Failure(Refused, …)`, which `refusal classes`'s zero-events
  assertion does see. Criterion 2 is the fourth `refusal classes` case and the
  co-wrap pair on `TestTheRefusalVocabularyIsAPartition`, both written into the
  plan. PLAN.md carries the argument at `#### event/errors.go` (*Why the target
  and not the promotion*), in C2, in C8 and in S6's [SPEC] edit list.

---

### GAP-160 [low][deferred] "Does this error carry a `Fault`" now has two answers in the framework — `errs.AsFault` and `event.faultIn` — and they disagree for a cause whose fault is reachable only through an `As` method

- **Where:** `event/errors.go:287-297` (`faultIn` finds a fault with the bare assertion
  `node.(*errs.Fault)` at each node of the bounded walk), against `errs/fault.go:106-112`
  (`AsFault` uses `errors.As`). `faultIn`'s only caller is `retryable`
  (`event/errors.go:229-235`), which decides whether `ErrBackend` class-wraps `crud.ErrUnavailable`.
- **What:** the kernel's walk follows `Unwrap() error` and `Unwrap() []error` and nothing else, so a
  cause that carries its fault behind an `As(any) bool` method — which is precisely the shape
  `event`'s own `refusal` has (`event/errors.go:103-105`: an `As`, no `Unwrap`) — is opaque to
  `faultIn` and transparent to `errs.AsFault`. `retryable`'s other arm survives it, because
  `matches` consults `Is` at each node and `refusal.Is` answers; the fault arm does not.
  `architecture.md`'s rule is one answer per question: two implementations that can diverge is what
  this is, and the divergence direction is a retryable store failure rendering **500** instead of
  **503**, so a caller does not retry something it should.
- **Why this severity:** `low`. Reaching it needs a cause that is an `As`-only error type; the
  ordinary `fmt.Errorf("%w", fault)` and a bare `*errs.Fault` are both found. No wrong data, one
  wrong status in a narrow shape.
- **Why this timing:** `deferred`. Closing it means either giving the walk an `As` arm or giving
  `errs` a bounded `AsFault` — the second is a change to another subsystem's surface, which the plan
  already parks for `crud.ValidDataSource` (`## Debt`). It rides with GAP-158's traversal work.
- **Close criteria:**
  - [ ] `faultIn` and `errs.AsFault` answer the same question the same way, or `event/errors.go`
        records which shapes the kernel deliberately does not look through and why.
  - [ ] A test asserts `retryable` over a cause whose `*errs.Fault{KindRetryable}` is reachable only
        through an `As` method, with the `fmt.Errorf("%w", fault)` shape as its control.

- **Status:** **closed** — round 6, not deferred, and the finding's own timing is
  why: it *"rides with GAP-158's traversal work"*, and GAP-158 is `[immediate]`.
  Once `faultIn` became `findAs[T]`, the second answer disappeared for free — the
  visitor asks the same two questions the stdlib asks at each node, so a node
  whose `T` is reachable only through an `As` method is found. No `errs` edit and
  no change to another subsystem's surface, which was the reason for the
  deferral. Reproduced first: `faultIn(asOnly{fault})` was **false** while
  `errs.AsFault` was **true**, and `Failure(NotWritten, asOnly{retryable fault})`
  rendered **500**; after, `findAs` is true, `retryable` is true and it renders
  **503**, with `fmt.Errorf("%w", fault)` unchanged as the control. The assertion
  lands on `TestADeclaredWrapIsReachableByErrorsAsAndACauseIsNot`. What the
  kernel deliberately still does not look through is stated on the walk: a
  foreign `Is` or `As` method's own work runs inside one visit and no budget
  bounds it.

---

### GAP-161 [low][immediate] The plan names S1's outcome test twice, under two different names, and the phase-5 checkpoint matches one of them literally

- **Where:** PLAN.md:787-790 (`*Test:* TestAnOutcomeOutsideTheVocabularyTakesTheFailSafeDefault …
  **S1** (the normalisation and `String`) + **S4** (the two doors)`) against PLAN.md:1883 and the
  phase-5 checkpoint at PLAN.md:1952, which both require
  `TestAnOutcomeOutsideTheVocabularyNormalises` and count exactly 8 names.
- **What:** the `#### event/outcome.go` contract block attributes S1's half of the work to the
  **S4** test's name, so a test author writing phase 5 from the contract block writes the wrong
  name. The coverage matrix uses both (PLAN.md:2522 and 2601 the S4 name, PLAN.md:2606 the S1 name),
  which is consistent with two tests existing — the contract block is the one site that is not.
- **Why this severity:** `low`. No code consequence, and the failure is loud: the checkpoint's
  `go test -list … | grep -c` clause returns 7 and the section cannot be marked `[x]`.
- **Why this timing:** `immediate` — the plan is the artifact phase 5 is written against, and the fix
  is one line.
- **Close criteria:**
  - [ ] PLAN.md:787 names `TestAnOutcomeOutsideTheVocabularyNormalises` for the S1 half and
        `TestAnOutcomeOutsideTheVocabularyTakesTheFailSafeDefault` for the S4 half, or merges them
        into one name used by both checkpoints.

- **Status:** **closed** — round 6, by the first branch rather than by merging:
  the two halves need different fixtures — S1's asserts the constructor holds
  `Unclassified` and that `String()` never renders a number, with no store at
  all, and S4's drives the same failure through `Append` and `ReadAll` — so one
  name over two sections would be a test that cannot run in the section that
  declares it. The `#### event/outcome.go` block now names both, says which
  section each belongs to, and says that each checkpoint names the one that
  belongs to it. Both checkpoints and both coverage-matrix rows were already
  right and are unchanged.

---

## Round 2 dispositions — 2026-09-07

| Gap | Severity | Disposition |
|---|---|---|
| GAP-157 | `[high][immediate]` | **closed** — the whole request class declares `crud.ErrBadRequest` on the sentinel; 500 → 400 for three, 413 and 409 unchanged |
| GAP-158 | `[medium][immediate]` | **closed** — three bounded traversals and no stdlib `errors.Is`/`errors.As` over a foreign chain; the cyclic chain returns, the depth-22 tree 813 ms → 25.6 µs |
| GAP-159 | `[medium][immediate]` | **closed** — the gate moves from the cause's promotion to the traversal's target; `Is(errApp)` false → true with the partition still whole at 336/336 |
| GAP-160 | `[low][deferred]` | **closed**, not deferred — it rides with GAP-158 by its own words and `findAs`'s `As` arm is one line; 500 → 503 |
| GAP-161 | `[low][immediate]` | **closed** in the plan — the contract block names both tests and their sections |

Nothing was rejected and **nothing was deferred**; round 1's four deferred items
(GAP-152, GAP-154, GAP-155, GAP-156) stay in PLAN.md `## Debt`, unchanged.

**One thing round 2 did not name and that was fixed under GAP-158**, recorded
because it is the same defect: `refusal.As` had no `recover`, and it is called
from stdlib `errors.As` on a caller's goroutine over a promoted foreign cause, so
a cause whose own `As` method panicked took the caller's request with it. It is
one `defer` and it is in the same edit.

**Where round 6's answer differs from a round-1 closure, and why that is not a
reopening.** GAP-147's close criterion 1 asked for a cause that reaches one of
the twenty-four to be *"refused or stripped where a refusal is built"*, and round
5 stripped it. GAP-159 is the bill for that: stripping is all-or-nothing over a
foreign error tree, so the application's own error went with the sentinel. Round
6 keeps GAP-147's **property** — no `errors.Is` from a refusal of one class
reaches a sentinel of another — and changes the **mechanism** to a gate on the
target of the traversal. The property is now enforced for all six classes rather
than for the two rows whose wrap is a cause, and it cannot be defeated by a cause
deeper than the walk's budget. Verified by the same 24 × 23 table round 2 used to
confirm the round-5 fix: 336 refusals, 336 vocabulary matches, one each.

Four of the five closures were **reproduced against the unfixed code first** and
re-run after, from a throwaway `event/zz_probe_test.go` since deleted (`ls event/`
shows nine `.go` files and no test file). GAP-161 is plan text and has no runtime
behaviour to break. The re-run checkpoint, `make check`'s nine arms and the
section's new line counts are pasted in PLAN.md § S1.

**What round 2 looked at and found clean**, recorded so a later round does not re-derive it: the
`Outcome` map at both doors including `Outcome(200)` (normalises in the constructor; `ErrUncertain`
from `Append`, `ErrBackend` from `ReadAll`) and a `%w`-wrapped `Failure(Conflict, …)` (still
classified, via `errors.As`, not a type assertion); `Backing`/`Authority` (`Backing{}.Equal(Backing{})`
false, `Authority{}.Valid()` false, `MarshalJSON` refuses, nil-by-any-route and comparability both
refused with `ErrWrongStore` and a message naming the rule and never the identity); `Compose`'s
legality and injectivity; the store contract's conformance to [SPEC] §D.14 clause by clause
(`Transaction`'s three answers, `ReadStream`'s `after + 1` and short-page clauses, `Append`'s
atomicity and conflict-door clauses, `Close`'s idempotence, the constancy of
`Capabilities`/`Limits` against the per-operation `Backing`, the panic rule, the
transaction-aftermath rule and the trust boundary); the section's purity, determinism and zero
global mutable state; and the exported-surface count against the plan's per-file table.

---

## Round 3 — econv-implementation-reviewer (clean context, re-audit after round 2's fixes) — 2026-09-07

Re-read against the **code**, not the dispositions. `event/` — 9 files, **839 lines**, still no
`_test.go` (`git status --untracked-files=all event/` lists exactly the nine `.go` files).
Read in full: `CLAUDE.md`, `.agents/artifacts/plans/EVENTSOURCE_P1_PLAN.md`
(§ Carried gaps C1–C10, § Contracts `#### event/*.go`, § Sections S1–S6, § Architecture metrics,
§ Debt), `.agents/artifacts/usecases/EVENTSOURCE_P1_USECASES.md` (§2.3, §D.13, §D.14, §D.15,
§INV-013/016/019/024/025/027/028/043/045), rounds 1–2 of this file, and
`~/.claude/skills/econv/references/{microkernel,architecture,building-blocks,data-integrity,universality,readability,restrictions,gaps}.md`.
Every number below was produced by running in this worktree from throwaway probes
(`event/zz_reviewprobe_test.go`, `zz_timing_test.go`, `zz_panic_test.go`, `zz_panic2_test.go`,
`event/zzprobe/`), **all deleted after the run**.

**Round 2's five closures re-verified against the code.**

| Round-2 gap | Re-verified how | Result |
|---|---|---|
| GAP-157 `[high][immediate]` | `errors.Is(<sentinel>, crud.ErrBadRequest)` over all four request-class members; `tooLarge("payload", 2<<20, 1<<20)` through `errs.AsFault` | all four true; the fault is `kind=too_large`, message `"payload is 2097152 bytes against a bound of 1048576"` — the bound and the count, never the payload; and the bare sentinel beneath it still answers `crud.ErrBadRequest`. **Closed** |
| GAP-158 `[medium][immediate]` | a balanced `errors.Join` tree at depths 18/22/24 through `refuseAppend`, and a self-referential `Unwrap() error` chain on a watchdog goroutine | **10.8 µs / 12.2 µs / 13.5 µs** (was 8.66 ms / 136 ms / 555 ms); the cyclic chain returns `ErrUncertain` immediately instead of hanging. **Closed** |
| GAP-159 `[medium][immediate]` | `upcastRefusal(fmt.Errorf("%w: %w", errApp, ErrRevision))` and `Failure(Refused, fmt.Errorf("%w: %w", errQuota, ErrConflict))` | `Is(errApp)=true` / `Is(ErrRevision)=false`; `Is(errQuota)=true` / `Is(ErrConflict)=false` / `Is(ErrRefused)=true`. The gate moved to the traversal's **target** and the application's own error survives. **Closed** |
| GAP-160 `[low]`, closed rather than deferred | `retryable` over an `*errs.Fault{KindRetryable}` reachable only through an `As` method | found; 503 rather than 500. **Closed — and it is the direct source of GAP-162 below**, which is what a re-audit is for |
| GAP-161 `[low][immediate]` | `grep -n TestAnOutcomeOutsideTheVocabulary PLAN.md` | S1's block names `…Normalises` and S4's names `…TakesTheFailSafeDefault`; the contract block (PLAN.md:804-808) names both and says which section owns which. **Closed** |
| GAP-147/148/149/150/151/153 (round 1) | the 24 × 23 partition table re-run whole; `Stream{Family:"orders\nFAKE"}.String()`; `failure.Error()` over a `pq:` text; `event/store.go:63-80` | **336 refusals × 24 targets → exactly 336 matches, one each, zero cross-class edges.** `"[stream unnameable]"`; `"event: the store reported [outcome not written]"`; the three §D.14 clauses on the fields. All still **closed** |
| GAP-152/154/155/156 `[deferred]` | PLAN.md `## Debt`; `NewAuthority(b, tx{1})` twice | all four carried, none dropped. GAP-152 re-measured live: two **distinct value** transactions still compare `Same()=true`. **Correctly deferred**; GAP-154 has grown and is re-stated as GAP-168 |

**Clean checks, with the numbers.**

*Checkpoint S1 phase 4, re-executed verbatim from PLAN.md:2048.* `go build ./...` EXIT=0;
`go vet ./event/` EXIT=0; `go test -race -count=1 ./event/` → `? github.com/frostgrove/vv/event [no test files]`, EXIT=0;
`gofmt -l event` silent; the import-graph string comparison returns
`github.com/frostgrove/vv/crud,github.com/frostgrove/vv/errs,github.com/frostgrove/vv/event,github.com/frostgrove/vv/utils`
and is **true**. `make check` green on all nine arms (`check-deps`, `check-tiers`, `check-utils`,
`check-triplets`, `check-todo`, `check-replaces`, `check-tidy`, `check-otel-schema`,
`check-workspace`). **The transcript pasted at PLAN.md:2091-2117 is real**; the section is honestly
`[~]` with `MISSING:` and its eight phase-5 names do not yet exist, which is the two-block rule
working. See GAP-167 for what that checkpoint does *not* prove.

*Architecture metrics, counted.* Files: `errors.go` **354**, `store.go` 182, `identity.go` 84,
`outcome.go` 61, `backing.go` 50, `authority.go` 46, `text.go` 43, `bounds.go` 16, `doc.go` 3 —
**839 total**, longest **354** against the 400 threshold. Longest function `walkWithin` **22** lines,
then `escapePart` 21, `checkText` 19, `Outcome.String` 18, `failure.refuse` 18 — **no function
reaches 40**. Maximum indent depth: `errors.go` 5, `identity.go` 4, `text.go` 3, everything else ≤ 2.
Internal (`frostgrove/vv`) imports per file: `errors.go` **2**, `backing.go` 1, `authority.go` 1,
the other six **0** — threshold 5. Exported surface counted by `go/ast` walk, discounting the four
methods on the two unexported types (`refusal.Error/Is/As`, `failure.Error`): **70** — identity 7,
bounds 6, text 0, backing 4, authority 6, outcome 10, errors 25, store 12. The plan's per-file table
undercounts three rows by one each (it omits `Stream.String`, `Outcome.String`, `Support.String`);
immaterial, the breach table already declares ≈82 for the package. `Store` has exactly **8** methods,
the declared breach. **0 import cycles**; **0** packages import `event`. Fakes to unit-test any S1
object: **0** — every one is an inert value or a pure function.

*Microkernel purity, with the command and the result.*
`grep -rn "eventmemory\|eventpg\|postgres\|pgx\|database/sql\|sqlrepo\|jobs\.\|tenancy\.\|port\." event/*.go`
→ **one hit and it is the word "import" inside a comment at `errors.go:193`**; zero code hits.
`grep -rnE "\.\(\*?[A-Z][A-Za-z]*Store\)|switch .*\.\(type\)" event/*.go` → one hit,
`errors.go:305`, and it is the two standard `Unwrap` interfaces, not a store. No `init`, no
registry, no `Register`. **Zero-diff proved at compile time, not asserted:** a throwaway
`event/zzprobe` package in a third package implemented the whole eight-method `Store` — returning
`Capabilities`, `Limits`, a `Backing` from `NewBacking`, an `Authority` from `NewAuthority`,
`Envelope` composite literals with all seven fields, `Cursor` by conversion, and
`Failure(BadCursor|Conflict|Refused, …)` — and `go build` and `go vet` both passed with **zero
diffs to `event/`**. The file that would have to change for `eventpg` is **none**.

*Purity and determinism.*
`grep -rn "time\.Now\|rand\.\|os\.\|io\.\|net/http\|log\." event/*.go` → **no hits**; `time` is
imported for the `Envelope.RecordedAt` field type only. `panic(` 0; `recover(` 3 (`walk`,
`refusal.As`, `sameError`) and all three are defences over a foreign chain. Three `range`
statements, **none over a map** (`Compose`'s parts, `vocabulary()`'s slice, `Unwrap() []error`'s
children), so iteration order is fixed. 25 package-level `var`s — 24 sentinels plus
`errAuthorityNotSerialisable` — every one initialised by `errors.New`/`fmt.Errorf` and none the
target of a later assignment: **0 global mutable state**.

*Universality sweep.* Every string literal is a sentinel message, a bracketed classification
(`[stream unnameable]`, `[event backing]`, `[outcome not written]`, `[support unstated]`), a
closed-set rule phrase (`is empty`, `is longer than its limit`, `is not valid UTF-8`, `contains a
control character`) or a format string. **No aggregate name, no family, no wire type, no fixture
path, no env read, no `if id == "…"`, no `parts[N]`, no `len(rows) == N`.** Numeric literals: the
six ceilings (GAP-155, deferred), `causeHops = 64` (precedent `cache/errors.go:92 budget := 64`),
and `0x0f`/`>>4` inside the hex escaper. `Compose` is variadic and total; `checkText` is
byte-length and rune-class only. Nothing here is fitted to one aggregate, one event type, one
payload shape or one size.

*Compose, re-run over 14 part lists* including `/`, `%`, `%2F`, `\x00`, an isolated `0xC2`,
Cyrillic, and empty parts in leading, middle and trailing position: every non-empty output passes
the kernel's own `checkText(…, MaxKeyBytes)`, and the only collision is the pair the contract names
(`Compose()` and `Compose("")`, both `""`, both refused as empty at every door).

*Naive-contract read of every exported signature.* `AppendRequest.Expected` carries the
zero-means-never-appended clause on the field (§D.14, GAP-151). `Backing{}.Equal(Backing{})` is
**false** and `Authority{}.Valid()` is **false**, so a zero value matches nothing including another
zero value — `crud.SameDataSource(nil, nil)` is `false` at `crud/executor.go:563`, verified.
`Failure` normalises out-of-range outcomes at construction (`Outcome(200).String()` →
`"[outcome unclassified]"`, no number). `Support`'s zero is `Unstated` and a store that forgets
`Capabilities()` returns the all-`Unstated` struct, which fails closed. `MarshalJSON` on `Authority`
always refuses. The one residual is GAP-152, already deferred.

---

### GAP-162 [high][immediate] `findAs` answers `(nil, true)`, and all three of its callers dereference at once — so an ordinary typed-nil `*errs.Fault` anywhere in a store's cause chain panics the kernel on the read door's fail-safe path

- **Where:** `event/errors.go:333-344` (`findAs[T error]`), and its three consumers
  `event/errors.go:207-208` (`refuse` → `classified.refuse(entered)`),
  `event/errors.go:259-260` (`retryable` → `fault.Kind`) and
  `event/errors.go:143-144` (`CauseOf` → `refused.cause`).
- **What:** `findAs` reports `ok = true` on two routes that leave `found` nil, and no caller checks:

  ```go
  if candidate, is := node.(T); is { found = candidate; return true }   // route 1: a typed-nil T
  asker, is := node.(interface{ As(any) bool })
  return is && asker.As(&found)                                          // route 2: an As that lies
  ```

  Route 1 fires for a node whose dynamic type is `T` and whose value is nil — `error((*errs.Fault)(nil))`
  is an ordinary Go value, `fmt.Errorf("store: %w", it)` renders `"store: <nil>"` without complaint,
  and `errs.Fault.Error()` is nil-safe, so nothing upstream catches it. Route 2 fires for any node
  whose `As(any) bool` returns true without setting the target, and route 2 is **new** — it is
  round 2's GAP-160 fix. `walk`'s `recover` does not help: it covers panics raised *inside* the
  visit; these three dereferences happen after `walk` has returned.
- **Why this severity:** measured in this worktree, four separate panics, each
  `runtime error: invalid memory address or nil pointer dereference`:

  | Input | Call | Result |
  |---|---|---|
  | `retryable(fmt.Errorf("store: %w", error((*errs.Fault)(nil))))` | `backendRefusal` → `retryable` | **panic**, while `errs.AsFault` on the identical value returns `(nil, true)` and does not |
  | `refuseAppend(fmt.Errorf("store lost the connection: %w", permissiveAs{}))` | `refuse`, the **first statement on every store error** | **panic** |
  | `refuseRead(<same>)` | `refuse` | **panic** |
  | `CauseOf(<same>)` | `CauseOf` | **panic** |

  `backendRefusal` → `retryable` is the read door's fail-safe default and `NotWritten`'s row, so
  this is the most-travelled error path in the subsystem. The failing input is a store that holds an
  `*errs.Fault` it did not populate and wraps it — a nil-interface mistake, not a contract
  violation; the store broke none of §D.14's rules. The blast radius is a panic on the caller's
  goroutine **during error handling, with the caller's ambient transaction open**, which under
  [[D-118]] is a transaction nobody now rolls back. And the file's own comment at
  `event/errors.go:265-272` promises the opposite in terms: *"a chain that loops and a matcher that
  panics are that party's defect and must not become the kernel's."* Today a matcher that merely
  lies, or a value that is merely nil, does become the kernel's.
- **Why this timing:** `findAs` is the classification seam every later section runs through —
  S3's `eventmemory` failures, S4's four doors, S5's six deliberately defective conformance stores
  (one of which is *built to return garbage*, which is exactly the shape that trips route 2). Fixing
  it after those exist means re-deriving which of them is allowed to panic. It is three lines.
- **Close criteria:**
  - [ ] `findAs` answers `false` when the located `T` is nil by any route (its own
        `nilByAnyRoute`, or an explicit `reflect` check), or each of the three callers guards
        before dereferencing — one of the two, stated once, not both spellings.
  - [ ] `retryable(fmt.Errorf("x: %w", error((*errs.Fault)(nil))))` returns `false` and does not
        panic; `refuseAppend`, `refuseRead` and `CauseOf` over an error whose chain carries a node
        with `As(any) bool { return true }` each return a value and do not panic.
  - [ ] A named test in `event/` pins all four — added to S1's phase-5 checkpoint list, whose count
        clause moves from 8 to 9 — with a well-behaved `As` and a populated `*errs.Fault` as its
        controls, so it cannot pass by the guard being unreachable.
  - [ ] `event/errors.go`'s walk comment says what the kernel now does with a matcher that answers
        for a value it did not set, since the current sentence claims immunity it does not have.
- **Status:** **closed** — phase 5. All four close criteria are met. The guard is in `findAs` and
  nowhere else, so the three callers are unchanged: a located `T` that is `nilByAnyRoute` is not a
  find, and the walk continues. Both routes are covered by the one guard — the typed-nil `T` and the
  `As` that answers without setting the target — because both now hand `candidate` to the same check
  before it becomes `found`. Reproduced first, four panics, each
  `runtime error: invalid memory address or nil pointer dereference`:
  `retryable(fmt.Errorf("store: %w", error((*errs.Fault)(nil))))`, and `refuseAppend`, `refuseRead`
  and `CauseOf` over `fmt.Errorf("...: %w", permissiveAs{})`. After the guard they answer `false`,
  `ErrUncertain`, `ErrBackend` and `nil`. `event/errors.go`'s walk comment now names the matcher that
  answers for a value it never set as the third thing the walk defends against, and says the
  producing input is an unpopulated `*errs.Fault` a store wrapped — a nil-interface mistake and not a
  contract violation. `TestANilOrLyingCauseNeverPanicsTheKernel` pins all four with three controls:
  a populated `*errs.Fault{KindRetryable}`, a fault reachable only through a well-behaved `As`, and a
  real `Failure(Conflict, …)` a decorator wrapped — so the guard cannot pass by refusing everything.
  Reverting it makes that test **panic** rather than fail. S1's phase-5 count clause moved 8 → 11.

---

### GAP-163 [medium][immediate] The partition gate reads a hand-maintained copy of the sentinel set, and its own test will read the same copy — so a sentinel omitted from `vocabulary()` reopens GAP-147 for that sentinel and the test passes vacuously

- **Where:** `event/errors.go:63-73` (`vocabulary()`), used once at `event/errors.go:121-128`
  (`inVocabulary`), which is the whole of `refusal.Is`'s cross-class gate at
  `event/errors.go:101-109`. PLAN.md:961 states the design: *"The sentinel list is what an
  unexported `vocabulary()` returns in `event/errors.go` — a function rather than a package-level
  slice, so §INV-013 stays literal — rather than a number in prose."*
- **What:** the 24 sentinels are declared at `event/errors.go:21-61` and listed again at
  `event/errors.go:63-73`. The two lists agree today — I counted both: 3 + 8 + 4 + 4 + 2 + 3 = 24
  declared, 24 returned. Nothing holds them together. The mechanism §INV-024's partition now rests
  on is `!inVocabulary(target) && matches(this.wrapped, target)`: a sentinel that exists but is
  missing from `vocabulary()` is treated as **not** part of the vocabulary, so a store's or an
  upcaster's cause carrying it is consulted and reaches it — GAP-147's defect, restored for exactly
  that row and silently.
- **Why this severity:** concrete: add `ErrDrained` to the write class in a later phase and forget
  line 70. A quota decorator writes the natural `Failure(Refused, fmt.Errorf("drained: %w",
  event.ErrDrained))`; a caller's `if errors.Is(err, event.ErrDrained) { … }` now fires on a
  **store-class** policy refusal, which is the cross-class reach §2.3 calls "false through a back
  door". It is not caught by the type checker, by `go vet`, by `make check` or by review, and — this
  is the part that makes it more than hygiene — it is not caught by the test either: `vocabulary()`
  is unexported, so `TestTheRefusalVocabularyIsAPartition` must live in package `event` and the only
  24-element list in the package is the one that is now wrong. The 24 × 23 table would be a 24 × 23
  table over the *surviving* 24 and pass. That is `CLAUDE.md`'s "a test that would still pass if the
  feature were deleted", arriving through the one door two audit rounds have already been spent on.
  Severity is medium rather than high because the two lists are correct **today** — measured,
  336/336, one match each.
- **Why this timing:** S6 is the section that writes the structural checks `go test` cannot see, and
  its file list is already fixed (`event/mutablestate_test.go`, `event/refusalmessages_test.go`,
  `event/transactioncontrol_test.go`, all `go/ast` walks over `event/`). This arm is the same
  plumbing and the same file set; deciding it after S6 is written means re-opening S6. It also
  changes what S1's phase-5 partition test must be built on, and phase 5 is next.
- **Close criteria:**
  - [ ] A check asserts set equality between `vocabulary()` and the exported package-level `Err*`
        error variables declared in `event/errors.go` — either a `go/ast` walk on S6's existing
        plumbing, or an in-package test that enumerates the declarations independently of
        `vocabulary()`. Not a count: a count passes on a swap.
  - [ ] The check is demonstrated to fail: remove one entry from `vocabulary()`, watch it go red,
        restore it, and say which entry in the section report.
  - [ ] The plan's `#### event/errors.go` block names the check beside the sentence at PLAN.md:961
        that currently states the design without an enforcement.
- **Status:** **closed** — phase 5, as an in-package test rather than on S6's plumbing, so S6 is not
  re-opened and the check exists before the section it protects is used. The first subtest of
  `TestTheRefusalVocabularyIsAPartition` parses `event/errors.go` with `go/ast` and compares three
  multisets: the exported `Err*` variables the file **declares**, the identifiers `vocabulary()`
  **returns**, and the test's own class-labelled table of twenty-four. Set equality in both
  directions, plus a value-set comparison against `vocabulary()`'s own return, so neither an omission
  nor a duplicate nor a name the table never heard of can pass. Demonstrated to fail: removing
  `ErrRevision` from `vocabulary()` reddens it with
  `ErrRevision appears 0 times listed in vocabulary(), which is the cross-class gate and 1 times in
  this test's table`, and — the point of the finding — the same mutation independently reddens the
  refusals subtest with `Refused failure at an append door over cause ErrRevision answers
  errors.Is(ErrRevision) as true`. The plan's `#### event/errors.go` block carries the check beside
  the sentence that stated the design.

---

### GAP-164 [medium][deferred] The store extension point declares no compatibility note where its implementer reads it, and the note [SPEC] does carry contradicts §INV-043 for the one struct most likely to grow

- **Where:** `event/store.go:104-182` (the `Log` and `Store` contract comments) — no growth or
  compatibility clause anywhere in the file. `event/errors.go:19-20` carries one, for the sentinel
  set only. [SPEC] §D.14's last paragraph (USECASES.md:5228-5235) carries the seam's:
  *"Growth is additive by new optional interface, never by a method on an existing one … Structs
  grow by field, and a field whose false value would skip a conformance section arrives as a
  `Support`, not a `bool`."*
- **What:** two things. (1) `microkernel.md` requires an extension point to declare a contract, a
  registration mechanism, a failure policy **and a compatibility note**; the first three are on the
  seam in code — the interface prose, "the value a composition root handed over", the `Outcome` map
  and the transaction-aftermath rule — and the fourth is only in a planning artifact under
  `.agents/`, which is not a place `CLAUDE.md` tells a reader of this repository to look, and which
  `eventpg`'s author in another repository cannot read at all. (2) "Structs grow by field" is false
  for `Capabilities` as built: a new `Support` field is `Unstated` in every existing store's
  composite literal, and §INV-043 has `Bind` **and** `Read` refuse an unstated capability, so adding
  one field stops every already-shipped store at the door. That is a defensible design — it fails
  closed — but it is the opposite of what the sentence promises, and the two are the same document.
- **Why this severity:** the failure is a phase-2 or phase-3 author reading §D.14, adding a fifth
  `Capabilities` field, and discovering at integration that every store outside the repository is
  refused at `Bind` with a wiring-class refusal naming nothing that points at the new field. No data
  is wrong; a release is. Medium, not high: nothing is broken today and the fix is prose plus one
  sentence of decision.
- **Why this timing:** deferred to **S6**, which is the section that writes the ADRs and
  `docs/modules/en/event.md`, and which is where a note about how the seam grows belongs beside
  D-121…D-125. It blocks nothing in S2–S5.
- **Close criteria:**
  - [ ] `event/store.go` carries the seam's growth rule where an implementer reads it: what may be
        added to `Store`, to `Capabilities`, to `Limits` and to `Outcome`, and what each does to a
        store compiled against the previous version.
  - [ ] The `Capabilities`-grows-by-field case is settled one way in [SPEC] §D.14 and in the code
        comment — either "adding a `Support` field is breaking and fails closed at `Bind`", or a
        stated rule that makes it additive — and the two say the same thing.
  - [ ] `docs/ai/decisions/` carries it, so the answer survives the plan artifact being archived.
- **Status:** open

---

### GAP-165 [low][immediate] The plan's `Outcome` map calls `Transaction`'s error a **wrap** where the code makes it a **cause** — the one distinction §2.3 spends a paragraph forbidding the conflation of

- **Where:** PLAN.md:1006 (`| a non-nil error from `Transaction` | `ErrAmbientNotTransaction`,
  **wrapping it** |`) against `event/errors.go:175-177`
  (`refuseTransaction(cause) → newRefusal(ErrAmbientNotTransaction, nil, cause)` — `wrapped` is
  `nil`). PLAN.md:998's own `wrapped` table says the opposite two rows earlier:
  *"every other sentinel | nothing — `As` from outside finds no framework type at all"*.
- **What:** the code is right and the prose is wrong. The store's error is the **cause**, reachable
  only through `CauseOf`; `errors.Is(err, thatError)` is `false` and `errors.As` finds nothing. The
  plan's map row says "wrapping it", which in this document's own vocabulary means reachable by
  `errors.Is`. [SPEC] §2.3 closes with *"Calling the two one thing is how a round-5 list gave one
  row two jobs"*, and this is that row.
- **Why this severity:** low — one word, and no code depends on it yet. It becomes wrong code the
  moment S4 implements `Within`/`Bind` from this table, because an implementer reading "wrapping it"
  writes `fmt.Errorf("%w: %w", ErrAmbientNotTransaction, storeErr)` and re-opens GAP-135: a store's
  incidental `crud.ErrUnavailable` would then set a wiring-class refusal's transport status to 503.
- **Why this timing:** immediate — it is a public-contract row that **S4 is about to depend on**,
  which is `gaps.md`'s own immediate trigger, and the edit is one word in the plan.
- **Close criteria:**
  - [ ] PLAN.md:1006 reads "carrying it as a cause, reachable through `CauseOf` and by neither
        traversal", consistent with PLAN.md:998's `wrapped` table.
  - [ ] The phase-5 `TestADeclaredWrapIsReachableByErrorsAsAndACauseIsNot` covers
        `refuseTransaction`, so the row is pinned by a test and not only by prose.
- **Status:** **closed** — phase 5, both criteria. PLAN.md's map row now reads *carrying it as a
  cause, reachable through `CauseOf` and by neither traversal*, with the finding's own argument for
  why the other word would re-open GAP-135 at S4.
  `TestADeclaredWrapIsReachableByErrorsAsAndACauseIsNot` has a `a transaction question's error is a
  cause and not a wrap` subtest: the refusal is `ErrAmbientNotTransaction`, `errors.Is` does **not**
  reach the `crud.ErrUnavailable` the store's answer carried, and `CauseOf` returns that answer
  unchanged.

---

### GAP-166 [low][deferred] `Stream.String()` still lets a store-supplied family forge a second bracketed field inside one log line — GAP-150 closed the newline route and not the bracket one

- **Where:** `event/identity.go:25-30`.
- **What:** the guard is `checkText(this.Family, MaxNameBytes)`, which refuses empty, over-cap,
  invalid UTF-8 and control characters. `]` and `[` are ordinary printable runes and pass. Measured:
  `Stream{Family: "orders] admin logged in [stream x"}.String()` renders
  `"[stream orders] admin logged in [stream x]"`.
- **Why this severity:** low. The family reaching this rendering is a store's data under the trust
  boundary at `event/store.go:83-86` — a restored dump, a shared database another service writes —
  and the residual is a forged *field* inside one line, not a forged line: no newline survives the
  rule, so a line-oriented log cannot be split and a structured logger emits it as one field's
  value. It is a narrowing of GAP-150's stated claim ("a forged log line"), not a hole in it.
- **Why this timing:** deferred. The kernel's own verification refusals that render a foreign
  stream are S4's, and the two candidate fixes — quoting with `%q`, or extending the text rule to
  the bracket pair — are both one line and both belong with §INV-011's source check, which S6
  already owns (`event/refusalmessages_test.go`, whose control is `Stream.String()`).
- **Close criteria:**
  - [ ] Either `Stream.String` quotes the family, or the reason a bracket is acceptable is written
        on the function beside the reason a newline is not.
  - [ ] `TestEveryRenderingNamesAClassAndNeverAValue` covers a bracket-bearing family, with the
        newline case as its control.
- **Status:** open

---

### GAP-167 [low][deferred] The phase-4 checkpoint's test clause executes none of the section's behaviour: six of its unexported functions have no caller anywhere in the tree, so the refusal map could be deleted and the checkpoint would still be green

- **Where:** `event/errors.go:167-181` (`tooLarge`, `upcastRefusal`, `refuseTransaction`,
  `refuseAppend`, `refuseRead`) and `event/store.go:30` (`Support.stated`) — counted with
  `grep -rn "\b<name>(" event/*.go` minus the declaration: **zero callers each**. Every other
  function in `event/errors.go` (`refuse`, `failure.refuse`, `door.unclassified`, `backendRefusal`,
  `retryable`, `newRefusal`, `causeAsWrap`, `walk`, `findAs`, `matches`) is reachable **only**
  through those five, so the whole store→kernel mapping half of the file — 190 of `errors.go`'s 354
  lines — is unreachable from anything the build or `make check` runs. The checkpoint clause is
  PLAN.md:2048's `go test -race -count=1 ./event/`, which prints `[no test files]` and exits **0**.
- **What:** the plan states its own rule at PLAN.md:1938 — *"a checkpoint that cannot fail is worse
  than no checkpoint"* — and applies it rigorously to `-run` patterns that match nothing, with a
  `go test -list | grep -c` count clause in front of every named test. The same vacuity arrives
  through the other door here and is not named: a `go test` clause over a package with no test files
  is a clause that cannot fail. What the phase-4 block genuinely proves is `go build`, `go vet`,
  `gofmt` and the four-package import graph — which is real evidence, and is the evidence the block's
  own prose claims for its last clause. It does not prove the refusal map exists.
- **Why this severity:** low. It is honestly declared: the section is `[~]` with `MISSING:` naming
  all eight phase-5 tests, the two-block rule is written out at PLAN.md:1943-1950, and phase 5 closes
  it structurally. Nothing is wrong; the evidence is narrower than a reader skimming a green
  checkpoint would take it to be. The five functions are S2's and S4's call sites and are not dead
  code in the ordinary sense.
- **Why this timing:** deferred — phase 5 is what closes it, and the change is one line in a
  checkpoint the plan will re-execute anyway.
- **Close criteria:**
  - [ ] S1's phase-4 block either drops the `go test` clause or says beside it that it asserts
        compilation, not behaviour, so `[no test files]` is not read as a passing suite.
  - [ ] The section report records that after phase 5 no unexported function under `event/` is
        without a caller — the same one-command count, re-run.
- **Status:** **closed** — phase 5, which is what the finding says closes it. The five functions the
  finding named — `tooLarge`, `upcastRefusal`, `refuseTransaction`, `refuseAppend`, `refuseRead` —
  are all driven by the suite, and through them the whole store→kernel mapping half of
  `event/errors.go`. Re-run, the only unexported function under `event/` still without a caller is
  `Support.stated`, which is S4's (`Bind` and `Read` read it for §INV-043); it is recorded in
  PLAN.md § S1 rather than left as a promise. The phase-4 block keeps its `go test` clause, which now
  runs eleven named tests instead of printing `[no test files]`.

---

### GAP-168 [low][deferred] GAP-154 re-measured: the comment ratio has grown to 27.8 %, no function in the package is long enough to earn one under `CLAUDE.md`'s rule, and the blocks now carry the arguments S6 will restate in D-121…D-125

- **Where:** `event/` as a whole — **233 comment lines of 839**, up from round 1's 24 %. Densest:
  `event/errors.go` ~140 of 354. Examples where the comment is longer than the code it sits on:
  `errors.go:110-120` (11 lines over a 7-line `inVocabulary`), `errors.go:264-277` (14 over a
  10-line `walk`), `errors.go:190-202` (13 over a 15-line `refuse`), `text.go:14-20` (7 over a
  19-line `checkText`, of which the first three restate the function's four branches verbatim).
- **What:** `CLAUDE.md` scopes a comment to *"a genuinely complex function of roughly 40+ lines: a
  non-trivial algorithm, a large raw SQL statement, or an invariant the code cannot make visible"*.
  Measured, the longest function in `event/` is **22 lines** and the median is 6. Comparable
  subsystems: `jobs` 170/35963 = **0.5 %**, `cache` 113/18367 = **0.6 %**, `tenancy` 262/2882 =
  **9 %**. A real part of the 233 is the third clause and earns its keep — the `Record.Payload` and
  `Envelope.Payload` ownership rules and `AppendRequest.Expected`'s zero clause are invariants the
  code cannot make visible to a store author in another package, and `CLAUDE.md`'s own carve-out
  covers them. The rest is design *argument*: why the target rather than the promotion, why a shared
  budget, why `Is` and `As` differ. S6 will write that same argument into D-121…D-125 and
  `docs/modules/en/event.md`, at which point the repository carries two copies of one reasoning that
  must be kept in sync — which is precisely the drift `CLAUDE.md`'s doc-consistency table treats as
  a defect, arriving from the side nobody checks.
- **Why this severity:** low — style and future maintenance, no behaviour.
- **Why this timing:** deferred, and it should be settled **while S6 writes the ADRs**, not before:
  the right cut is "the obligation stays in the code, the argument moves to the decision", and the
  decision does not exist yet.
- **Close criteria:**
  - [ ] After S6, each argument-shaped comment block under `event/` is either deleted in favour of a
        one-line `[[D-1NN]]` reference or is the one place that argument is written.
  - [ ] The obligation-shaped comments — `Record.Payload`, `Envelope.Payload`,
        `AppendRequest.Records`, `AppendRequest.Expected`, the trust boundary, the transaction
        aftermath — stay on the seam, since a store author in another module reads no `docs/`.
  - [ ] The ratio is re-counted and recorded in the section report beside `jobs`'s and `cache`'s.
- **Status:** open

---

### GAP-169 [low][deferred] The `Envelope` trust-boundary comment enumerates the checks and claims a store is "trusted … not at all", while `RecordedAt` is checked by nothing and is read by the caller

- **Where:** `event/store.go:83-102`. The comment lists four checks — the type name and
  `Stream.Family` against the kernel's identifier rule, the revision against the fact's own count,
  the payload against `Limits().MaxPayload` — and concludes *"so a store is trusted exactly as far
  as its numbers are, which is not at all."* `Version` and `Position` are checked elsewhere (the
  per-page density and ordering clauses at `event/store.go:156-163` and PLAN.md:1015). `RecordedAt`
  appears in no check in this section and in none the plan schedules for S4.
- **What:** the field is a `time.Time` a store fills from its own injected clock, handed straight to
  whoever receives the envelope. A store that returns the zero time, a time in the future, or a time
  carrying a monotonic reading that will not survive its own round trip is not refused and not
  flagged. The claim in the last sentence is therefore wider than the list above it.
- **Why this severity:** low. Nothing in the kernel reads `RecordedAt`, so no framework decision
  turns on it; the exposure is a consumer that orders or ages by it. There is also no obviously
  right check — a clock skew bound would be a calibrated threshold this framework has no basis to
  pick, which is why stating the residual is the better close than inventing one.
- **Why this timing:** deferred. It is a sentence on a contract, and the natural moment is S6's
  `docs/modules/en/event.md`, alongside the store's clock obligation.
- **Close criteria:**
  - [ ] `event/store.go`'s trust-boundary comment either names `RecordedAt` as unchecked and the
        caller's to distrust, or narrows its closing sentence to the values it enumerates.
  - [ ] The store's clock obligation — "its own injected clock", `event/store.go:169` — states
        whether `RecordedAt` must be UTC and whether it must be monotone within a stream, since
        `eventtest` cannot certify what the contract does not say.
- **Status:** open

---

## Round 3 verdict

**One `[high][immediate]` finding: GAP-162.** It is a genuine regression-shaped defect, reproduced
four times against the code, and it arrived with round 2's own GAP-160 closure — which is the
argument for the re-audit rule rather than against the fix. Everything round 2 claimed closed **is**
closed, verified by re-running the evidence rather than by reading the disposition table: the
partition is whole at 336/336 with zero cross-class edges, the request class renders 400, the
balanced join tree costs 13.5 µs instead of 555 ms, the cyclic chain returns, and the plan names its
two outcome tests once each. The section's zero-diff claim is now proved by compilation rather than
asserted: an eight-method `Store` in a third package builds with no change to `event/`.

---

## Round 3 dispositions — 2026-09-07, closed in phase 5

| Gap | Severity | Disposition |
|---|---|---|
| GAP-162 | `[high][immediate]` | **closed** — one guard in `findAs`; four reproduced panics → `false` / `ErrUncertain` / `ErrBackend` / `nil`, with `TestANilOrLyingCauseNeverPanicsTheKernel` and its three controls |
| GAP-163 | `[medium][immediate]` | **closed** — a `go/ast` three-multiset check inside `TestTheRefusalVocabularyIsAPartition`; demonstrated red by dropping `ErrRevision` from `vocabulary()` |
| GAP-164 | `[medium][deferred]` | **deferred**, unchanged — S6 writes the seam's growth rule beside D-121…D-125. Carried in PLAN.md `## Debt` |
| GAP-165 | `[low][immediate]` | **closed** — the map row says *cause*, and `refuseTransaction` is pinned by a subtest |
| GAP-166 | `[low][deferred]` | **deferred**, unchanged. Phase 5 deliberately wrote **no** bracket case: `Stream{Family: "orders] admin logged in [stream x"}` still renders a forged second field, and a test asserting today's answer would encode a known defect as expected. The newline, over-cap, invalid-UTF-8, NUL and empty cases are all in `TestEveryRenderingNamesAClassAndNeverAValue`, so the bracket case is one row to add when S6 chooses `%q` or the wider rule |
| GAP-167 | `[low][deferred]` | **closed by phase 5 rather than deferred** — the section now has behaviour under test, and the re-run count is in PLAN.md § S1: the only unexported function under `event/` still without a caller is `Support.stated`, which is S4's |
| GAP-168 | `[low][deferred]` | **deferred**, unchanged — the S6 comment pass. Phase 5 added no comment to `event/` beyond one clause on the walk that GAP-162 required |
| GAP-169 | `[low][deferred]` | **deferred**, unchanged — `RecordedAt` is still checked by nothing, and phase 5 asserts nothing about it |

**What phase 5 did not test, said plainly.** GAP-152's value-typed transaction identity is
`[medium][deferred]` and still live: `NewAuthority(b, 7)` twice answers `Same() == true`, so
`TestABackingAndAnAuthorityAreComparedAndNeverIdentical` drives **pointer** identities for its
two-live-transactions case and the value case waits for S5's `transactions` section, exactly as
PLAN.md `## Debt` records. Writing the value case now would either encode the defect as expected or
fail the gate for a finding whose declared remedy is two sections away.

---

## Round 4 dispositions — remediation pass, 2026-09-08

Cluster `aggregate-crossing-and-log-injection` of `EVENTSOURCE_P1_REMEDIATION.md` § 11. One finding
from this file.

| Gap | Severity | Disposition |
|---|---|---|
| GAP-166 | `[low][deferred]` | **closed by the wider rule, not by `%q`** — `event/text.go:checkFamily` is the kernel text rule plus `[` and `]`, and it runs at both doors a family crosses: `TryDefine` and `Stream.String` |

**Reproduced first, from `/tmp/vvprobe` through the exported API:**

```
injection:       [stream orders] admin logged in [stream x]     ← two fields in one line
control newline: [stream unnameable]
control plain:   [stream orders]
```

**After:**

```
injection:       [stream unnameable]
control newline: [stream unnameable]
control plain:   [stream orders]
```

**Why the wider rule and not `%q`.** Quoting annotates the closing bracket rather than removing it:
`[stream "orders] admin logged in [stream x"]` still closes the field the renderer opened, and a
log reader — or a grok pattern — that keys on that delimiter is still told there are two fields.
The rule the finding's own close criterion asks for ("the reason a bracket is acceptable is written
on the function") is instead the reason it is not, written on `checkFamily`.

**The rule is at the declaration too, and deliberately.** A family carrying a bracket is now
`ErrDeclaration` at `Define`/`TryDefine`, so the only family that can ever render as
`[stream unnameable]` is one that arrived on an `Envelope` — store data, declared by nobody this
program can see. Refusing it where it is declared is the direction `econv` asks for; refusing it at
the rendering alone would leave a legitimately declared family silently unnameable in every log
line. The rule is **not** in `checkText`: a key crosses that predicate too, and `Compose("a[1]")`
is a caller's identity domain, not a rendering.

**Left behind, and each was watched fail with the fix reverted:**

| Test | Reverted | Answer |
|---|---|---|
| `TestEveryRenderingNamesAClassAndNeverAValue/a family a store supplied is rendered only when it passed the kernel's own rule` | `Stream.String` back to `checkText` | `a stream whose family closes the field it is rendered in rendered "[stream orders] admin logged in [stream x]" rather than a classification` |
| the same subtest's one-field property, with the golden-string loop removed so it cannot be the one that fires | `Stream.String` back to `checkText` | `… rendered "[stream orders] admin logged in [stream x]", which is two fields in one log line` |
| `TestEveryMalformedDeclarationPanicsAndTryDefineReturnsIt` | `TryDefine` back to `checkText` | `a family carrying a bracket … was accepted, so a malformed declaration survives to be discovered at load or append time` |

The property assertion is the durable half: every rendering in `everyStreamRendering()` must hold
exactly one `[` and one `]`, so a later renderer that opens a second field fails without anybody
adding a row for it.

**Docs updated in the same change:** `docs/modules/en/event.md` and `docs/modules/ru/event.md` gain
*One family names one aggregate* / *Одно семейство именует один агрегат*, which state the family
rule and the bracket in it; `[[FL-036]]`'s declaration step, its `event/text.go` row and its test
table; `PLAN.md`'s `Stream.String` contract block, its exported-symbol row, its store-data
paragraph and the `## Debt` entry.

---

## Round 5 — econv-implementation-reviewer (clean context, re-audit of the round-4 remediation cluster) — 2026-09-08

Scope: **GAP-166** (`Stream.String` and the bracket route) and every file the round-4
disposition touched for it — `event/identity.go`, `event/text.go`, `event/aggregate.go`,
`event/rendering_test.go`, `event/declaration_test.go`. Nothing was inherited from the
disposition note: the defect was re-derived by constructing the input the original finding
named, and each repair was reverted in place, the suite run, and the file restored.

The tree was left byte-identical to how it was found. `md5sum` before the mutations and after
the restore: `event/identity.go` `f95198de6e880da5eeadbc158e7cacb6`, `event/aggregate.go`
`c9610ad1a3033239c267a221dc8daf27`, `event/change.go` `16fe2a4be211940732c77d53ec850198`.
The two throwaway probes (`event/zzaudit_probe_test.go`, `eventpgprobe/`) were deleted;
`git status --porcelain | grep -c "zzaudit\|eventpgprobe"` = **0**.

### Gate, run here

`gofmt -l .` silent (exit 0, whole repository) · `go vet ./event/...` exit 0 ·
`go test -race -count=2 ./event/...` green, no flake — `event` 6.081 s, `eventmemory` 1.489 s,
`eventtest` 4.004 s. Re-run a second time with identical results.

### Metrics — counted, not eyeballed

| Metric | Command | Value |
|---|---|---|
| `event/identity.go` | `wc -l` | 85 |
| `event/text.go` | `wc -l` | 63 |
| `event/aggregate.go` | `wc -l` | 113 |
| `Stream.String` | lines 26–31 | 6 |
| `checkFamily` | lines 51–59 | 9 |
| `TryDefine` | lines 36–44 | 9 |
| parameters, every function above | count | <= 2 |
| nesting depth, every function above | count | 1 |
| callers of `checkFamily` | `rg -n "checkFamily" event/*.go \| grep -v _test` | **2** — `aggregate.go:37`, `identity.go:27` |
| kernel imports of a concrete store | `grep -rn "eventmemory\|eventpg\|eventtest" event/*.go \| grep -v _test.go \| wc -l` | **0** |

### GAP-166 — closed, verified by mutation and not by the note

The finding's own input, run here:
`Stream{Family: "orders] admin logged in [stream x", Key: "acme%2Fevil/A-17"}.String()`
now renders `"[stream unnameable]"`, and `everyStreamRendering()` carries it as
*a stream whose family closes the field it is rendered in* (`event/rendering_test.go:94`).

Both arms were reverted in place and both went red:

| Reverted | Test that fired |
|---|---|
| `identity.go:27` back to `checkText(this.Family, MaxNameBytes)` | `TestEveryRenderingNamesAClassAndNeverAValue/a family a store supplied is rendered only when it passed the kernel's own rule` — *a stream whose family closes the field it is rendered in rendered `"[stream orders] admin logged in [stream x]"` rather than a classification* |
| `aggregate.go:37` back to `checkText(family, MaxNameBytes)` | `TestEveryMalformedDeclarationPanicsAndTryDefineReturnsIt` — *a family carrying a bracket, which is what a rendered stream is written inside was accepted* |

The declaration arm is genuinely pinned, in **both** spellings — `declaration_test.go:971`
(`"accounts[account"`) and `:972` (`"accounts]account"`) — and the durable half of the rendering
arm is the property loop at `rendering_test.go:221-226`, which asserts exactly one `[` and one `]`
per rendering rather than the golden phrase. The control that keeps it from passing vacuously is
`rendering_test.go:209-212`: a declared family **must** be named in its own rendering, so a
`Stream.String` that returned `"[stream unnameable]"` unconditionally fails.

Docs verified present, not assumed: `docs/modules/en/event.md:249` and `docs/modules/ru/event.md:254`
carry the family rule including the bracket; `docs/ai/flows/FL-036…:25-26` and its
`event/text.go` row at `:256` name `checkFamily`.

### GAP-201 [medium][immediate] The bracket rule was applied to one of the two store-supplied strings the kernel renders, and `Envelope.Type` still forges a bracketed field in the same line

- **Where:** `event/repo.go:264` (`fmt.Errorf("%w: %q on %s", ErrUnknownType, envelope.Type, envelope.Stream)`),
  `event/repo.go:259` (the type name is held to `checkText`, not to `checkFamily`),
  `event/text.go:44-50` (the comment that justifies a family-only rule),
  `event/text.go:56` (`strings.ContainsAny(value, "[]")`) against `event/identity.go:30`
  (`"[stream " + this.Family + "]"`), `event/rendering_test.go:221` (the bracket property is
  asserted over `everyStreamRendering()` and not over `everyRendering(t)`).
- **What:** measured here, through the real path (`Repo.apply` on an envelope a store returned,
  driven by the `recordingStore` fixture):

  ```
  Envelope{Stream: {accounts.account, acme/A-17}, Type: "accounts.credited] admin logged in [stream x"}
    -> event: the recorded type names no declared fact: "accounts.credited] admin logged in [stream x" on [stream accounts.account]
       open brackets = 2   close brackets = 2
  ```

  This is byte for byte the defect GAP-166 named — a store-supplied string closes the bracketed
  field the renderer opened and writes a second one inside one line — reached through the other
  store-supplied string the kernel renders. `%q` does not help: Go's quoting escapes quotes and
  non-printables and leaves `[` and `]` alone, which is the same reason the round-4 disposition
  itself rejected `%q` for the family. `event/bounds.go:11` says `MaxNameBytes` governs
  *"a family and a wire type name"*, and the rule over those two bytes has now diverged between
  them with nothing saying why. `event/text.go:45` states the premise the family-only rule rests
  on — *"A family is the only identifier the kernel renders unquoted, and it renders inside a
  bracketed field of its own"* — and the premise is doing work that quoting does not do.
  Separately, the delimiter the guard protects (`"[]"`, `text.go:56`) and the delimiter the
  renderer writes (`"[stream " … "]"`, `identity.go:30`) are two independent literals in two
  files with no test tying them: change the frame and the guard silently protects nothing, which
  is `architecture.md`'s single-format-place clause exactly.
- **Why this severity:** medium. The store-supplied wire type name is content §INV-025 explicitly
  permits, and no newline survives `checkText`, so the residual is a forged *field* inside one
  line and not a forged line — the same narrowing the original GAP-166 argued. It is not `low`
  because the round-4 fix chose a mechanism ("no bracket in text the kernel renders into its own
  bracketed line") and then applied it to one of the two inputs that mechanism exists for, and
  because the guard is now coupled to a frame it does not read. Concrete scenario: `eventpg`
  reads a shared ledger table another service writes; a row with
  `type = 'x] [stream billing.invoice'` makes `Load` refuse with `ErrUnknownType`, the consumer
  logs `err.Error()`, and an operator's `\[stream ([^\]]*)\]` pattern attributes the refusal to a
  stream that was never read.
- **Why this timing:** immediate. Phase 2 is the first store that supplies a `Type` the framework
  did not write, so the rule for *what the kernel may render into its own bracketed line* must
  have one answer before there are two stores producing the input. It also changes nothing on the
  store seam, so closing it now costs one predicate and one test row; closing it after `eventpg`
  ships means two answers to one question in a rendering §INV-025 names.
- **Close criteria:**
  - [ ] The bracket rule reaches every store-supplied string the kernel renders into a refusal,
        not the family alone — `event/repo.go:259` holds `envelope.Type` to the same predicate as
        the family, or `event/repo.go:264` stops rendering the type name; and `event/text.go:45`'s
        "the only identifier the kernel renders unquoted" is either true afterwards or restated.
  - [ ] `TestEveryRenderingNamesAClassAndNeverAValue` asserts the one-field property over
        `everyRendering(t)` and not only over `everyStreamRendering()`, and `everyRendering(t)`
        carries a refusal built from an `Envelope` whose `Type` holds `] … [`. Watched fail with
        the new predicate reverted, with the existing family case as its control.
  - [ ] The frame the guard protects and the frame the renderer writes are one place, or one test
        asserts they agree (a rendering built from `Stream{}.String()`'s own frame characters is
        refused by the family rule), so a later change to either fails rather than silently
        un-guards the other.
  - [ ] `event/bounds.go:11`'s *"a family and a wire type name"* still describes one rule over the
        two, or says which of the two carries the extra clause and why.
- **Status:** **closed** by round 6's remediation pass — see the dispositions below.

---

## Round 6 dispositions — remediation pass, 2026-09-08

Cluster `aggregate-crossing-and-log-injection` of `EVENTSOURCE_P1_REMEDIATION.md` § 11, re-opened by
round 5. One finding from this file.

| Gap | Severity | Disposition |
|---|---|---|
| GAP-201 | `[medium][immediate]` | **closed by widening the rule to both declared identifiers, not by dropping the rendering** — `event/text.go:checkFamily` is now `checkName` and governs the family *and* the wire type name at all four doors the two cross |

**Reproduced first, through the real path** — a throwaway in-package probe drove `Repo.Load` over
`recordingStore` with the finding's own envelope, and asked `TryDeclare` the same question:

```
rendered: event: the recorded type names no declared fact: "accounts.credited] admin logged in [stream x" on [stream accounts.account]
open=2 close=2
declare with bracket: accepted, err=<nil>
```

**After:**

```
rendered: event: the recorded type names no declared fact: [stream accounts.account] recorded a type name of 44 bytes that contains a bracket
open=1 close=1
declare with bracket: refused, err=event: the declaration is malformed: a wire type name on the aggregate "probe.family" contains a bracket
```

**Why widen the rule rather than stop rendering the type name.** The second close criterion offers
both. Dropping `%q` from `event/repo.go:264` removes the operator's only clue about *which* fact a
history holds that this declaration cannot read — the refusal would then say `[stream …]` and a byte
count for every unreadable row alike, and `TestNoRefusalRendersAnIdentityAPayloadAKeyOrACursor`'s
control at PLAN.md:4777 exists precisely because §INV-025 permits naming the wire type name. Widening
keeps that and removes the forgery. It also removes the divergence the finding names: `MaxNameBytes`
governs *"a family and a wire type name"* and now one predicate governs both, so `event/bounds.go:11`
is true as written and needed no edit (fourth criterion).

**Both doors, for both names.** A rule at the envelope alone would let a program declare
`orders]evil` and then find its own history unloadable, which is the asymmetry the round-4 fix had
already refused for the family. `checkName` therefore runs at `TryDefine`, `Stream.String`,
`TryDeclare` and `Repo.apply`. The consequence a caller can see is that a wire type name carrying
`[` or `]` — `fmt.Sprintf("%T", Order[int]{})` is the realistic way to produce one — is now
`ErrDeclaration` at `Declare` rather than a stream nothing can load; that is the direction `econv`
asks for, and both module pages state it.

**The frame is one place** (third criterion): `event/text.go` declares `fieldOpen` and `fieldClose`,
`Stream.String` builds its rendering out of them, and `checkName` refuses them. A test derives the
delimiters from a live rendering as well, so a renderer that starts writing a different frame fails
rather than silently un-guarding the guard.

**Left behind, and each was watched fail with the fix reverted:**

| Test | Reverted | Answer |
|---|---|---|
| `TestEveryRenderingNamesAClassAndNeverAValue/a recorded type name is rendered only when it passed the kernel's own rule` | `repo.go` back to `checkText(envelope.Type, MaxNameBytes)` | `a recorded type name that closes the field the stream is rendered in is rendered in "…: \"accounts.credited] admin logged in [stream x\" on [stream accounts.account]", and a refusal names the rule that was broken and never the text that broke it` |
| `TestEveryRenderingNamesAClassAndNeverAValue/no rendering carries a field the renderer did not open` | the same | `… which closes a field nothing opened: everything after it reads as a field of its own` |
| `TestEveryHistoryClassRefusalIsRaisedByTheThingItNames` | the same | `a wire type name that closes the field the stream beside it is rendered in rendered the store's own data in "…"` |
| `TestEveryMalformedDeclarationPanicsAndTryDefineReturnsIt` | `fact.go` back to `checkText(name, MaxNameBytes)` | `a wire type name carrying a bracket, which is what a rendered field is written inside was accepted, so a malformed declaration survives to be discovered at load or append time` |
| `TestEveryRenderingNamesAClassAndNeverAValue/the identifier rule refuses the characters a field is framed with` | the same | `a field is rendered as "[stream accounts.account]" and a wire type name carrying "[" was accepted at the declaration` |
| the same subtest | `Stream.String` rewritten to frame with `<` and `>` while `checkName` keeps `[]` | `a field is rendered as "<stream accounts.account>" and a family carrying "<" was accepted at the declaration, so the rule guards a frame the renderer no longer writes` |
| `TestEveryRenderingNamesAClassAndNeverAValue/a family a store supplied is rendered only when it passed the kernel's own rule` | `identity.go` back to `checkText` | `a stream whose family closes the field it is rendered in rendered "[stream orders] admin logged in [stream x]" rather than a classification` (round 4's arm, still pinned) |

**Neither new assertion can pass vacuously, and each has its control.**
`refusedRecordedType(t, "accounts.retired")` — a legal type name no declaration knows — **must** be
named in its own refusal, so a `Repo.apply` that stopped rendering the type name altogether fails the
control rather than passing the four forged rows: measured, deleting the `%q` verb answers *"a legal
type name no declaration knows is not named in … so an operator cannot tell which fact the history
holds and the cases below are about a name nothing renders"*. The field walk counts the fields it
inspected and fails at zero; over `everyRendering(t)` it walks **541** of them today.

**Docs updated in the same change:** `docs/modules/en/event.md` and `docs/modules/ru/event.md` — the
section is now *A declared identifier: the family and the wire type name* / *Объявленный
идентификатор: семейство и имя типа на проводе*, stating one rule over two names and two doors each,
with *One family names one aggregate* keeping the aggregate half; `[[FL-036]]`'s declaration steps 1
and 3, its `apply` paragraph and its `event/text.go` row; `PLAN.md`'s `Stream.String` contract block,
its exported-symbol row, the `Envelope.Type` and `Envelope.Stream.Family` trust-boundary bullets, S6's
contract clause 1, and the `## Debt` entry for GAP-166.

### Gate, run here

`gofmt -l .` silent · `go build ./...` ok · `go vet ./event/...` ok ·
`go test -race -count=2 ./event/...` green (`event` 6.107 s, `eventmemory` 1.492 s,
`eventtest` 4.027 s) · `make unit` exit 0 · `make check` exit 0, all nine checks ok.
`make api` produces no line for any symbol this change added: `checkName`, `fieldOpen` and
`fieldClose` are unexported and the store seam is untouched, so `event/eventpg`'s zero-diff
obligation is unaffected — a store still constructs `Envelope{Type: …}` from any string it likes and
the kernel, not the constructor, is what refuses one it cannot have declared.

---

## Round 7 — econv-implementation-reviewer (clean context, re-audit of the round-6 remediation cluster) — 2026-09-08

Scope: the log-injection half of cluster `aggregate-crossing-and-log-injection` — **GAP-166** and
the **GAP-201** it re-opened — and every file round 6 touched for them: `event/identity.go`,
`event/text.go`, `event/aggregate.go`, `event/fact.go`, `event/repo.go`, `event/rendering_test.go`.
Nothing was inherited from the disposition note. Each defect was re-derived by constructing the
input the finding named and running it **from outside package `event`**, and each repair was
reverted in place, the suite run, and the file restored from a byte-level backup.

The tree was left byte-identical to how it was found. `md5sum` before the mutations and after the
restore: `event/aggregate.go` `dc59c1e5efb6c86e886c91964f4e2097`, `event/change.go`
`16fe2a4be211940732c77d53ec850198`, `event/identity.go` `bbb6de6fb878df80dd7cceb124b5a530`,
`event/text.go` `e1c18b5c5c91b6afb53576755814cfb1`, `event/fact.go`
`d400976d4395769889aa2085227a4876`, `event/repo.go` `f360915020180a2279242f952b83e05a`,
`event/rendering_test.go` `896fb50889996694c59889939d0508a8`. Three throwaway probe packages were
deleted; `git status --porcelain | grep -c zzaudit` = **0**.

### Gate, run here

`gofmt -l .` silent (exit 0, whole repository) · `go vet ./event/...` exit 0 ·
`go test -race -count=2 ./event/...` green, no flake — `event` 6.030 s / 6.064 s,
`eventmemory` 1.488 s / 1.499 s, `eventtest` 4.009 s / 4.031 s across two independent invocations.

### Metrics — counted, not eyeballed

| Metric | Command | Value |
|---|---|---|
| `event/identity.go` | `wc -l` | 85 |
| `event/text.go` | `wc -l` | 74 |
| `event/repo.go` | `wc -l` | 272 |
| `event/fact.go` | `wc -l` | 221 |
| `Stream.String` | `awk '/func \(this Stream\) String/,/^}/' \| wc -l` | 6 |
| `checkName` | `awk '/^func checkName/,/^}/' \| wc -l` | 9 |
| `Repo.apply` | `awk '/Repo\[S, ID\]\) apply/,/^}/' \| wc -l` | 13 |
| parameters, all four above | count | 0 / 1 / 1 / 2 |
| nesting depth, all four above | count | 1 / 1 / 1 / 1 |
| callers of `checkName` | `rg -n checkName event/*.go \| grep -v _test` | **4** — `aggregate.go:37`, `identity.go:27`, `fact.go:28`, `repo.go:261` |
| kernel imports of a concrete store | `grep -rn "eventmemory\|eventpg\|eventtest" event/*.go \| grep -v _test.go \| wc -l` | **0** |
| global mutable state under `event/*.go` | `grep -n "^var " event/*.go \| grep -v _test` | 3 blocks, all `errors.New` sentinels |
| stale `checkFamily` references outside the artifacts | `grep -rn checkFamily --include=*.md --include=*.go .` | **0** |

### Microkernel — derived here, not inherited

An external package (module-internal, outside `package event`) was written that implements the whole
eight-method `event.Store` — `var _ event.Store = (*store)(nil)` compiled — and constructs from
exported API alone every value the contract makes a store produce:
`Capabilities{Transactions: event.Supported, …}`, `Limits{…}`, `event.NewBacking(identity)`,
`event.NewAuthority(backing, tx)`, `event.Stream{Family, Key}` **including a family carrying `]` and
`[`**, `event.Compose(...)`, `event.Envelope{Stream, Version, Position, Type, Revision, Payload,
RecordedAt}` **including a `Type` carrying `]` and `[`**, `event.Record{…}`, reading
`AppendRequest{Stream, Expected, Records}`, `event.Cursor`, and `event.Failure(outcome, err)` over
all seven exported outcomes. `go run` on it succeeded and drove `Repo.Load` end to end. The round-6
rule is enforced at *use*, never at construction, so nothing a store builds changed shape.
**`event/eventpg` is still writable with zero diffs under `event/`. Microkernel passes.**

### GAP-166 and GAP-201 — closed, verified by construction and by mutation

Measured from outside the package, through the real path:

```
Stream{Family: "orders] admin logged in [stream x", Key: "acme/A-17"}.String()
  -> "[stream unnameable]"                      open=1 close=1
Stream{Family: "accounts.account", Key: "acme/A-17"}.String()
  -> "[stream accounts.account]"                (control: a declared family still names itself)

Repo.Load over a store returning Envelope{Type: "accounts.credited] admin logged in [stream x"}
  -> event: the recorded type names no declared fact: [stream accounts.account] recorded a type
     name of 44 bytes that contains a bracket    open=1 close=1  errors.Is(ErrUnknownType)=true
Repo.Load over Envelope{Type: "accounts.retired"}
  -> event: the recorded type names no declared fact: "accounts.retired" on
     [stream accounts.account]                   (control: a legal unknown name is still named)

TryDefine("orders]evil")            -> event: the declaration is malformed: the stream family
                                       contains a bracket
TryDeclare(a, "orders.placed[v1]")  -> event: the declaration is malformed: a wire type name on
                                       the aggregate "orders.order" contains a bracket
```

Every arm was reverted in place and every one went red:

| Reverted | Test that fired |
|---|---|
| `identity.go:27` back to `checkText(this.Family, MaxNameBytes)` | `TestEveryRenderingNamesAClassAndNeverAValue/a family a store supplied is rendered only when it passed the kernel's own rule` — *a stream whose family closes the field it is rendered in rendered `"[stream orders] admin logged in [stream x]"` rather than a classification* |
| `repo.go:261` back to `checkText(envelope.Type, MaxNameBytes)` | the same test's *a recorded type name …* subtest, its *no rendering carries a field the renderer did not open* subtest, **and** `TestEveryHistoryClassRefusalIsRaisedByTheThingItNames` — three independent assertions, one revert |
| `fact.go:28` back to `checkText(name, MaxNameBytes)` | `TestEveryMalformedDeclarationPanicsAndTryDefineReturnsIt` *(declaration_test.go:1009)* and `…/the identifier rule refuses the characters a field is framed with` |
| `aggregate.go:37` back to `checkText(family, MaxNameBytes)` | `TestEveryMalformedDeclarationPanicsAndTryDefineReturnsIt` *(declaration_test.go:976)* and the same frame subtest |
| `Stream.String` reframed with `<`/`>` while `checkName` keeps `[]` | *a field is rendered as `"<stream accounts.account>"` and a family carrying `"<"` was accepted at the declaration, so the rule guards a frame the renderer no longer writes* — the frame and the guard are genuinely one place |

Nothing was widened. `Repo.apply` runs `checkName` **before** the fact-table lookup, so the `%q` at
`repo.go:266` can only ever render a name that already passed the rule; `strconv.Quote` escapes
non-printables into `\x`/`\u`/`\U` + hex digits, none of which is `[` or `]`, so the quoted route
cannot manufacture a frame either. The controls are real: `rendering_test.go:241-243` requires a
declared family to be named in its own rendering (so an unconditional `[stream unnameable]` fails),
and `rendering_test.go:262-265` requires a legal-but-unknown type name to be named (so dropping the
`%q` verb fails rather than silently passing the four forged rows). `rendering_test.go:276-278`
fails if `fieldOpen`/`fieldClose` stop being one byte each, which is what the byte-wise walk and
`strings.ContainsAny` both assume.

Docs verified present, not assumed: `docs/modules/en/event.md:248-262` and
`docs/modules/ru/event.md:253-267` carry *A declared identifier: the family and the wire type name*
with the bracket clause and both doors; `docs/ai/flows/FL-036…:25`, `:35`, `:88` and its
`event/text.go` row at `:258` name `checkName`; `eventtest.Families`, which
`docs/modules/en/event.md:272` names, exists at `event/eventtest/proxies.go:69`.

### GAP-205 [low][deferred] The bracket rule reaches the two identifiers a store supplies and not the two the kernel renders from a program, and `everyRendering(t)` is a curated list rather than the package's renderings — so the field walk cannot see either

- **Where:** `event/fact.go:26` (`fmt.Errorf("%w: a fact is declared on an aggregate and %q names none", ErrDeclaration, name)` — `name` is rendered **before** `checkName` runs at `:28`), `event/fact.go:147` (`… reads %s …`, where `carried.read.typeName` is `reflect.TypeFor[V]().String()` from `event/codec.go:66`), against `event/rendering_test.go:43-80` (`everyRendering(t)`) and `event/rendering_test.go:275-304` (the field walk).
- **What:** measured here through the exported API:

  ```
  TryDeclare[state, string, box[int]](nil, "orders.placed] admin logged in [stream x", …)
    -> event: the declaration is malformed: a fact is declared on an aggregate and
       "orders.placed] admin logged in [stream x" names none
       — a "]" that closes a field nothing opened, then a "[" that opens one nothing closes

  Fact[…, box[int]].RoundTrip("not a box")
    -> event: this sample cannot prove what a round trip claims for it: crud: bad request:
       revision 1 of "orders.placed" reads main.box[int] and the sample is a string
       — "[int]" is a bracketed field the renderer never opened
  ```

  Both are renderings of the mechanism round 6 chose — *no text the kernel renders into its own
  bracketed line may carry the frame characters* — reached by the two inputs that mechanism was not
  applied to. The first is a wire type name rendered at the one door that precedes `checkName`; the
  second is a Go type name the kernel derives itself, and a generic reader type is the ordinary way
  to produce one. Neither is caught by the `no rendering carries a field the renderer did not open`
  walk, because that walk runs over `everyRendering(t)` — a hand-written list of 24 sentinels, 512
  enumerated `Outcome`/`Support` values, the door refusals, seven stream renderings and four recorded
  type names — and not over the package's renderings. Round 6's disposition cites *"the field walk
  counts the fields it inspected and fails at zero; over `everyRendering(t)` it walks 541 of them
  today"* as evidence for the mechanism; 541 is the size of that list, not of the package's `fmt.Errorf`
  set, which is **83** call sites under `event/*.go` excluding tests.
- **Why this severity:** low. Both strings are program-authored — a wire name the program passed with
  a nil aggregate (a wiring bug that panics at init in the `Declare` spelling) and a Go type the
  program declared — so neither is reachable by a store, a request or any party outside the binary.
  `main.box[int]` is balanced, so it adds a spurious field rather than splitting the line. It is the
  same narrowing GAP-166 was: a forged *field*, not a forged line.
- **Why this timing:** deferred. Nothing on the store seam and nothing phase 2 depends on; `eventpg`
  supplies neither string. It is one predicate move plus two rows in `everyRendering(t)`, and S6
  already owns §INV-011's source check over the same file.
- **Close criteria:**
  - [ ] `event/fact.go:26` either renders the name only after `checkName` has passed it, or stops
        rendering it — and `event/fact.go:147`'s `typeName` is held to the same rule or rendered
        without the frame characters.
  - [ ] `everyRendering(t)` carries a refusal built from `TryDeclare(nil, "…] … […", …)` and one
        from `Fact.RoundTrip` over a generic reader type, so the existing field walk reaches both.
        Watched fail with the new predicate reverted, with the family case as its control.
  - [ ] Either `everyRendering(t)` is derived from the package's renderings rather than listed, or
        the list states in one line which renderings it deliberately omits and why.
- **Status:** open

### GAP-206 [low][deferred] `[stream unnameable]` is a legal family, so the refusal rendering and a family literally named `unnameable` are one string

- **Where:** `event/identity.go:27-31` against `event/text.go:51-59`.
- **What:** `checkName("unnameable")` returns `""`, so `Stream{Family: "unnameable"}.String()` renders
  `"[stream unnameable]"` — byte for byte what `Stream{Family: "orders\n\tFAKE"}.String()` renders.
  Measured here; `TryDefine[account]("unnameable", …)` is also accepted, so the collision is reachable
  from a declaration as well as from an envelope.
- **Why this severity:** low. An operator alerting on `[stream unnameable]` — the only signal that a
  store handed the kernel a family no declaration could have produced — cannot distinguish it from a
  stream of an aggregate whose author chose that family, and a store that wants the alert suppressed
  can supply exactly that family. No fold, no append and no read changes: the refusal is unaffected,
  only its rendering is ambiguous. The rest of the rendering vocabulary is explicitly held to the
  opposite rule by `TestEveryRenderingNamesAClassAndNeverAValue/a rendering says which kind of thing
  it is and which one of them`, which compares kinds against kinds and does not ask this question.
- **Why this timing:** deferred. It changes one string and one test row, blocks nothing in phase 2,
  and the store seam is untouched. The clean fix is a refusal phrase the success path cannot produce
  — the success path is `fieldOpen + "stream " + family + fieldClose`, so anything without the space
  after `stream` is unreachable from it.
- **Close criteria:**
  - [ ] The unnameable rendering is a string `fieldOpen + "stream " + <any legal family> + fieldClose`
        cannot equal, or the reason the collision is acceptable is written beside the reason a bracket
        is not.
  - [ ] `TestEveryRenderingNamesAClassAndNeverAValue` asserts that no legal family renders the same
        string as an illegal one, with `Stream{Family: "accounts.account"}` as its control.
- **Status:** open

### Round 7 verdict

**GAP-166 and GAP-201 are genuinely closed**, verified by constructing the inputs the findings named
from outside the package and by reverting all five arms of the repair and watching six distinct
tests fire. The fix introduced no new accept, no new nil path and no weakened assertion: the rule
was widened rather than relaxed, the frame is one place and a test proves it, and the two controls
that keep the new assertions from passing vacuously were each driven red by deleting the thing they
control. Zero open `[critical][immediate]` or `[high][immediate]` from this file. The two findings
above are both `[low][deferred]` and are the next two narrowings of the same mechanism, not holes
in it.
