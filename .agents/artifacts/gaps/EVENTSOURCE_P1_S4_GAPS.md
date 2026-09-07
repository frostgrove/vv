# EVENTSOURCE_P1 — implementation S4 — GAPS

## Round 1 — econv-impl-reviewer (clean context) — 2026-09-07

Reviewed against the **code**, not the plan's prose. Read in full:
`.agents/artifacts/plans/EVENTSOURCE_P1_PLAN.md` (§ Carried gaps rows 4–5 and GAP-53,
§ Contracts `event/token.go`, `event/binding.go`, `event/repo.go`+`marker.go`,
`event/reader.go`, `event/eventmemory/`, § S4, § S5, § S6),
`.agents/artifacts/usecases/EVENTSOURCE_P1_USECASES.md` (§UC-030, UC-031, UC-032, UC-033,
UC-034, UC-035, UC-036, UC-037, UC-038, UC-039, UC-046, UC-049, UC-053, UC-059;
INV-004, INV-006, INV-011, INV-015, INV-016, INV-022, INV-025, INV-031, INV-038, INV-039,
INV-040, INV-043, INV-044, INV-045), `CLAUDE.md`,
`event/{repo,binding,reader,token,marker,store,errors,change,fact,chain,codec,outcome,`
`aggregate,seal,identity,backing,authority,bounds,text}.go`, the S4 test files
(`repo_test.go`, `binding_test.go`, `transaction_test.go`, `reader_test.go`,
`outcomes_test.go`, `status_test.go`, `recordingstore_test.go`), and
`~/.claude/skills/econv/references/{microkernel,architecture,building-blocks,data-integrity,universality,readability,restrictions,gaps}.md`.

Every number below was produced by running in this worktree. The four adversarial probes ran
from a scratch test file (`event/zzz_review_scratch_test.go`) and an out-of-tree module at
`/tmp/evprobe` with a `replace` onto this checkout; the scratch file was **deleted** and
`git status --porcelain event/` is byte-identical to what it was on entry — no repository file
was edited by this review.

---

### Checkpoint S4 — phase 4, re-run here

```
$ go build ./...                                   # clean
$ go vet ./event/...                               # clean
$ gofmt -l .                                       # silent, whole tree
$ go test -race -count=1 ./event/...
ok  github.com/frostgrove/vv/event           1.256s
ok  github.com/frostgrove/vv/event/eventmemory 1.028s
$ go test -race -count=1 ./event/...                # second run, no flake
ok  github.com/frostgrove/vv/event           1.261s
ok  github.com/frostgrove/vv/event/eventmemory 1.029s
$ go test -race -count=1 -v -run '…the six checkpoint names…' ./event/
--- PASS: TestBothDoorsCheckTheStore, TestAFreshStreamLoadsAsZero,
           TestAppendRefusesInItsStatedOrder, TestAForgedTokenIsRefusedBeforeAnyStatement,
           TestAnOverLongPageIsRefused, TestWithinComposesForTwoBackings           (6/6)
$ make unit                                        # every module, no FAIL
$ make check
check-deps: ok  check-tiers: ok  check-utils: ok  check-triplets: ok  check-todo: ok
check-replaces: ok  check-tidy: ok  check-otel-schema: ok  check-workspace: ok
```

**The checkpoint output pasted into the plan is real.** The coverage claim is real too:
`binding.go` `Open`/`Bind`/`hold`/`admit`/`admitLimits`/`admitCapabilities` 100 %,
`marker.go` 100 %, `reader.go` 100 % (all six), `token.go` 100 % (all eight),
`repo.go` `Load`/`Append`/`transaction`/`checkKey`/`replay`/`nothing`/`checkPage` 100 %,
and the four the plan hands to the tests phase are exactly the four below 100 %:
`Within` 62.5 %, `Authority` 75 %, `streamOf` 83.3 %, `records` 83.3 %, `apply` 87.5 %.

### Architecture metrics — counted, not eyeballed

| Metric | Value | Threshold |
|---|---|---|
| S4 file lengths | `repo.go` 261, `binding.go` 134, `reader.go` 77, `token.go` 47, `marker.go` 40 | all well inside |
| Longest function | `Append` 36 lines; `admitLimits` 34; `replay` 22 | none near 40 |
| Max brace depth | `repo.go` 4 (= 2 nested blocks in `replay`), `binding.go`/`reader.go`/`marker.go` 3, `token.go` 1 | ok |
| Exported symbols added by S4 | 21: `At`+2 methods, `Commit`+6, `Binding`, `Open`, `Bind`, `Repo`+4, `ReadOnly`, `Read`, `Reader`+3 | matches the plan's contract block exactly, no silent extra surface |
| Import fan-out per S4 file | `repo.go` 2 (`context`,`fmt`), `binding.go` 2 (`fmt`,`sync`), `reader.go` 2, `marker.go` 1, `token.go` 0 | zero third-party, zero cross-subsystem |
| `event` package first-party deps | `crud`, `errs` only (`errors.go`, `backing.go`, `authority.go`) | matches [[D-116]]/S6's planned allow-list |
| Fakes needed per object | `Repo` 1 (a `Store`), `Reader` 1 (a `Log`), `Binding` 1 (a `Store`); the aggregate is a real value | ≤ 2 ✓ |
| Global mutable state in `event` non-test | 0 (the three `var` blocks are sentinels and `reflect.Type` constants) | ✓ |
| Import cycles | none (`go build` is the proof; `eventmemory` imports `event`, never the reverse) | ✓ |

### Microkernel purity — the command and the result

```
$ rg -n 'eventmemory|eventpg|eventtest' event/{repo,binding,reader,token,marker}.go | wc -l
0
$ rg -n 'case \*[a-z]|\.\(\*event|switch .*\.\(type\)' event/{repo,binding,reader,token,marker}.go | wc -l
0
$ go list -f '{{join .Imports "\n"}}' ./event/eventmemory | rg frostgrove
github.com/frostgrove/vv/event
```

**Phase 2's `eventpg` costs zero diffs to `event/`, and `eventmemory` is the executed proof.**
It is a separate package and therefore reaches nothing unexported, yet implements all eight
`Store` methods: `Backing` via `NewBacking`, `Authority` via `NewAuthority`, a classified error
via `Failure(Outcome, error)`, and `Stream`/`Key`/`Version`/`Position`/`Cursor`/`Envelope`/
`Record`/`AppendRequest`/`Limits`/`Capabilities` by conversion or composite literal.
`ResidentPage` is exported so a store derives the same page bound the kernel re-applies at
`binding.go:103`. Registration is `Open(store)` / `Read(log, cursor)` — two doors, both running
`admit`; the failure policy is `ErrWrongStore` at both; the compatibility note is
`Capabilities`' tri-state. The kernel works with zero extensions registered (`Open(nil)` →
`Bind` → `ErrWrongStore`, `binding_test.go:55-63`). No speculative extension point was added.

### Universality — every literal traced

```
$ rg -no '"[^"]*"' event/{repo,binding,reader,token,marker}.go | sed 's/.*://' | sort -u
```
33 literals, all of them either an import path, a `Limits`/`Capabilities` **field name** used to
name the bound in a refusal message, or refusal prose. Zero domain values.

```
$ rg -no '\b[0-9]{2,}\b' event/{repo,binding,reader,token,marker}.go
(no output)
```
**Zero numeric literals of two digits or more in the whole section.** Every bound the kernel
applies is read from `store.Limits()` retained at the door (`repo.go:160,167,173,206,227,257`)
or from `event/bounds.go`'s named ceilings (S1). `checkPage`'s `after+Version(offset)+1` is the
contract's own densification rule, not a shape assumption. No fixture path, no `testdata`
reference and no example-aggregate name appears in any non-test file
(`rg -n "testdata|_test|fixture" event/*.go | rg -v "_test.go:"` → empty). **No universality
finding.**

### Purity, determinism and error discipline — verified

- `Load`'s rehydration path is `replay` → `checkPage` → `apply` → `applierOf` closure →
  `chain.links[r-1].decode` → `decodeWith` → the codec. `rg 'log\.|time\.|rand\.|os\.'` over the
  five S4 files returns **nothing** but `log.Backing()`/`log.Limits()`/`log.Capabilities()` on
  the `Log` **parameter** and `this.log.ReadAll`. No clock, no randomness, no telemetry, no I/O
  the store did not perform. Upcasting is `carry`→`upcastTo`, likewise pure.
- Iteration order is deterministic everywhere: `admitLimits`/`admitCapabilities` walk slice
  literals, `records`/`replay`/`checkPage` walk the caller's slice in order. The only map read is
  `this.aggregate.facts[envelope.Type]`, a lookup.
- `rg 'err.Error\(\) ==|strings.Contains\(err'` over `event/*.go` non-test → **empty**. Nothing
  compares an error by string; `refuse` reads only the `*failure` classification found through
  `findAs`, per §INV-045.
- No refusal in S4 carries a key, a payload, a version, a position or a cursor. `repo.go:233,251`
  render a store's `Stream` through `Stream.String()`, which is guarded by `checkText`;
  `repo.go:255` renders `envelope.Type`, which §INV-025 explicitly permits ("the wire type name")
  and which `apply` has already validated to ≤128 bytes of control-free UTF-8.
- `rg 'type: ignore|nolint|t.Skip|TODO|FIXME|HACK' event/` → **empty**. No shortcut was used to
  reach green.

### Contract conformance — plan vs code, both directions

Every signature in the plan's `### Contracts` block is implemented verbatim
(`Open`, `Bind`, `Repo.{Load,Append,Within,Authority}`, `At.{Stream,Version}`,
`Commit.{Empty,Stream,First,Last,Count,Authority}`, `ReadOnly`, `Read`,
`Reader.{Next,Events,Cursor}`), with **no extra exported symbol**. `Append`'s six steps sit in
the plan's order including S4's own correction (batch count first: `repo.go:167` precedes the
per-record loop), the empty-append short circuit sits between step 1 and step 2
(`repo.go:63-68`), and the plan's two S4 widenings are present (`b == nil` → `ErrWrongStore`,
`a == nil` → `ErrDeclaration`, `binding.go:31-36`). `Within` returns the context it was given on
every refusal (`repo.go:107,111,114`). `ReadOnly(nil)` returns a nil `Log` (`reader.go:16-18`).
`seal.go`'s enumeration carries `Bind` as its sixth row and `errors.go:171` carries `tooMany`.
The two `[x]` claims I could not verify from source alone — the seventeen mutations and their
byte-identical restores — are consistent with `git status` (the five files are untracked and
unmodified since 14:52).

### Naive-contract probes I ran, and what they showed

| Probe | Result |
|---|---|
| `json.Marshal(At[acc]{})` / `Commit{}` / `Authority{}` | `{}`, `{}`, **error**. See GAP-4 |
| Empty `Append` of a foreign-backing token | succeeds, receipt empty, token returned unchanged — §UC-031's stated cost, correct |
| `Authority(ctx)` vs `Load(ctx)` on a `Within`-marked context that now carries a second transaction | `Authority` → valid, nil error; `Load` → `ErrTransactionMismatch`. See GAP-2 |
| `Load`/`Append` under an already-cancelled context | `Load` → bare `context.Canceled`; `Append(At{})` → `ErrKey` with **0** store calls. Correct: §D.5 step 1 outranks the store |
| A page whose positions descend **across** a page boundary in `Reader` | admitted. **Not a defect** — §UC-037 makes a later page's positions legitimately lower for a store with `MonotoneVisibility: Unsupported`, and §UC-037 forbids the kernel any comparison of cursors. `reader.go`'s comment scopes the rule to "a page", singular, and is right |
| A forged `At[S]{stream:…}` whose `Family` is not this repo's | reaches `decidedFor`, which forces every change's stream to equal the token's; a `Change[S]` can only be minted by `Fact.New` from a declared aggregate, so the family cannot be forged past step 2 with anything to write. Correct |
| Two aggregates over one family through two `*Binding` values | admitted — §INV-039 states this escape is **deliberately** admitted and `TestOneFamilyNamesOneAggregate:113` pins it. Not a defect |

---

### GAP-1 [medium][immediate] `Append`'s public GoDoc says every refusal means nothing was written

- **Where:** `event/repo.go:60-61` (the doc comment) against `event/repo.go:91-93` (the store
  call).
- **What:** The GoDoc on the exported `Repo.Append` ends: *"Every refusal returns the token that
  went in, **because nothing reached the backing** and the caller's next append is through the
  same token."* The clause is true of the six pre-store steps it follows, and false of the branch
  at `:91-93`, which returns `at` **after** the store's `Append` has been issued. §INV-040's table
  gives each of the eight outcome rows its own reason precisely because they differ:
  `ErrConflict` is "the store's expected-version check refused it", `ErrUncertain` is "unchanged
  in value, untrustworthy in meaning". The comment collapses all eight into the one reason that
  is wrong for four of them.
- **Why this severity:** The returned *value* is correct in every row, so there is no
  behavioural defect. The harm is a consumer reading `go doc event.Repo.Append` and concluding
  that `err != nil` ⇒ nothing was written — which is the exact inference §UC-034 exists to
  forbid, and the reason `ErrUncertain` is a sentinel of its own. Concretely: a handler that
  gets `ErrUncertain` from a Postgres store whose connection died at commit, and reports "your
  payment was not recorded" to the user on the strength of this sentence, is telling the user
  something the framework does not know. The recovery path itself stays safe (a retry through
  the same token is admitted only if the first attempt did not land), which is why this is
  `medium` and not `high`.
- **Why this timing:** It is a public contract comment on the method every other section and the
  conformance suite document against, and S6 writes the module page and the flow from these same
  sentences. Fixing it after `docs/modules/event.md` has copied it is a multi-document change.
- **Close criteria:**
  - [x] The sentence at `repo.go:60-61` no longer asserts "nothing reached the backing" for
        refusals raised after `this.store.Append` is called.
  - [x] The comment either names the pre-store steps as the scope of that reason, or points at
        §INV-040's per-row table for what an outcome says about the token.
  - [x] `go doc event.Repo.Append` contains no sentence a caller could read as "an error means
        the append did not land".
- **Status:** closed in phase 5. The reason is scoped to the six pre-store steps and the two
  outcomes that mean otherwise are named on the method itself; `go doc` re-read afterwards.

### GAP-2 [medium][immediate] `Repo.Authority` and `Repo.Load`/`Append` give opposite answers about one context

- **Where:** `event/repo.go:122-128` (`Authority`) against `event/repo.go:137-146`
  (`transaction`, which `Load` and `Append` use).
- **What:** `transaction` asks the store and **then** compares the answer with the innermost
  marker for this backing, refusing with `ErrTransactionMismatch` when they differ.
  `Authority` asks the store and returns the answer unconditionally — it never reads the marker.
  Run in this worktree:

  ```
  Authority on a crossed context: valid=true  err=<nil>
  Load on the same context:       event: the bound transaction is not the one this context
                                  was marked for   (mismatch=true)
  ```

  (context obtained from `Within`, then a second `recordingTx` for the same store bound into it —
  §UC-049's exact precondition.)
- **Why this severity:** §UC-030's stated observable is *"`commit.Authority()` and
  `repo.Authority(ctx)` answer the same value"*, and §UC-030 is the use case whose whole point is
  that an application can **check** its cross-subsystem atomicity claim. On §UC-049's context the
  check answers "yes, one transaction" from the one door that has already decided the answer is
  no: an application that calls `Authority` on both subsystems, compares `Same`, logs "atomic"
  and only then appends gets a true-then-false pair. Nothing is written wrongly — the append is
  refused — so the damage is a wrong assertion in the application's own audit trail rather than a
  wrong write, which is why this is `medium`. Two doors of one object answering opposite things
  about one input is the shape `building-blocks.md` calls a leaky contract.
- **Why this timing:** `Authority`'s contract is what §UC-030, the module page and S5's
  `TestRepoAuthorityIsTheComparisonTwoSubsystemsUse` are all written against, and S5 is about to
  pin whichever answer stands. Deciding after the conformance suite has encoded one of them is a
  suite rewrite. The plan's carried-gaps row 5 (line 384) describes `Authority` without
  mentioning the marker at all, so plan and code are not in conflict — but neither states the
  divergence, and one of them must.
- **Close criteria:**
  - [x] `Authority(ctx)` either performs the same marker comparison `transaction` does, or the
        plan and the doc comment state explicitly that it deliberately does not and why an
        application may still use it for §UC-030's proof.
  - [x] A test drives §UC-049's context through `Authority` and `Load` in one case and asserts
        the chosen relationship between the two answers.
  - [x] §UC-030's "answer the same value" sentence is checked against whichever answer stands.
- **Status:** closed in phase 5. The divergence stands and is now stated: `Authority` reports
  what is bound and never whether an append may be written through it, on the method's own
  comment and in the plan's S4 block.
  `TestRepoAuthorityIsTheComparisonTwoSubsystemsUse` drives §UC-049's crossed context through
  both doors in one case, and §UC-030's equality is asserted over the precondition it is stated
  for — one transaction bound, `commit.Authority()` `Same` as `repo.Authority(ctx)`.

### GAP-3 [medium][deferred] `admitLimits` / `admitCapabilities` mirror the structs by hand, which is the default §INV-043 says cannot happen

- **Where:** `event/binding.go:85-95` (five hand-written `Limits` rows) and
  `event/binding.go:120-128` (four hand-written `Capabilities` rows) against
  `event/store.go:32-49`.
- **What:** Both admission checks iterate a literal table of field names that must be kept in
  sync with the struct definitions by hand. §INV-043 states the tri-state's value as a
  generalisation — *"a field added to `Capabilities` after phase 1 cannot silently default to
  'skip that section'"* — but a field added to `Capabilities` and not added to
  `binding.go:120-128` **does** default silently: its zero is `Unstated`, `admitCapabilities`
  never looks at it, and every store is admitted with it unstated. The same holds for a sixth
  `Limits` field, whose zero would then be an unenforced bound.
- **Why this severity:** No phase-1 input reaches it — the tables and the structs agree today,
  and `store_test.go:121-138`'s `declaredValues` table fails on a new field, so the drift cannot
  land completely silently. It is a DRY/keep-in-sync defect on the mechanism a stated invariant
  claims generalises, not a present wrong answer.
- **Why this timing:** Module-internal; no external contract changes and nothing in S5 or S6
  depends on which way it is written. Phase 2 is the first realistic occasion for a new field.
- **Close criteria:**
  - [ ] `admitCapabilities` derives its field list from `Capabilities` rather than restating it
        (a `reflect` walk asserting every `Support`-typed field is `stated()`), **or** a test
        asserts that the number of rows in the table equals `reflect.TypeFor[Capabilities]().NumField()`
        and likewise for `Limits`.
  - [ ] A case adds a field in a fixture type and shows the check fails rather than passing.
- **Status:** open

### GAP-4 [medium][deferred] `At[S]` and `Commit` marshal to `{}` in silence while `Authority` refuses

- **Where:** `event/token.go:9-13`, `event/token.go:29-35` against `event/authority.go:46`.
- **What:** Run from an out-of-tree module against this checkout:

  ```
  At:        {}  err=<nil>
  Commit:    {}  err=<nil>
  Authority:      err=json: error calling MarshalJSON for type *event.Authority:
                  event: an append authority is the transaction it names, and a
                  transaction does not survive a wire
  wrapped:   {"token":{}}  err=<nil>
  json.Unmarshal(`{"stream":{...},"version":9}`, &at)  ->  <nil>  [stream unnameable]  0
  ```

  §INV-015 is satisfied literally — there is no exported field and no unmarshaller — but the
  *shape* that makes `Authority` safe to hand to a caller (a loud refusal at the wire) is not
  applied to the two values a caller is far more likely to try to put on a wire.
- **Why this severity:** It fails closed. A token that made a JSON round trip comes back as the
  zero token and `Append` refuses it with `ErrKey` at §D.5 step 1, before any store call — no
  wrong write is possible. The cost is DX: the refusal a developer sees is *"the identity does
  not render a legal stream key"* (rendered 400 by `porthttp`) for a mistake that is actually
  "an at-token is not transportable", and the empty `{"token":{}}` in a response body gives no
  hint at all at the point where the mistake was made. `Authority` already carries the exact
  sentence that would have said so.
- **Why this timing:** Additive — a `MarshalJSON` on `At[S]` and `Commit` can be added later
  without breaking any caller that is not already relying on `{}`, which nothing sensibly is. No
  section depends on the current behaviour.
- **Close criteria:**
  - [ ] `json.Marshal` of an `At[S]` and of a `Commit` either returns an error naming why a token
        is not transportable, or the plan records the decision to leave them silently empty with
        the reason `Authority` was treated differently.
  - [ ] A case drives `json.Marshal` of all three values and asserts the chosen behaviour.
- **Status:** open

### GAP-5 [low][deferred] `replay`'s page loop has no `ctx.Err()` guard

- **Where:** `event/repo.go:192-209`.
- **What:** The loop terminates only on a short page or a store error. Its progress is strict —
  `checkPage` forces every page to begin at `after + 1` and `at` advances by `len(page)` — so it
  cannot spin on one page; but a store that ignores the context and keeps answering full,
  correctly-versioned pages holds the request goroutine inside the kernel until `Version`
  overflows. Everywhere else this kernel treats a store's answers as untrusted
  (`store.go:82-86`, `binding.go:59-65`, `repo.go:217-225`); here it trusts the store to honour
  a deadline.
- **Why this severity:** It needs a store that both ignores `ctx` and lies about the end of a
  stream. `eventmemory` checks `ctx.Err()` on every `ReadStream`, and any real driver does. A
  `ctx.Err()` check at the top of the loop body is one line and costs nothing measurable.
- **Why this timing:** A function body, no contract change, no effect on S5 or S6.
- **Close criteria:**
  - [ ] `replay` checks `ctx.Err()` once per page and returns `this.nothing(ctx.Err())`, **or**
        the plan records that the store alone owns cancellation on this path.
  - [ ] If the check is added, a fixture store that ignores `ctx` is driven under an expired
        deadline and the load is asserted to return rather than run away.
- **Status:** open

### GAP-6 [low][deferred] Comment weight, and one comment that is a second copy of the code it sits on

- **Where:** `event/repo.go:45-59` primarily; the density figure covers all five S4 files.
- **What:** Measured comment lines / total lines, non-test files only:

  | package | ratio |
  |---|---|
  | `event/` | **24 %** (615 / 2537) |
  | `tenancy/` | 14 % |
  | `storage/` | 3 % |
  | `jobs/`, `cache/`, `port/`, `crud/`, `auth/` | 0 % |

  S4's own files: `marker.go` 37 %, `token.go` 34 %, `repo.go` 24 %, `reader.go` 24 %,
  `binding.go` 13 %. `CLAUDE.md` gates a comment on "a genuinely complex function of roughly 40+
  lines"; the longest function in the section is `Append` at **36** lines and the median is 13.
  Most of these comments earn their place under the escape hatch ("an invariant the code cannot
  make visible") — `marker.go:5-18`'s resolve-by-backing argument and `repo.go:185-188`'s
  short-page decision are not recoverable from the source. **One does not**: the numbered
  six-step list at `repo.go:45-59` restates the six code blocks below it line for line, which
  makes it a second copy of an order that must now be kept in sync with the code by hand — and
  it is already out of sync, which is GAP-1.
- **Why this severity:** Style, plus one concrete duplication. No behaviour.
- **Why this timing:** Cosmetic; the `event/` house voice was set in S1–S3 and S4 is consistent
  with it, so changing it is a package-wide decision rather than a section one.
- **Close criteria:**
  - [ ] `repo.go:45-59` no longer enumerates the six steps a reader can count in the function
        body, or the plan records that §D.5's order is deliberately restated at the call site and
        names what keeps the two in sync.
  - [ ] The `event/` comment convention is either recorded as a deliberate departure from
        `CLAUDE.md`'s 40-line gate (in the plan or an ADR S6 writes) or brought back inside it.
- **Status:** open

---

### What I looked for and did not find

No `[critical]` and no `[high]` finding. Specifically checked and clean:

- **Universality.** 33 string literals, all import paths / field names / refusal prose; **zero**
  numeric literals of two digits or more; zero fixture or `testdata` references from non-test
  code; nothing fitted to the `account` example, which appears only in `_test.go`.
- **Microkernel.** Zero kernel imports of, and zero kernel branches on, any concrete store.
  `eventpg` costs zero diffs to `event/`; `eventmemory` already proves it from outside the
  package.
- **Token forgery.** `At[S]{}` is refused at §D.5 step 1 with **0** calls of any of the eight
  store methods (`TestAForgedTokenIsRefusedBeforeAnyStatement`, re-run here); a hand-built token
  with a legal key is refused at step 5 because `crud.SameDataSource(nil, nil)` is `false`
  (`crud/executor.go:563`), so an invalid backing genuinely matches nothing including another
  invalid one. No expected version can be omitted, defaulted or chosen.
- **Partial rehydration.** Every error path in `replay` goes through `nothing()` (zero state,
  version 0) and `Load` additionally returns its own `none` and `At[S]{}` at `repo.go:39-41`.
  A fold's panic unwinds past both, so §INV-006 holds by construction there too.
- **Wrong-aggregate writes.** `decidedFor` compares every change's stream with the token's
  before anything else; a `Change[S]` is mintable only by `Fact.New`, which renders the stream
  from the declaration.
- **Conflict vs retryable.** `ErrConflict` and `ErrUncertain` are built with a `nil` wrap
  (`errors.go:228,232`), so no store cause can promote either into `crud.ErrUnavailable`.
- **Concurrency.** `Repo` is immutable after `Bind`; `Binding.families` is written only under
  `hold`'s mutex; `Aggregate.facts` is frozen by the `seal()` `Bind` performs, with a
  mutex-mediated happens-before; `marker` values are immutable and chained, never mutated;
  `Reader` is the one documented single-goroutine value. `-race` is green twice.
- **Integrity.** No HTTP/model/queue call anywhere near the store call. The transaction seam
  matches [[D-118]] exactly: an ambient transaction is joined (`transaction` returns the store's
  authority and proceeds), a non-transaction ambient executor refuses before any statement, and
  nothing bound runs on the store's own autocommit. `Within` opens nothing and rolls back
  nothing.
