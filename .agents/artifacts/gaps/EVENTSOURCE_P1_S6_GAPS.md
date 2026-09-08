# EVENTSOURCE_P1 — implementation S6 — GAPS

## Round 1 — econv-impl-reviewer (clean context) — 2026-09-07

Reviewed against the **code**, not the plan's prose. Read in full: `CLAUDE.md`,
`.agents/artifacts/plans/EVENTSOURCE_P1_PLAN.md` (§ S6, § Microkernel classification,
§ Architecture metrics, § Debt), `.agents/artifacts/usecases/EVENTSOURCE_P1_USECASES.md`
(§INV-011, §INV-013, §INV-019, §INV-027, §INV-036),
`/home/user/.claude/skills/econv/references/{microkernel,architecture,building-blocks,
data-integrity,universality,readability,restrictions,gaps}.md`, and every file S6 touched:
`scripts/{checks.sh,event_test.go,extensions_test.go,tenancy_test.go,docs_test.go}`,
`event/{mutablestate,refusalmessages,transactioncontrol,store,refusal}_test.go`,
`docs/ai/decisions/D-121…D-125`, `docs/ai/flows/FL-036`,
`docs/ai/usecases/modules/event/UC-032`, `docs/modules/{en,ru}/{event,eventmemory,eventtest}.md`,
the two roadmaps, and the three `event/…` sections of `docs/api/surface.md`.

### Checkpoint S6 — re-executed by the reviewer, not read

| Command | Result |
|---|---|
| `go test -list '^(…six scripts names…)$' ./scripts/ \| grep -c '^Test'` | **6** ✓ |
| `go test -list '^(…three event names…)$' ./event/ \| grep -c '^Test'` | **3** ✓ |
| `go test -count=1 ./scripts/...` | `ok github.com/frostgrove/vv/scripts 3.054s` |
| `go test -race -count=1 ./event/...` | `ok event 1.365s` · `ok eventmemory 1.046s` · `ok eventtest 2.548s` |
| `make check` | `check-deps/tiers/utils/triplets/todo/replaces/tidy/otel-schema/workspace: ok` (nine) |
| `make unit` | every module, **no FAIL** — including `utils/vvdb` and `utils/vvdb/dbpgx`, so the plan's "R10's two pool failures are green too" is real |
| `make vet` | every module, no diagnostics |
| `gofmt -l .` | silent |
| `make api` (against a `/tmp` copy, then restored) | **byte-identical** — `docs/api/surface.md` is already the regenerated file |
| `git --no-pager diff --stat docs/api/surface.md` | `96 insertions(+), 2 deletions(-)` — matches the plan's paste exactly |
| `make integration` | `ok test/bridge · test/codegen · test/dsn · test/integration 19.359s · test/portmount · test/versionstore` |

**The checkpoint output pasted into the plan is real.** The surface diff's two deletions are
`jobs.ScheduleOverlap` / `AllowOverlap`, which the plan names as not-S6 and which the tree
confirms (`rg ScheduleOverlap jobs/` → nothing). `event`'s 26 types + 3 functions + 6 constant
lines are purely additive.

### Contract conformance — plan vs code, both directions

| Plan promised | Shipped | Verdict |
|---|---|---|
| `scripts/checks.sh` — `event` added to `SUBSYSTEMS`, "that is the whole edit" | one-word diff, `SUBSYSTEMS=(… tenancy event)` | ✓ exact |
| `scripts/extensions_test.go` — `listed`, `firstPartyDependencies`, `under` moved here; `under` gains a `prefix` parameter | 44 lines, exactly those three, `under(prefix, path)` | ✓ exact |
| `scripts/tenancy_test.go` — helpers removed, test names unchanged | three names unchanged; cited by `D-116:115,119`, `FL-033:214`, `docs/modules/{en,ru}/tenancy.md:46` and still resolving (`TestEveryTestNameTheDocsCiteExists` green) | ✓ exact |
| `scripts/event_test.go` — three tests, `< 50` package floor, `crud`+`errs` allowed explicitly, `go/ast` not grep | all present; floor trips at 50 and the tree lists **54** | ✓ |
| `event/{mutablestate,refusalmessages,transactioncontrol}_test.go` | all three, each with a fixture-driven falsification subtest | ✓ |
| `docs_test.go` — the `exactly.?once` arm, scoped and windowed | `TestNoDocPromisesExactlyOnceDelivery` + its fixture control | ✓ shipped, see GAP-2 |
| D-121…D-125 + `docs/ai/decisions/Index.md` rows + the "Event sourcing" area paragraph | all five, all `accepted`, five rows, one paragraph | ✓ |
| FL-036 + **both** index tables in `docs/ai/flows/Index.md` | flow written, both tables edited | ⚠ see GAP-4 |
| UC-032 + `Index.md` row; "links only to flows, names no symbol" | ✓ verified by reading — no file path, no package name, no symbol in the body | ✓ |
| `docs/modules/{en,ru}/{event,eventmemory,eventtest}.md` + Index rows | six files; heading counts and inline-code table keys **identical** between `en` and `ru` for all three | ✓ |
| Roadmap supersessions in place | status block, the two baseline rows, E0 rows 1 and 3, the non-goal — all superseded **in place** with the reason | ✓ |
| `Roadmap.md` "§15 and its summary row 27" | §15 rewritten; the summary row is **15**, and there is no row 27 in that table | ⚠ GAP-11 |
| Exported surface added by S6 | **0** — every S6 file is `_test.go`, one shell variable, and Markdown | ✓ no silent extra surface |

### Architecture metrics — counted with `wc -l`, `awk` and `grep`, not eyeballed

| Metric | Value | Threshold |
|---|---|---|
| Lines per new file | `event/mutablestate_test.go` 249 · `refusalmessages_test.go` 194 · `scripts/event_test.go` 161 · `transactioncontrol_test.go` 127 · `scripts/extensions_test.go` 44 · `docs_test.go` +119 | all ≤ 400 ✓ |
| Longest function | `TestNoEventPackageCostsMoreThanTheSeamItNames` **41** · `packageLevelState` 39 · `renderedValues` 38 · `controlComplaints` 34 | ≤ 50 ✓ |
| Exported symbols added | **0** (`grep '^func [A-Z]\|^type [A-Z]\|^var [A-Z]' minus '^func Test'` → 0) | ✓ |
| Internal (first-party) imports per file | `scripts/extensions_test.go` 0 · the three `event/` files 0 · `scripts/event_test.go` 0 (the two `frostgrove` hits are string constants, not imports) | ≤ 5 ✓ |
| Import cycles | 0 — `go list -deps ./event` → `crud`, `errs`, `utils`, `event`; nothing under `event/` is imported by anything outside it | ✓ |
| Global mutable state added | 0 — the only new package-level `var`s are `docs_test.go`'s four `regexp.MustCompile` values, never reassigned | ✓ |
| Nesting depth | `packageLevelState` **5** (for/for/for/for/if) | ≤ 3 — **breach**, GAP-8 |
| Fakes to unit-test each new object | 0 — every check is a pure function over `go/ast` plus one `t.TempDir()` fixture; `listed` shells out to `go list` (1 real dependency, the toolchain) | ≤ 2 ✓ |
| Modules edited for this feature | 2 (`scripts/`, `docs/`) — no `Makefile` edit was needed | ≤ 3 ✓ |
| Forbidden shortcuts (`t.Skip`, `nolint`, `TODO`, loosened assertion) | **0** across all six S6 files | ✓ |

### Microkernel purity — the command and the result

```
$ grep -rn "eventmemory\|eventpg\|eventtest" event/*.go | grep -v _test.go | wc -l
0
$ go list -f '{{join .Imports "\n"}}' ./event | grep frostgrove
github.com/frostgrove/vv/crud
github.com/frostgrove/vv/errs
```

The kernel names no concrete store. **What a second store costs in diffs to `event/` is zero
and S6 does not change that.** What it costs *outside* `event/` is one row in
`scripts/event_test.go:seams` — which is deliberate ("the table is what the next store is
added beside") and correctly parked in `scripts/` rather than in the kernel. The three
checks' four required properties are all declared: contract (the `seams` table and the two
allow-lists), registration (a row per package), failure policy (`t.Errorf` naming the package
and what it reached), compatibility note (`costOf`'s two phrasings). See GAP-5 for the one
package class that mechanism does not reach.

---

### GAP-1 [high][immediate] `TestNoRefusalRendersAnIdentityAPayloadAKeyOrACursor` matches the *spelling* of an argument, so six of seven real §INV-011 violations pass

- **Where:** `event/refusalmessages_test.go:65-75` (`forbiddenToRender`), `:86-123`
  (`renderedValues`), `:125-132` (`formatsAMessage`), `:136-157` (`renderedName`)
- **What:** the mechanism is a fixed list of sixteen identifier spellings (`"Key"`, `"key"`,
  `"Cursor"`, `"cursor"`, `"Payload"`, … `"identity"`) compared against the *name* of the
  expression handed to `fmt.Errorf`/`fmt.Sprintf`. Nothing resolves a type. Three
  consequences, all structural:
  1. `renderedName` on an `*ast.CallExpr` that is not `len`/`cap` returns the **callee's**
     name (`:146 return renderedName(held.Fun)`), so `stream.Key.String()` reads as `"String"`
     and `string(stream.Key)` reads as `"string"`;
  2. `%v` on a struct that *contains* the forbidden values reads as the struct variable's name
     — `Envelope` carries `Stream`, `Version`, `Position` and `Payload`, and
     `fmt.Errorf("%w: %v", ErrBackend, envelope)` reads as `"envelope"`;
  3. `formatsAMessage` recognises only `fmt.Errorf` and `fmt.Sprintf`, so
     `errors.New("event: key " + string(key))`, `fmt.Sprint`, `fmt.Sprintln` and any
     `strings.Builder` message are not read at all.
  A fourth, smaller: `verbsIn` (`:159-174`) treats `[` and `*` as verbs, so `%[1]s` and `%*d`
  shift the whole verb→argument index mapping for the rest of the format string.
- **Why this severity:** demonstrated, not argued. The four helpers were extracted verbatim
  into `/tmp/escapecheck` and run over a fixture of seven deliberate §INV-011 violations:

  ```
  fmt.Errorf("%w: %s", ErrKey,      stream.Key.String())   arg-name="String"    FLAGGED=false
  fmt.Errorf("%w: %s", ErrKey,      string(stream.Key))    arg-name="string"    FLAGGED=false
  fmt.Errorf("%w: %v", ErrBackend,  envelope)              arg-name="envelope"  FLAGGED=false
  fmt.Errorf("%w: %v", ErrPayload,  raw)                   arg-name="raw"       FLAGGED=false
  fmt.Errorf("%w: %s", ErrCursor,   resume)                arg-name="resume"    FLAGGED=false
  fmt.Errorf("%w: %d", ErrConflict, at.v)                  arg-name="v"         FLAGGED=false
  fmt.Errorf("%w: %q", ErrPayload,  envelope.Payload)      arg-name="Payload"   FLAGGED=true
  ```

  **Six of seven pass.** The one caught is the one whose variable happens to be spelled with a
  forbidden word. §INV-011 says no key, payload, version, position, cursor or identity reaches
  an error message; `event/repo.go`, `event/reader.go` and `event/errors.go` hold **72**
  `fmt.Errorf`/`fmt.Sprintf` sites, and the behavioural sibling
  `TestEveryRenderingNamesAClassAndNeverAValue` (`event/rendering_test.go:101`) walks the 24
  sentinels, the door refusals and the `Backing`/`Authority`/`Stream` renderings — it does
  **not** walk those 72 messages. So this AST check is the only guard over them, and it is
  blind in exactly the directions a future refusal will drift. `universality.md`: "a keyword /
  phrase / pattern list used as **the** mechanism rather than as one configurable signal among
  several" — minimum `[high][immediate]`, never deferred.
- **Why this timing:** it is a hardcode finding (never deferred), it is the check the plan and
  three docs cite as proof of §INV-011 (`FL-036:314`, `D-122` "Proven by"), and phase 2's
  `eventpg` will add refusal sites under the same guarantee. A doc that asserts a guarantee
  the named test cannot make is the failure `docs_test.go` exists to prevent, one level up.
- **Close criteria:**
  - [ ] the forbidden set is decided from the argument's **type**, not its spelling — the
        stdlib `go/types` + `go/importer` (or an `importer.ForCompiler` over the package) is
        enough and adds no dependency; any expression whose type is or transitively contains
        `Key`, `Cursor`, `Version`, `Position`, `Envelope`, `Record` or a payload `[]byte` is
        reported whatever it is called
  - [ ] the fixture subtest at `:40-57` is extended with all six escapes above and each is
        asserted reported
  - [ ] the set of message constructors read is derived rather than listed: any call whose
        result flows into an `error` (`errors.New`, `fmt.Errorf`, `fmt.Sprint*`, string
        concatenation into one) is inspected, and the check fails loudly if it read zero
        constructors of a kind the package uses
  - [ ] `verbsIn` handles `%[n]v` and `%*d` or the check refuses a format string containing
        either, rather than silently mis-indexing
  - [ ] the scope note is written down: `event/eventmemory`'s own messages are not read, and
        either they are brought in scope or `FL-036` says why they need not be
- **Status:** **closed** 2026-09-07. `event/refusalmessages_test.go` is rewritten
  against `go/types` (`event/sources_test.go` type-checks a directory with the
  standard library's source importer — stdlib, no dependency). The forbidden set
  is four **types** — `Key`, `Cursor`, `Version`, `Position` — plus a byte slice,
  and everything else is derived: a value whose type reaches one of them renders
  it, and a type declaring its own `String`/`Error` decides its own rendering,
  which is what lets `Stream` through and is now asserted present as the control.
  `renderedName` and the sixteen spellings are gone. All six escapes the finding
  demonstrated are in `event/fixtures_test.go:renderingFixture` and each is
  asserted reported: `key.String()`, `string(stream.Key)`, `%v` on an envelope,
  a bare `[]byte`, a value passed through a helper, and a `Version`/`Cursor`
  field. A message constructor is derived — any call returning an error that
  takes a `string` — so nothing is listed and an append or a decode carrying a
  key is not read. `%[n]v` and `%*d` are **refused** rather than mis-indexed.
  Scope: the packages a program links, so `event/eventmemory` is now read too
  (120 messages against 72 before) and `event/eventtest` is out because it
  imports `testing` — derived, and written down in `FL-036`'s new
  *What the four structural checks reach* section. Falsified: `string(key)` and
  `[]byte(key)` added to `Repo.checkKey` produce two reports at `repo.go:169`.
  One criterion is answered differently and argued rather than met: the
  "fails loudly if it read zero constructors of a kind the package uses" clause
  is satisfied by construction — the predicate is the **result type**, so there
  is no kind to miss — plus two floors that a name list cannot have, that both a
  wrapping and a bare construction were read.

---

### GAP-2 [high][immediate] `TestNoDocPromisesExactlyOnceDelivery` reads one language and excuses a promise on any nearby "no", so four of five real phrasings escape

- **Where:** `scripts/docs_test.go:751-755` (the four regexes), `:783-832` (`deliveryClaims`,
  `deliveryWindow`)
- **What:** three fitted literals are the whole mechanism.
  1. `exactlyOnce = (?i)exactly.?once` is **English only**. `docs/` ships a parallel Russian
     tree by design (`docs/modules/ru/` — 30+ files, including the three this section wrote),
     and the Russian phrase is `ровно один раз`, which is already in the tree at
     `docs/modules/ru/vvcfg.md:63`. No Russian doc is checked, and the `checked == 0` floor
     cannot notice because it counts English hits.
  2. `refusesTheClaim = (?i)\bno\b|\bnot\b|\bnever\b|…` excuses a promise whenever any of
     those tokens appears in the line, either neighbour, or the nearest heading above.
  3. `aboutDelivery = (?i)deliver|broker|at.least.once` — a promise that does not use the word
     "deliver" is not even classified.
- **Why this severity:** demonstrated with the four regexes verbatim in `/tmp/doccheck`:

  ```
  "A projector delivers … exactly once, so you do not need an inbox table."   EXCUSED as a refusal
  "# No configuration needed" + "delivers each event exactly once."           EXCUSED as a refusal
  "Проектор доставляет каждое событие ровно один раз."                        INVISIBLE
  "Every subscriber sees each committed event exactly once."                  NOT EVEN CHECKED
  "A projector delivers every committed event exactly once."                  REPORTED  ← the control
  ```

  The one phrasing the check catches is the one its own fixture writes. §INV-036 and
  `UC-032`'s "Out of scope" make "no doc promises exactly-once delivery" a stated invariant of
  this subsystem; a Russian module page, or an English one with an ordinary negation in the
  paragraph, ships the promise silently and a reader builds no idempotency into a consumer of
  an at-least-once log. `universality.md`: a phrase list as the mechanism, tuned to one
  language and one sentence shape.
- **Why this timing:** hardcode finding, never deferred. The `ru` tree is written *in the same
  changes* as the `en` tree by this repository's own rule, so the blind half grows with every
  future doc, and the check is cited as proof in `FL-036:330`.
- **Close criteria:**
  - [ ] the phrase table is data, not a literal in the walker, and carries at minimum the
        `en` and `ru` renderings of "exactly once", "at least once" and "delivery" — the two
        languages `docs/` actually ships
  - [ ] the check fails when a language present in the tree contributed **zero** checked
        occurrences, so blindness is loud instead of green (the existing `checked == 0` floor
        is per-tree and cannot see it)
  - [ ] "refusal" is decided by a negation **attached to the claim** (a negation in the same
        sentence or in a `Non-goals`/`Out of scope` heading), not by any of eight tokens in a
        ±1-line window; the fixture asserts the two English cases above are reported
  - [ ] `aboutDelivery` no longer gates the check on the word "deliver": a phrasing about
        subscribers, consumers, projections or handlers is classified rather than skipped
  - [ ] `TestAPromiseOfExactlyOnceDeliveryIsReportedAndARefusalOfOneIsNot` carries all five
        rows above with their expected verdicts
- **Status:** **closed** 2026-09-07, with one criterion rejected in writing.
  `scripts/docs_test.go` now reads a `wording` table with an English and a
  Russian row, each carrying the promise, the weaker claim, the delivery
  vocabulary, the negation and the refusing heading. A refusal is a negation
  **attached to the claim** — the sentence up to the end of the clause the phrase
  sits in, the sentence before it in the same paragraph, or a `non-goal` /
  `out of scope` heading — so the two English promises the finding demonstrated
  are reported and the ten real prohibitions in `docs/` stay green.
  `aboutDelivery` no longer turns on the word "deliver": subscriber, consumer,
  projection, projector, handler, dispatch, inbox, outbox and "at least once" all
  classify. Markdown is read a paragraph at a time — a list item, a table row and
  a heading each begin one — so a phrase wrapped across two lines is one phrase,
  which the line-based version could not see.
  `TestAPromiseOfExactlyOnceDeliveryIsReportedAndARefusalOfOneIsNot` carries all
  five rows plus the two refusals and the non-delivery use, and asserts the
  per-language checked counts. Falsified twice against the real tree: a promise
  added to `docs/modules/en/event.md` **under its own `## What this is not`
  heading and with a trailing "so you do not need an inbox table"** is reported
  at `:235`, and the Russian rendering added to `docs/modules/ru/event.md` is
  reported at `:239`. That first falsification also removed `what this is not`
  from the refusing headings, which had been written into the first draft of
  this fix and would have excused exactly the case the finding is about.
  **Rejected:** criterion 2 as written — "fails when a language present in the
  tree contributed zero *checked* occurrences". Measured: the `ru` tree has **no**
  delivery-scoped occurrence of the phrase at all (`docs/modules/ru/event.md`
  says `доставляет не менее одного раза`, which is the prohibition worded without
  the phrase), so that arm would be permanently red and would be loosened on its
  first run — [REC] R6. What shipped is the same arm over the language's
  **counting vocabulary**: a language written in the tree that contributed zero
  occurrences of either its exactly-once or its at-least-once wording fails,
  because a row that matches nothing permits everything. `ru` contributes two.
  The fragility that leaves is recorded in the plan's `## Debt`.

---

### GAP-3 [high][immediate] `TestNoPackageLevelStateIsEverMutated` reads two spellings of a mutable declaration, so `make(...)`, `new(...)` and mutation through a parameter all pass

- **Where:** `event/mutablestate_test.go:111-135` (`heldKind`), `:152-181` (`mutationsOf`)
- **What:** `heldKind` inspects `value.Type` and the `*ast.CompositeLit` type of an
  initialiser — and nothing else. A package-level `var` initialised by a **call** has neither,
  so `make(map[string]int)` and `new(sync.Pool)` are not classified as holding mutable state.
  `mutationsOf` then reports only `*ast.AssignStmt`, `*ast.IncDecStmt` and a `delete(...)`
  whose root identifier is one of the collected names — so state written through a function
  **parameter** is invisible, and `pool.Put(x)` / `atomic.AddInt64(counter, 1)` are not
  mutations at all under this predicate.
- **Why this severity:** demonstrated with `heldKind`, `synchronised`, `mutationsOf` and
  `rootName` extracted verbatim into `/tmp/statecheck` and run over

  ```go
  var seen  = make(map[string]int)
  var pool  = new(sync.Pool)
  var tally = new(int64)
  func remember(name string) { record(seen, name); pool.Put(name); bump(tally) }
  func record(m map[string]int, k string) { m[k]++ }
  func bump(n *int64)                     { *n++ }
  ```

  → `package-level names: 3` · **`NO COMPLAINTS — every one of these escaped`**. All three are
  process-wide mutable state shared by every request goroutine, which is precisely what
  §INV-013 forbids and what `architecture.md` counts as `0` allowed. The check's own fixture
  (`:219-249`) uses `map[string]int{}` (a composite literal) and `sync.Mutex` (a named type) —
  the two spellings the predicate happens to read — so the falsification subtest passes while
  the three ordinary spellings do not. The plan's Architecture-metrics table cites this test as
  the enforcement for "Global mutable state | **0**".
- **Why this timing:** hardcode/shape-assumption finding, never deferred. `event/eventpg` and
  any later store are walked by the same predicate (`extensionDirectories` reads the
  filesystem, so a nested module *is* reached here), and a connection pool or a prepared-
  statement cache declared as `var stmts = make(map[string]*sql.Stmt)` is exactly the shape
  that escapes.
- **Close criteria:**
  - [ ] a package-level `var` whose initialiser is a call to `make` or `new` is classified by
        the type argument of that call, not skipped
  - [ ] mutability is decided from the declared/inferred **type** (map, slice, chan, pointer,
        func, interface, or any type containing a `sync`/`atomic` member) rather than from the
        two syntactic shapes currently read — `go/types` is stdlib and adds no dependency
  - [ ] a package-level name **passed as an argument** to a function that writes through its
        parameter is reported, or the check states in its own message that it cannot see that
        and a second arm covers it (for example: report any package-level name of a reference
        kind that is *read* outside its own initialiser at all)
  - [ ] the fixture at `:219-249` gains the three escapes above and asserts each is reported
  - [ ] the `declared < 24` floor is restated in terms the check cannot satisfy vacuously —
        today all 24 come from `errors.go` alone and prove nothing about the other files walked
- **Status:** **closed** 2026-09-07. `event/mutablestate_test.go` takes the kind
  from the value's **type** through `go/types`, so `make(map[string]int)`,
  `new(sync.Pool)` and `new(int64)` are findings where the two syntactic shapes
  were not: map, slice, channel, pointer, and anything transitively holding a
  `sync`/`sync/atomic` type. The "written through a parameter" hole closes with
  it and needs no second walk — such a name is reported **where it is declared**,
  which is stated in the file's comment. `heldKind` reads a `types.Type`;
  `mutationsOf` resolves each target through `info.Uses` against the package
  scope rather than by root name, so a shadowed local cannot be mistaken for a
  package-level one. The fixture gains all three escapes the finding ran —
  `seen = make(...)` written through `record`, `pool = new(sync.Pool)` written
  through `Put`, `tally = new(int64)` written through `bump` — and each is
  asserted reported while the `errors.New` sentinel is asserted not. The vacuity
  floor is restated per package as well as in total: a walked directory that
  declares nothing at package level now fails by name, so the 24 from
  `errors.go` can no longer stand in for the other two packages. Falsified:
  `seen` and `tally` added to `event/errors.go` produce two reports.

---

### GAP-4 [high][immediate] FL-036's file table and the reverse index omit thirteen of the tree's source files, including the six that hold the whole conformance suite

- **Where:** `docs/ai/flows/FL-036-a-decision-becomes-a-recorded-fact.md:221-261` (the Files
  table), `docs/ai/flows/Index.md:457-489` (the "By file" reverse index)
- **What:** measured, not eyeballed:

  ```
  $ comm -23 <(find event -name '*.go' ! -name '*_test.go' ! -path 'event/testdata/*' | sort) \
             <(grep -oP '^\| `\Kevent/[^`]+' docs/ai/flows/Index.md | sort)
  event/doc.go
  event/eventmemory/doc.go
  event/eventtest/declaration.go
  event/eventtest/defects_lifecycle.go
  event/eventtest/defects_ownership.go
  event/eventtest/defects_read.go
  event/eventtest/defects_write.go
  event/eventtest/doc.go
  event/eventtest/sections_lifecycle.go
  event/eventtest/sections_ownership.go
  event/eventtest/sections_read.go
  event/eventtest/sections_resumption.go
  event/eventtest/sections_transactions.go
  event/eventtest/sections_write.go
  ```

  Fourteen rows missing from the reverse index, thirteen from FL-036's own table (which does
  list `event/doc.go`). That is **≈ 78 KB / ≈ 2 700 lines** of shipped source with no flow
  row — among it every one of the six `sections_*.go` files, which contain the twenty
  conformance sections FL-036 spends a section describing, and the four `defects_*.go` files
  that hold the twenty-five deliberately broken stores it also describes. The plan's own S6
  entry promises "`docs/ai/flows/FL-036-…` + **both** index tables".
- **Why this severity:** `CLAUDE.md` is explicit and binding on both halves — "A flow is the
  only place file paths and symbols appear", and "the reverse index maps **every** source file
  to the flows that touch it. If you are about to edit a file, read its flows first", with "an
  index that does not list a file is worse than a missing file — an agent trusts the index and
  stops looking." Concrete failure: the next agent asked to add a conformance section opens
  `docs/ai/flows/Index.md`, greps for `sections_write.go`, finds nothing, concludes the file is
  outside every flow, and edits it without reading FL-036's traps — the short-page trap, the
  payload-ownership trap and the transaction-retry trap are all stated only there. This is the
  documentation section's own primary deliverable, and the eventtest split landed at 20:54
  while FL-036 was written at 21:19+, so the files existed when the table was written.
  `CLAUDE.md`: "Documentation drift is a defect here, not untidiness … Treat a stale doc
  exactly like a failing test."
- **Why this timing:** it is the deliverable of this section, not a follow-up; and `docs_test.go`
  cannot catch it (it verifies that cited names *exist*, never that existing files are cited),
  so nothing will surface it later.
- **Close criteria:**
  - [ ] every non-test `.go` file under `event/` has a row in FL-036's Files table with what
        it holds, or FL-036 states a rule for what it deliberately omits (`doc.go`, say) and
        that rule is applied consistently
  - [ ] the same set appears in `docs/ai/flows/Index.md`'s "By file" table
  - [ ] the `comm -23` command above returns empty (modulo a stated omission rule)
  - [ ] consider making it enforceable the way the citation check already is: a `scripts/`
        test that fails when a source file under a subsystem named by a flow has no reverse-
        index row — otherwise this recurs on the next split
- **Status:** **closed** 2026-09-07, with criterion 4 rejected for this section
  and carried as repo-wide debt. All fourteen missing files now have a row in
  `FL-036`'s Files table and in `docs/ai/flows/Index.md`'s "By file" table:
  `event/doc.go`, `event/eventmemory/doc.go`, `event/eventtest/doc.go`,
  `declaration.go`, the six `sections_*.go` and the four `defects_*.go`, each
  with what it holds. `scripts/extensions_test.go` gained a row too, since the
  round moved the walking there. The finding's own command now returns empty:
  `comm -23 <(find event -name '*.go' ! -name '*_test.go' ! -path 'event/testdata/*' | sort) <(grep -oP '^\| `\Kevent/[^`]+' docs/ai/flows/Index.md | sort)`.
  `FL-036` states the rule it follows — every non-test `.go` file, `doc.go`
  included — and gains a *What the four structural checks reach, and what they do
  not* section; `D-121`'s *Where it lives* and *Proven by* say which package
  classes each check reaches.
  **Rejected for S6:** making it enforceable here. Measured: the reverse index
  holds 383 rows against 510 non-test source files, and the narrower rule —
  every file in a directory the index *already* mentions — still leaves 108
  unrowed files across `auth`, `cache`, `crud`, `internal`, `jobs`, `otel`,
  `port`, `storage`, `tenancy`, `test` and `utils`. A check scoped to `event/` is a
  rule fitted to one example, which `universality.md` forbids; a repo-wide one is
  red on arrival and belongs to a repo-wide documentation task. Recorded in the
  plan's `## Debt` with the measurement.

---

### GAP-5 [medium][immediate] the three graph checks list `./event/...`, which excludes a nested module — and `event/eventpg` is planned to be exactly that

- **Where:** `scripts/event_test.go:25` (`"./..."`), `:59`, `:113` (`"./event/..."`);
  `docs/ai/decisions/D-121…:7-8, 105, 131-135`
- **What:** `go list` package patterns stop at a nested `go.mod`. Verified on the sibling that
  already has one:

  ```
  $ go list ./jobs/...
  github.com/frostgrove/vv/jobs
  github.com/frostgrove/vv/jobsmemory        # jobspg, jobsredis, jobsfx are modules → invisible
  $ find . -name go.mod -not -path './test/*' -not -path './_examples/*' | wc -l
  32
  ```

  D-121 states plainly that `event/eventpg` **is** a module ("It is a module at that point,
  under `event/`, and the move is mechanical: one `go.mod`, one `go.work` line…"). The moment
  it exists: `TestNoEventPackageCostsMoreThanTheSeamItNames` still sees three packages, still
  equals `len(seams)+1`, and never measures what the PostgreSQL store costs; and
  `TestMerelyImportingTheEventExtensionStartsNothing` never reads its files, so an `init()` or
  a background goroutine in the store that actually talks to a database is unchecked.
  (`event/mutablestate_test.go` is unaffected — it walks the filesystem, not `go list`.)
- **Why this severity:** the guarantee two docs assert becomes false for the one extension the
  roadmap says is next. `D-121:7-8` — "a store package costs the vocabulary and whatever its
  own row in `scripts/event_test.go` says"; `D-121:131-135` cites both tests as "Proven by".
  Nothing today is wrong; the mechanism the plan calls "what the next store is added beside"
  will not fire for the next store. Not `[high]` because `make check-deps` and `check-replaces`
  still measure a module's external weight, and the remedy is a small, local edit.
- **Why this timing:** phase 2 starts by writing `event/eventpg`, and it must not inherit a
  check it believes is protecting it. Recording it after the store is written is recording it
  too late.
- **Close criteria:**
  - [ ] the three checks enumerate satellite modules as well — e.g. drive off
        `find event -name go.mod` (or the `go.work` `use` list) and run `go list` inside each,
        the way `make unit`/`make vet` already discover modules rather than keeping a list
  - [ ] a floor asserts the number of *modules* walked, so a store that becomes a module and
        is not added is a red test rather than a silent skip
  - [ ] `D-121`'s "Where it lives" / "Proven by" say which package classes each check reaches
  - [ ] falsified: a throwaway `event/eventtmp/go.mod` with an `init()` in it turns
        `TestMerelyImportingTheEventExtensionStartsNothing` red, then is removed
- **Status:** **closed** 2026-09-07. `scripts/extensions_test.go` gains
  `modulesUnder`, `extensionModules` and `packagesUnder`: every module under an
  extension is listed **in its own directory**, so a nested `go.mod` is reached
  rather than skipped, and `firstPartyDependenciesIn` runs `go list -deps` where
  the package actually lives. The floor is `coversEveryDirectory`, which fails
  when a directory holding non-test source was listed by nobody — self-maintaining
  where a hand-written module count is not, and it is what a store that becomes a
  module trips. `TestNoBaseSubsystemDependsOnThe*Extension` now walks **every
  published module** (32 of them, 88 packages) rather than the root's 54: a
  satellite importing an extension makes it non-optional for that satellite's
  consumers, and that whole class was invisible before. `test` and `_examples`
  are excluded, which is the pair `scripts/common.sh:satellites` already names.
  `D-121`'s *Where it lives* and *Proven by* say all of this.
  Falsified exactly as the criterion asks: a throwaway `event/eventtmp` module
  with an `init`, an `os.Getenv` initialiser and a goroutine. Before it is added
  to `go.work` the checks fail with `cannot list [./...] in ../event/eventtmp` —
  which is the right answer, because a module outside the workspace escapes
  `make unit` too. Added to `go.work`, all three checks report it: `the extension
  has 4 packages and 2 of them say what they cost`, `…/eventtmp … says nothing
  about what it costs`, and the `init`, the `os.Getenv` and the goroutine each by
  position. Removed afterwards. Separately falsified with
  `_ "…/event"` imported from `jobs/jobsfx`, a satellite module, which the old
  root-only walk could not see.

---

### GAP-6 [medium][immediate] the event and tenancy "starts nothing" checks have already diverged — the event one silently dropped the package-level environment/global arm

- **Where:** `scripts/event_test.go:112-161` vs `scripts/tenancy_test.go:92-103`
- **What:** the two extension check-suites are now near-duplicates that must be kept in step,
  and one pair already differs in what it forbids:

  | | tenancy (grep, `:98`) | event (`go/ast`, `:141-161`) |
  |---|---|---|
  | `func init()` | ✓ | ✓ |
  | `go func(` / `go f(` | ✓ (all packages) | ✓ (packages that do not import `testing`) |
  | `var … = os.Getenv(…)` | ✓ | **✗ — dropped** |
  | `var … = regexp.MustCompile(…)` | ✓ | **✗ — dropped** |
  | false positive on the word "go " in prose | ✗ (the reason for the rewrite) | ✓ fixed |

  The plan argues the `go/ast` rewrite (R6: an unanchored `go [a-zA-Z]` goes red on a comment)
  and it is right — but the environment/global half went with it and is written down nowhere.
  The first pair, meanwhile, is *byte-for-byte identical*: `diff` of
  `tenancy_test.go:21-37` and `event_test.go:24-40` with the two constants normalised reports
  only the function name. `architecture.md`: "Two methods whose bodies differ only by a
  constant … are one method with a parameter", and the DRY signal it warns about — "they will
  diverge and produce two different answers to the same question" — has already happened in the
  third pair, in the same commit that created the second copy.
- **Why this severity:** today `grep -rn "os.Getenv\|regexp.MustCompile" event/ --include='*.go'`
  returns nothing, so it is a latent hole rather than a live one — a package-level
  `var pattern = regexp.MustCompile(...)` or an env read added to `event/` or to any future
  store passes. `[medium]` rather than `[high]` because two copies is `architecture.md`'s own
  tolerated number and the plan argued the split deliberately; `immediate` because the second
  copy is what makes divergence cheap and it is cheapest to reconcile now, while both are one
  file apart.
- **Close criteria:**
  - [ ] the `go/ast` walker gains the arm the grep had: a package-level `var` whose initialiser
        reaches `os.Getenv`, `os.LookupEnv`, `regexp.MustCompile` or any other call is reported
        (a package-level initialiser that *calls anything* is the general form)
  - [ ] `scripts/tenancy_test.go`'s grep is replaced by the same walker, so the two extensions
        answer one question — or FL-036/D-121 states in writing why the two prohibitions differ
  - [ ] `TestNoBaseSubsystemDependsOnThe*Extension` exists once, parameterised by the extension
        prefix, in `scripts/extensions_test.go` beside the three helpers already shared there —
        the bodies are identical, which is the case `architecture.md` says is one function
  - [ ] the fixture control for the new arm is written and watched go red
- **Status:** **closed** 2026-09-07. There is now one walker. `startsNothing`
  and `noBaseSubsystemDependsOn` live in `scripts/extensions_test.go` and both
  extension files call them; the test **names** stay where they are, because
  `D-116:115,119`, `FL-033:214`, `docs/modules/{en,ru}/tenancy.md:46`, the
  multitenancy roadmap and this plan's own `-list` checkpoint clause all cite
  them, and `scripts/docs_test.go` fails on a cited name that does not exist.
  The dropped arm is back and **generalised rather than restored**: instead of
  the grep's two spellings, a package-level `var` initialiser that calls anything
  is reported unless it is `errors.New`, `fmt.Errorf` or `reflect.TypeFor` — the
  three the repository's own idiom needs, each argued in the comment. That is
  fail-closed, so `time.Now`, `sql.Open`, `rand.New` and anything else nobody
  thought of are refused without being named, and a conversion (`(*Store)(nil)`,
  `[]byte("…")`) is correctly not a call. The tenancy grep is gone, so the
  false positive on the word "go " in prose is gone from that side too, and the
  two extensions now answer one question. Falsified:
  `var identifier = regexp.MustCompile(…)` and `var home = os.Getenv(…)` added to
  `event/text.go` are both reported by position.

---

### GAP-7 [medium][immediate] `TestTheKernelNeverIssuesTransactionControlAndNeverRetries` matches selector names, so an unexported or free-function equivalent passes and an unrelated `.Append` in a loop fails

- **Where:** `event/transactioncontrol_test.go:50-102` (`controlComplaints`, `appendsWithin`,
  `calledMethod`)
- **What:** `calledMethod` returns `selector.Sel.Name` and the three arms compare it to the
  literals `"Begin"`, `"Commit"`, `"Rollback"` and `"Append"`. False negatives:
  `tx.commit()`, `commit(tx)` (a free function is an `*ast.Ident`, not a selector),
  `this.finish()`, a retry written as recursion, and `store.Append` reached through a closure
  declared outside the loop that encloses its call. False positive: any *other* method spelled
  `Append` inside a `for`/`range` — `strings.Builder`-style APIs and `crud` builders both
  have one — would be reported with a message about stale decisions.
- **Why this severity:** the plan's argument that "the `Store` seam declares none, so there is
  no exemption to write" is correct and is what keeps this from being `[high]`:
  `TestTheStoreSeamHasEightMethodsAndNoneMutatesOrQueries` is the real gate on the seam's
  shape, and `event/repo.go:96` holds the only `.Append(` in the kernel. But the check is
  presented in `FL-036:318` as proof that "the kernel never issues transaction control", and
  a name-matched prohibition cannot make that claim about code that spells it differently.
- **Why this timing:** it is a prohibition check whose stated scope and actual scope differ,
  and the difference is cheapest to correct while the kernel still has one `Append` call site.
- **Close criteria:**
  - [ ] the transaction arm resolves the **receiver's type** (a `go/types` question) or, at
        minimum, matches case-insensitively and includes free-function calls, so `commit(tx)`
        and `tx.commit()` are reported
  - [ ] the loop arm resolves that the `Append` it found is `event.Store.Append` rather than
        any method with that name, so the check cannot go red on an unrelated builder and get
        loosened (R6)
  - [ ] a recursion arm, or a written statement in the test's comment that a retry expressed
        as recursion is not detected and what does detect it
  - [ ] the fixture at `:104-127` gains `tx.commit()`, `commit(tx)`, a recursive retry and a
        `builder.Append` inside a loop, with the expected verdict for each
- **Status:** **closed** 2026-09-07. `event/transactioncontrol_test.go` resolves
  rather than spells. Transaction control matches case-blind over the callee's
  final identifier and covers a plain `*ast.Ident`, so `tx.commit()` and
  `commit(tx)` are each reported. An `Append` is the store's only when
  `types.LookupFieldOrMethod` finds `ReadStream` on the receiver's type — the
  seam's own shape — so a builder with an `Append` of its own cannot turn the
  check red and get it loosened, which was the R6 risk in the original. A third
  arm reports a function that calls **itself** around a store append; mutual
  recursion through two functions is stated as not seen, in the file's comment,
  with the loop arm named as what catches the ordinary form. The fixture carries
  `tx.Begin()`, `tx.commit()`, `commit(tx)`, `tx.Rollback()`, a loop around
  `store.Append`, a recursive retry and a `goto`, and asserts that a paged read
  and `builder.Append` inside a `range` are **not** reported. Falsified against
  the tree: a free `commit(ctx)` and a `for` around `this.store.Append` in
  `event/repo.go` are reported at `repo.go:96:2` and `repo.go:98:7`.

---

### GAP-8 [medium][deferred] `packageLevelState` nests four loops and an `if` — five levels against `architecture.md`'s three

- **Where:** `event/mutablestate_test.go:71-109`
- **What:** `for source` → `for declaration` → `for spec` → `for name` → `if held != "";`
  the guard clauses (`continue`) are used correctly, but the loop nest itself is four deep in
  a 39-line function. `architecture.md`'s table: nesting depth ≤ 3, target 2. The plan's
  § Architecture metrics table covers the shipped `event/` files and does not list any S6 file,
  so the breach is unjustified in writing.
- **Why this severity:** a literal reading of `architecture.md` ("An unjustified breach is a
  gap of severity `high`") would say high; recorded as medium because it is one test helper,
  entirely module-internal, on nothing's public surface, and the innermost body is two lines.
  The concrete cost is readability: the file is the one three other test files depend on for
  `sourcesIn`, so it is read often.
- **Why this timing:** `deferred` by gaps.md's own rule — module-internal detail with no effect
  on any external contract. It does not block the gate. If GAP-3 is closed by moving to
  `go/types`, this function is rewritten anyway and the nest disappears with it.
- **Close criteria:**
  - [ ] the two inner loops (`GenDecl` → `ValueSpec` → `Names`) are extracted into a named
        function — `declaredNames(file)` or `packageVars(file)` — leaving `packageLevelState`
        at ≤ 3
  - [ ] or the plan's § Architecture metrics gains an S6 row justifying the breach in writing
- **Status:** **closed** 2026-09-07, as the finding itself predicted. GAP-3's
  move to `go/types` rewrote `packageLevelState` into a walk over the package
  scope, and the four-deep loop nest is gone: the function is now a single loop
  over `scope.Names()` with two guard clauses, 19 lines. No S6 file exceeds a
  nesting depth of 3 or a length of 41 lines, and the three broken-source
  constants moved to `event/fixtures_test.go` so that every file of this round
  stays inside the 400-line budget.

---

### GAP-9 [low][immediate] `scripts/event_test.go` carries an unreachable branch and shadows a `bool` with a `[]string` of the same name

- **Where:** `scripts/event_test.go:55-58`, `:75-92`, `:96-101`
- **What:** both rows of `seams` map to `""`, so `costOf`'s second return (`:100`) and the
  `if seam != ""` append (`:83-84`) are dead today — `architecture.md`'s "Extending a public
  contract 'for the future'" / Protected Variations counter-rule. Separately, `named` is a
  `bool` at `:75` (`seam, named := seams[path]`) and is re-declared as a `[]string` at `:81`
  inside the `t.Run` closure; one identifier means two things eleven lines apart.
- **Why this severity:** cosmetic today, but it is the file a future store's row is added to,
  and the dead branch is the part that would carry that row's meaning.
- **Why this timing:** `immediate` under gaps.md's "weak naming in a code path other sections
  are about to depend on" — phase 2 edits exactly these lines.
- **Close criteria:**
  - [ ] the `seam` value is either used by a row or the map is a `map[string]bool` / set until
        a store needs a seam, with a one-line note saying what a non-empty value would mean
  - [ ] `named` (the `[]string`) is renamed — `allowedFrom`, `charged` — so no identifier
        carries two types in one function
- **Status:** **closed** 2026-09-07. `seams` is a `map[string]bool` named
  `charged`; `costOf` and its unreachable second return are deleted, and the
  comment above the table says in one sentence what a non-empty value would have
  meant and when the set becomes a map again — a store reaching a first-party
  package the vocabulary does not, which is where its own dependency would be
  argued. The `named` `bool` no longer shares its name with a `[]string`: the
  allow-list is built inline as `allowed`, and the boolean read is gone with the
  branch it guarded.

---

### GAP-10 [low][deferred] four parallel implementations of "read this package's non-test sources", and the shared one lives in the wrong file

- **Where:** `event/mutablestate_test.go:202-217` (`sourcesIn`, used by
  `refusalmessages_test.go:26`, `transactioncontrol_test.go:25` and `refusal_test.go:108`),
  `event/store_test.go:83-124` (the same `os.ReadDir` + suffix filter, inline),
  `event/refusal_test.go:133-152` (a single-file parse of the literal `"errors.go"`),
  `scripts/event_test.go:113` (the same question asked of `go list`)
- **What:** four bodies answering one question, differing only in what they do with the parsed
  file. `sourcesIn` is the shared one and it is housed in `mutablestate_test.go`, a file named
  for a different check — so `refusalmessages_test.go` cannot be read or moved on its own.
- **Why this severity:** low — all four are test scaffolding, none is on a public surface, and
  `architecture.md` tolerates two copies. It is on the list because it is four and because
  three of the four were written or edited in this section.
- **Why this timing:** deferred — no contract turns on it.
- **Close criteria:**
  - [ ] `sourcesIn` (and, if it earns it, a `parsedSourcesIn`) lives in a file named for what
        it owns — `sources_test.go` — and `store_test.go`'s inline walk uses it
  - [ ] `namesListedInTheGate`'s `"errors.go"` literal is either derived or its comment says
        why one file is the right scope where the sibling now walks all of them
- **Status:** **closed** 2026-09-07. `sourcesIn` lives in the new
  `event/sources_test.go`, named for what it owns, beside `typedSources`,
  `extensionDirectories` and `linkedDirectories`;
  `event/refusalmessages_test.go` can now be read on its own.
  `event/store_test.go:exportedInterfaces` uses it instead of its own
  `os.ReadDir` walk, and `event/refusal_test.go:namesListedInTheGate` walks the
  package rather than the literal `"errors.go"`, with its comment saying why —
  for the reason its sibling `namesDeclaredInThePackage` already walks it. Four
  bodies became one.

---

### GAP-11 [low][deferred] three citation slips inside the plan's own S6 entry

- **Where:** `.agents/artifacts/plans/EVENTSOURCE_P1_PLAN.md:4561`, `:4617`, `:4476`
- **What:** (a) ":4561 — `docs/roadmaps/Roadmap.md` — §15 and its summary row **27**"; the
  summary table's row for this item is **15** and `grep '| 27 |' docs/roadmaps/Roadmap.md`
  returns nothing. (b) ":4617 — D-123's blurb says "**ten** triggers, otherwise ten `recover()`
  blocks"; the shipped `D-123:30-34` says fifteen refuse a declaration outright plus seven more,
  twenty-two `recover()` blocks. (c) ":4476 the heading reads "the **five** ADRs" where the rest
  of the plan and the review brief say four — harmless, but it is the heading a reader counts
  from.
- **Why this severity:** low; the shipped documents are correct and the plan is the artifact
  that is wrong, in three places nothing reads for behaviour.
- **Why this timing:** deferred — no code and no shipped doc depends on it.
- **Close criteria:**
  - [ ] the three spots are corrected in the plan, or (b) and (c) are left with a note saying
        the ADR is the authority
- **Status:** **closed** 2026-09-07, two corrected and one rejected. (a) the
  plan now reads "§15 and its summary row **15**", which is the row
  `docs/roadmaps/Roadmap.md` actually carries. (b) D-123's blurb in the plan now
  says **twenty-two** triggers and twenty-two `recover()` blocks, matching the
  shipped `D-123:29-34` (fifteen refusing a declaration outright plus seven more).
  (c) **rejected**: the heading is right. Five ADRs were agreed and five shipped
  — `D-121` through `D-125` — and the plan's own ADR table, its `## Debt` and
  `docs/ai/decisions/Index.md` all say five. The review brief's "four" is the
  stale count, not the heading's.

---

### GAP-12 [low][deferred] a 2 281-line untracked `GAPS.md` sits in the repository root

- **Where:** `/home/user/ws/apps/photon/tmp/frostgrove/framework/GAPS.md` (untracked,
  dated 2026-09-06, the six-module naive-contracts audit)
- **What:** not S6's output — it predates this section and belongs to the tenancy-era audit —
  but it collides with this workflow's own convention that gap reports live in
  `.agents/artifacts/gaps/`, and it is the first file an agent opening the repo root sees.
  Recorded here so it is not lost rather than because S6 wrote it.
- **Why this severity:** low; it is a stray artifact, not a defect in any code path.
- **Why this timing:** deferred; owned by whoever produced it.
- **Close criteria:**
  - [ ] the file is moved under `.agents/artifacts/`, committed deliberately, or deleted —
        with whichever is chosen stated in the plan's `## Debt`
- **Status:** **closed** 2026-09-07. Moved, not deleted:
  `GAPS.md` → `.agents/artifacts/audit/NAIVE_CONTRACTS_AUDIT.md`, which is where
  this workflow's audit reports already live (`TENANCY_AUDIT.md` and nine more
  are in that directory). It was untracked and stays untracked; whether its
  findings are still open is a question for whoever produced it, and that is
  recorded in the plan's `## Debt`.

---

### What is clean, with the numbers

- **The checkpoint is real.** Every command in it re-executed by the reviewer, including
  `make integration` and a `make api` round-trip; the `96 insertions(+), 2 deletions(-)` figure
  matches to the line.
- **Zero exported surface added by S6.** Six files, all `_test.go` plus one shell array
  element and Markdown.
- **Zero kernel diffs for a second store.** `grep -rn "eventmemory\|eventpg\|eventtest"
  event/*.go | grep -v _test.go` → **0**; `go list -f '{{join .Imports "\n"}}' ./event | grep
  frostgrove` → `crud`, `errs`, and nothing else. D-121's dependency claim (`go list -deps
  ./event/...` → stdlib + `crud` + `errs` + `utils`) verified exactly.
- **Every ADR's factual claims spot-checked against the code and hold**: `jobs/queue.go:808`
  really is `normalizeSenderError` and really reads `err.(rejectedPlacement)` (D-122);
  `event/errors.go` declares exactly **24** `Err*` sentinels (D-121, D-122, FL-036);
  `event/outcome.go` declares exactly the seven named outcomes (D-122); `encodable.go` is
  **379** lines and **20** functions against `jobs/json.go` **1407** and `cache/codec.go`
  **1564** (D-124); `Compose`'s escape set is read through `text.go`'s own `invalidRune` /
  `controlRune` predicates with upper-case hex, as D-125 states.
- **Every symbol FL-036 names exists.** All 50 checked, including the ones declared as types
  rather than functions (`link`, `word`, `over`, `narrowed`, `policing`, `unconfirming`,
  `wrapping`, `failure`, `probe`).
- **`en`/`ru` module-doc parity is exact** for all three pages: identical heading counts and
  identical inline-code table keys.
- **Every index row the plan promised is present**: five decision rows plus the "Event
  sourcing" area paragraph, the UC-032 rows in both usecase indexes, the three module rows in
  both `docs/modules/{en,ru}/Index.md`, and both flow index tables.
- **`UC-032` obeys the linking rule** — no file path, no package name, no symbol in its body.
- **Both roadmap supersessions are in place, not rewritten**, with the reason beside each.
- **No forbidden shortcut** in any S6 file: no `t.Skip`, no `nolint`, no `TODO`, no loosened
  assertion, no fixture path referenced from a non-test file.
- **Each of the six new checks carries a fixture-driven falsification subtest** that passes,
  and all six run in `make unit`. The three findings above are about what those fixtures do
  **not** contain, not about their absence.

---

## Round 1 — dispositions, 2026-09-07

Twelve findings, twelve closed. Two criteria are answered differently and argued
above rather than met silently: GAP-2's per-language *checked*-count arm (the
`ru` tree has no delivery-scoped occurrence, so the arm as written is red on
arrival) and GAP-4's enforceable reverse-index check (185 unrowed files across
twelve other subsystems, so it is either red repo-wide or fitted to `event/`).
GAP-11(c) is rejected outright: five ADRs shipped and "five" is the right word.
Three items are carried into the plan's `## Debt` rather than into the code —
the repo-wide reverse index, the moved audit file, and the two occurrences the
Russian arm rests on.

Every check this round rewrote was broken against the real tree, watched go red,
and restored; the reports are quoted in the plan's anti-vacuity table. Section
checkpoint re-executed in full: `go test -count=1 ./scripts/...`, `make check`
(nine arms), `make unit` (0 FAIL), `make vet`, `gofmt -l .` silent, `make api`
leaving the surface diff at **96 insertions and 2 deletions** — unchanged,
because every file this round touched is a `_test.go`, a Markdown page or a
shell array element — `go build ./...`, `go vet ./event/...`,
`go test -race ./event/...`, and `make integration` twice in a row, both green.

---

## Round 2 — econv-impl-reviewer (clean context, re-audit after round 1's fixes) — 2026-09-07

Read in full: `CLAUDE.md`, the plan's § S6 (`:4476-4890`), § Microkernel classification,
§ Architecture metrics, § Debt, `.agents/artifacts/usecases/EVENTSOURCE_P1_USECASES.md`
(§INV-007, §INV-011, §INV-013, §INV-019, §INV-025, §INV-027, §INV-036),
`/home/user/.claude/skills/econv/references/{microkernel,architecture,building-blocks,
data-integrity,universality,readability,restrictions,gaps}.md`, round 1 of this file in
full, and every file S6 touched or added:
`scripts/{checks.sh,event_test.go,extensions_test.go,tenancy_test.go,docs_test.go}`,
`event/{sources,fixtures,mutablestate,refusalmessages,transactioncontrol,store,refusal}_test.go`,
`docs/ai/decisions/D-121…D-125` + `Index.md`, `docs/ai/flows/FL-036` + both index tables,
`docs/ai/usecases/modules/event/UC-032` + two index rows,
`docs/modules/{en,ru}/{event,eventmemory,eventtest}.md` + both `Index.md`s, both roadmaps,
and the `event/…` sections of `docs/api/surface.md`.

**Every round-1 finding was re-verified by falsifying the shipped check against a scratch
copy of the tree (`/tmp/fw`), not by reading its disposition.** Twenty-one probe files were
written, run and removed.

### Checkpoint S6 — re-executed by this reviewer

| Command | Result |
|---|---|
| `go test -list '^(…six scripts names…)$' ./scripts/ \| grep -c '^Test'` | **6** ✓ (`LIST-SCRIPTS OK`) |
| `go test -list '^(…three event names…)$' ./event/ \| grep -c '^Test'` | **3** ✓ (`LIST-EVENT OK`) |
| `go test -count=1 ./scripts/...` | `ok github.com/frostgrove/vv/scripts 5.264s` |
| `go test -race -count=1 ./event/...` | `ok event 4.881s` · `ok eventmemory 1.050s` · `ok eventtest 2.528s` |
| `make check` | nine arms, all `ok` — deps, tiers, utils, triplets, todo, replaces, tidy, otel-schema, workspace |
| `make unit` | 0 `FAIL`, 0 panics across every module |
| `make vet` | every module, no diagnostics |
| `gofmt -l .` | silent |
| `make api` against a saved copy | `docs/api/surface.md` **byte-identical** — already regenerated |
| `git --no-pager diff --stat docs/api/surface.md` | `96 insertions(+), 2 deletions(-)` — matches the plan's paste exactly |
| `make integration` ×2 | both green: `test/bridge 1.005s`, `test/codegen 2.907s`, `test/dsn 1.012s`, `test/integration 19.29s`, `test/portmount 1.06s`, `test/versionstore 1.006s` — 0 `FAIL`, and the two `vvdb` pool failures the plan records as pre-existing are **not** failing here either |

**The checkpoint output pasted into the plan at `:4340-4394` is real**, including the
4.881 s / 4.854 s `event` timing the round-1 rewrite cost.

### The eight previously-blocking findings, re-verified by falsification

| Round 1 | How I falsified the shipped fix | Result |
|---|---|---|
| GAP-1 (§INV-011 read spellings) | added `fmt.Errorf("%w: %s", ErrKey, fmt.Sprintf("%s", key))` to `event/probe.go`; separately `string(stream.Key)` to `event/eventmemory/probe.go` | both reported — `probe.go:11:56 in probeSprintf: a Key is rendered into a refusal…` and `eventmemory/probe.go:10:79`. **`event/eventmemory` is genuinely in scope**, and the floor reads **120** messages against a floor of 60. Residual escape: **GAP-13** |
| GAP-2 (doc check read one language) | appended an English promise **under a `## What this is not` heading with a trailing "so you do not need an inbox table"** to `docs/modules/en/event.md`; then a Russian one to `docs/modules/ru/event.md` | both reported: `…/en/event.md:244` and `…/ru/event.md:248`. The negation-attached-to-the-claim model works in both languages |
| GAP-3 (§INV-013 read two spellings) | ran the shipped fixture; instrumented the floor | `registry`, `guard`, `counter`, `seen`, `pool`, `tally` all reported, `sentinel` not; **39** package-level names read against a floor of 24. Residual escapes: **GAP-14** |
| GAP-4 (FL-036 file table incomplete) | `comm -23 <(find event -name '*.go' ! -name '*_test.go' \| sort) <(grep -oE '`event/[^`]+\.go`' docs/ai/flows/Index.md \| tr -d '`' \| sort -u)` | **47 of 50** rowed; the 3 remaining are `event/testdata/crossings/{change,control,identity}/*.go`. See **GAP-19** |
| GAP-5 (checks miss a nested module) | created a real throwaway module `event/eventtmp` with its own `go.mod` and added it to `go.work` | `the extension has 4 packages and 2 of them say what they cost` + `…/eventtmp … says nothing about what it costs`. Removed from `go.work`: `cannot list [./...] in ../event/eventtmp: … directory prefix . does not contain modules listed in go.work` — loud, not silent |
| GAP-6 (event walk dropped tenancy's env/global arm) | the same throwaway module carried `var home = os.Getenv("HOME")`, `func init()` and `go func(){}()` | all three reported in one run, from `startsNothing`/`startsSomething` in the **shared** file |
| GAP-7 (transaction control matched selectors) | added a free `func commit() {}` and a caller to `event/probe.go` in the real kernel | `probe.go:5:28 in probeIssuesCommit: commit is called…`. Residual escape: **GAP-16** |
| GAP-9 (dead branch, shadowed name) | read `scripts/event_test.go:29-32` | `charged map[string]bool`; no `costOf`, no `seams`, no `named` shadow. Closed |

GAP-8 and GAP-10 to GAP-12 re-checked by reading: `sourcesIn` is at `event/sources_test.go:84`
and `store_test.go:85`, `refusal_test.go:108` and `:137` all call it (four bodies became one);
the plan now reads "summary row 15" (`:4575`) and "twenty-two triggers" (`:4631`);
`GAPS.md` is gone from the root and `.agents/artifacts/audit/NAIVE_CONTRACTS_AUDIT.md` exists.
GAP-8's disposition overstates its own closure — **GAP-20**.

### Contract conformance — plan vs code, both directions

| Plan promised (round-1 contract changes 6–9) | Shipped | Verdict |
|---|---|---|
| C6 — the three `event/` checks resolve **types** behind one file set, one importer, a memoised package | `event/sources_test.go:33-50` — `typedGuard`/`typedPositions`/`typedImporter`/`typedCache`; `importer.ForCompiler(…, "source", nil)`, stdlib only | ✓ |
| C6 — §INV-011 decided from four named types plus a byte slice; a type with its own `String`/`Error` decides its own rendering | `identityNamed` `:265-278`, `reachedIdentity` `:284-321`, `rendersItself` `:323-325`; `Stream` is the asserted control (`read.delegated == 0` fatals) | ✓ |
| C6 — §INV-013 takes the kind from the type; a per-package vacuity floor | `heldKind`/`writtenThrough` `:80-113`; `len(names) == 0` fatals **per directory** at `:32-34`, plus a total floor of 24 | ✓ |
| C6 — §INV-007/§INV-027 case-blind over methods **and** free functions; `Append` is the store's only when its receiver answers `ReadStream`; recursion is a third arm | `controlCalled` `:89-96` (`strings.ToLower`), `onALog` `:129-141` (`types.LookupFieldOrMethod(…, "ReadStream")`), `retriesItself` `:143-166` | ✓ |
| C7 — the doc check is a per-language table with a blindness arm | `deliveryWordings` `docs_test.go:768-787`, blindness at `:802-804`, fixture control `TestAPromiseOfExactlyOnceDeliveryIsReportedAndARefusalOfOneIsNot` | ✓ shipped, residual **GAP-18** |
| C8 — the walking both extensions share moves to `scripts/extensions_test.go`: `listedIn`, `firstPartyDependenciesIn`, `modulesUnder`, `publishedModules`, `packagesUnder`, `coversEveryDirectory`, `noBaseSubsystemDependsOn`, `startsNothing`, `initialiserCalls` | all nine present, `scripts/extensions_test.go` 44 → 380 lines, `scripts/tenancy_test.go` 151 → 66 | ✓ |
| C8 — "the two extension files keep their test names and **their bodies are one line each into the shared walker**" (`:4780`) | true of **two of the three** tests; the cost test is still a 36-line body in `event_test.go` and a 40-line one in `tenancy_test.go`, and they have already diverged | ⚠ **GAP-15** |
| C9 — `[[FL-036]]` names every file under `event/` and so does the reverse index | 47 of 50; 3 `testdata` files unrowed against the flow's own sentence | ⚠ **GAP-19** |
| `scripts/checks.sh` — `event` added to `SUBSYSTEMS`, "that is the whole edit" | `:18` `SUBSYSTEMS=(crud auth port remote storage app tenancy event)`; `git diff Makefile` empty | ✓ exact |
| Exported non-test surface added by S6 | **0** — the only `^(func\|type\|var\|const) [A-Z]` lines in the seven new files are `func Test…` and text inside the raw-string fixtures | ✓ |

### Architecture metrics — counted, not eyeballed

| Metric | Value | Threshold |
|---|---|---|
| Lines per new file | `extensions_test.go` **380** · `refusalmessages_test.go` **374** · `fixtures_test.go` **199** · `transactioncontrol_test.go` **166** · `mutablestate_test.go` **165** · `sources_test.go` **143** · `event_test.go` **67** | ≤ 400 ✓ |
| Lines per **edited** file | `scripts/docs_test.go` **1055** (736 before S6, +319) | ≤ 400 — **breach, pre-existing, worsened: GAP-21** |
| Longest function | `TestNoRefusalRendersAnIdentityAPayloadAKeyOrACursor` **41** · `reachedIdentity` **38** · `renderedValues` **36** · `TestNoEventPackageCostsMoreThanTheSeamItNames` **36** · `mutationsOf` **32** · `controlComplaints` **32** | ≤ 50 ✓ |
| Nesting depth (control statements, closures counted as a new function) | `renderedValues` 3 · `mutationsOf` 3 · `controlComplaints` 3 · `noBaseSubsystemDependsOn` 3 · **`TestNoEventPackageCostsMoreThanTheSeamItNames` 4** (`for`→`if`→`for`→`if`, `event_test.go:38-48`) | ≤ 3 — **one breach: GAP-20** |
| Function parameters | max **4** (`judgeRendering`) | ≤ 4 ✓ |
| Boolean flag parameters | **2** — `reachedIdentity(…, whole bool)`, `startsSomething(…, underTest bool)` | 0 — **GAP-17** |
| Exported symbols added (non-test) | **0** | ✓ |
| First-party imports per new file | **0** in all seven | ≤ 5 ✓ |
| Import cycles | 0 — `go list -f '{{join .Imports "\n"}}' ./event \| grep frostgrove` → `crud`, `errs`, and nothing else | ✓ |
| Global mutable state added to a **non-test** path | **0** — every new file is `_test.go`; `typedCache` is a test cache behind `typedGuard` | ✓ |
| Fakes to unit-test each new object | **0** — every check is a function over `go/ast`/`go/types` plus a `t.TempDir()` fixture; `listedIn` shells out to the toolchain (1 real dependency) | ≤ 2 ✓ |
| Modules edited for this section | **2** (`scripts/`, `docs/`) | ≤ 3 ✓ (the plan's metrics row says 3 including `Makefile` — see GAP-20) |
| Forbidden shortcuts (`t.Skip`, `nolint`, `// type: ignore`, `TODO`, loosened assertion) | **0**; the one changed assertion in `docs_test.go` was **tightened** (`checked == 0` → `checked < 1000`) | ✓ |

### Microkernel purity — the commands and the results

```
$ grep -rn "eventmemory\|eventpg\|eventtest" event/*.go | grep -v _test.go | wc -l
0
$ go list -f '{{join .Imports "\n"}}' ./event | grep frostgrove
github.com/frostgrove/vv/crud
github.com/frostgrove/vv/errs
$ go list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' ./crud ./errs
crud · errs · utils          # the whole allow-list TestNoEventPackageCosts… enforces
```

The kernel names no concrete store and branches on none. **A second store costs `event/`
zero diffs**; what it costs outside is one row in `scripts/event_test.go:29-32` (`charged`),
which is deliberate, argued in the comment above it, and I confirmed by adding a real nested
module and watching the count arm name it. Purity of the phase-2 path is proved
independently by `event/eventtest/suite_test.go:TestATrivialStoreNeedsNoInternalAccess`,
which is `package eventtest_test` and so can reach only exported API.

Purity of rehydration: `go list -f '{{join .Imports "\n"}}' ./event` shows no `net/*`, no
`os`, no `math/rand`, no `time` beyond the `Envelope` field type, and
`grep -rn "port\.\|Logger" event/*.go event/eventmemory/*.go` (non-test) returns only
`this.log.…` field access — nothing writes to a logger, nothing reads a clock or the
environment inside a fold or an upcast.

---

### GAP-13 [medium][immediate] §INV-011's check has no dataflow, so one intermediate `string` local defeats it — four real leaks demonstrated

- **Where:** `event/refusalmessages_test.go:183-217` (`judgeRendering`), `:228-238`
  (`renderedFrom`), `:160-181` (`buildsAMessage`); the limit is admitted in the header
  comment at `:33-34` ("A string built into a variable and wrapped a statement later is the
  shape this does not follow") and nowhere in `[[FL-036]]` or `[[D-122]]`, which cite this
  test as the proof of §INV-011.
- **What:** the mechanism is now correctly type-driven, but it judges only the expression
  that appears **as an argument of the message constructor**. A value assigned to a local of
  type `string` one statement earlier has lost its type, and `renderedFrom` never follows an
  identifier back to its assignment. Four probes written into the real `event/` package and
  run through the shipped check:

  ```go
  detail := string(key);                     fmt.Errorf("%w: %s", ErrKey, detail)   // not reported
  detail := fmt.Sprintf("%s at %d", key, at); fmt.Errorf("%w: %s", ErrKey, detail)  // not reported
  text := "event: the cursor " + string(cursor); fmt.Errorf("%w: %s", ErrKey, text) // not reported
  body := string(raw);                       fmt.Errorf("%w: %s", ErrKey, body)     // not reported
  ```

  `go test -run TestNoRefusalRendersAnIdentityAPayloadAKeyOrACursor ./event/` → `ok`. The
  same four written inline are all reported, so the check is one assignment away from blind.
  `fmt.Sprintf` in particular is invisible by construction: `buildsAMessage` requires the
  call's result to implement `error`, and `Sprintf` returns a `string`.
- **Why this severity:** medium, not high. The mechanism is general (four named types plus a
  byte slice, everything else derived), the escapes need a second statement, and I probed the
  real tree: `event/` and `event/eventmemory` contain **no** message built through a local
  today, so nothing is leaking now. The concrete future failure is phase 2: `eventpg` will
  write refusals over driver errors, `detail := string(stream.Key)` is the natural spelling
  when a message needs two pieces, and the leak lands in a log line that carries a customer
  identifier while `[[FL-036]]:359` still says the invariant is proved.
- **Why this timing:** immediate — `[[D-122]]` and `[[FL-036]]` name this test as the guard
  for a stated invariant that phase 2 adds sites under, and a limit that lives only in a code
  comment is a limit the next author will not know about.
- **Close criteria:**
  - [ ] the check follows a local of type `string` back to its single assignment inside the
        same function body (an `*ast.AssignStmt` with one LHS `*ast.Ident` whose object is a
        function-local `*types.Var`) and judges the RHS with the same rules
  - [ ] or, if that is refused as too much machinery, `buildsAMessage` also reads any call
        returning a `string` whose result reaches an error constructor in the same function,
        and the refusal of the wider rule is written into `[[FL-036]]`'s
        *What the four structural checks reach* section rather than into a code comment
  - [ ] the four probes above are added to `event/fixtures_test.go:renderingFixture` and each
        is asserted reported
  - [ ] whichever is chosen, `[[FL-036]]` and `[[D-122]]` state the residual blind spot in
        prose, so the doc no longer claims more than the test delivers
- **Status:** open

---

### GAP-14 [medium][immediate] §INV-013's check misses two real shapes of shared mutable state — a value mutated through its own method, and an interface-typed holder

- **Where:** `event/mutablestate_test.go:80-113` (`heldKind`/`writtenThrough`), `:115-146`
  (`mutationsOf`), `:148-165` (`rootObject`)
- **What:** the kind arm reads the declared type and the mutation arm reads assignments,
  increments and `delete` whose **root object** is a package-level name. Neither sees:

  ```go
  type probeState struct{ n int }        // ordinary kind — heldKind returns ""
  var probeHeld probeState               // package-level state
  func (this *probeState) bump() { this.n++ }   // rootObject is the receiver, not probeHeld
  func ProbeBump() { probeHeld.bump() }  // not reported

  var probeAny any = map[string]int{}    // underlying is an interface — heldKind returns ""
  ```

  Written into the real `event/` package: `go test -run TestNoPackageLevelStateIsEverMutated
  ./event/` → `ok`. Both are package-level values every request shares and every caller
  writes through — the first is the ordinary shape of a registry (`func (r *registry)
  add(...)`), which is precisely what §INV-013 exists to refuse.
- **Why this severity:** medium. The tree is clean today (39 package-level names read, all
  sentinels and reflected type tokens), and neither shape appears in `event/`,
  `event/eventmemory` or `event/eventtest`. The cost is that the file's own comment at `:10-22`
  claims more than the code does — "a check that reads two of those four spellings passes the
  other two" is exactly what the pointer-receiver shape now does to the type-driven version.
- **Why this timing:** immediate — `event/eventpg` will hold a prepared-statement cache or a
  dialect table, and the natural Go spelling of both is a package-level value with methods.
  The check is what stops it, and it does not.
- **Close criteria:**
  - [ ] a mutation whose root object is a **method receiver** is attributed to every
        package-level value that method is reachable on, or, more cheaply, a package-level
        value of a named type that declares any pointer-receiver method is reported by the
        kind arm with a message naming that method
  - [ ] `writtenThrough` reports an `*types.Interface` underlying type — a package-level
        value that can hold anything can hold something writable
  - [ ] both shapes are added to `event/fixtures_test.go:mutableFixture` and asserted reported,
        and the fixture keeps a control that a package-level `error` sentinel (which is also
        an interface) is **not** reported
- **Status:** open

---

### GAP-15 [medium][immediate] the cost arm of the two extension graph tests is still duplicated, and the two copies have already diverged — one reaches a nested module, the other does not

- **Where:** `scripts/event_test.go:28-63` vs `scripts/tenancy_test.go:23-62`; the rule they
  break is stated in `scripts/extensions_test.go:15-21` ("What is walked is shared, because
  two copies of one prohibition drift and then answer the same question two ways"); the plan
  claims the opposite at `:4780` ("their bodies are one line each into the shared walker").
- **What:** round 1's C8 extracted nine helpers and reduced two of the three tests to one
  line each (`noBaseSubsystemDependsOn`, `startsNothing`). The third — the cost test — was
  not: `TestNoEventPackageCostsMoreThanTheSeamItNames` is a 36-line body and
  `TestNoTenancyPackageCostsMoreThanTheSeamItNames` is a 40-line body, structurally identical
  (count against the table, loop, core branch, uncharged branch, `t.Run` with an allow-list),
  and they differ in exactly the thing GAP-5 was about:

  | | event | tenancy |
  |---|---|---|
  | how packages are listed | `packagesUnder(t, …)` — module-aware, `coversEveryDirectory` floor | `listed(t, "{{.ImportPath}}", "./tenancy/...")` — a `go list` pattern, which stops at a nested `go.mod` |
  | how a package's deps are read | `firstPartyDependenciesIn(t, found.module, found.path)` | `firstPartyDependencies(t, "."+strings.TrimPrefix(path, …))` — root module only |

  So the day `tenancy` grows a module the tenancy cost test silently measures the tree without
  it, which is the failure the shared file was created to prevent, reintroduced in the one
  test that was not moved. The **table** being per-extension is deliberate and argued; the
  25 lines of walk around the table are not.
- **Why this severity:** medium — no production code, and the event side is correct today. It
  is on the list because a written rule and the code beside it disagree, and because the plan
  records the disagreement as resolved.
- **Why this timing:** immediate — phase 2 adds `event/eventpg` as a nested module and edits
  exactly this test; a `charged` row added to a walk that is *not* the shared one is how the
  divergence becomes permanent.
- **Close criteria:**
  - [ ] the walk is one function in `scripts/extensions_test.go` — `costsNoMoreThan(t, prefix,
        root, charged map[string]string_or_bool, contracts []string)` or equivalent — and both
        extension files pass their own table into it, keeping their own test names and their
        own messages
  - [ ] `TestNoTenancyPackageCostsMoreThanTheSeamItNames` therefore reaches a nested module,
        proved the way the event one was: a throwaway module under `tenancy/` is reported
  - [ ] the plan's `:4780` sentence is corrected, or the refusal to share this third body is
        written down with its argument
- **Status:** open

---

### GAP-16 [low][immediate] the transaction-control check misses a method value, so `finish := tx.Commit; finish()` passes

- **Where:** `event/transactioncontrol_test.go:89-96` (`controlCalled`), `:98-116`
  (`calledName`)
- **What:** the arm reads the callee's own spelling. A method value bound to a local carries
  the method's name at the binding site, not at the call site:

  ```go
  func ProbeCommitThroughAMethodValue(tx probeHandle) { finish := tx.Commit; finish() }
  ```

  Written into the real `event/` package: `go test -run
  TestTheKernelNeverIssuesTransactionControlAndNeverRetries ./event/` → `ok`. The same
  omission covers `defer` of a bound method value and a commit passed as a `func()` argument.
  The check's own comment `:19-25` names mutual recursion as not seen and says nothing about
  this.
- **Why this severity:** low — an exotic spelling, and the kernel's seam declares no
  `Begin`/`Commit`/`Rollback` at all, so there is nothing in `event/` a method value could be
  bound to today. It is here because the arm's whole value is that it cannot be spelled around.
- **Why this timing:** immediate under the same rule as GAP-15 — it is the check `[[D-118]]`,
  `[[FL-036]]` and `docs/modules/en/event.md:190` cite for "the framework issues no begin, no
  commit and no rollback anywhere", and phase 2's store is where transaction control first
  becomes writable.
- **Close criteria:**
  - [ ] a `*ast.SelectorExpr` whose selection is a `types.MethodVal` and whose method name
        lowercases to `begin`/`commit`/`rollback` is reported wherever it appears, not only as
        the `Fun` of a `CallExpr`
  - [ ] the fixture gains `finish := tx.Commit; finish()` and asserts it reported
  - [ ] whatever is still not seen (mutual recursion, a commit reached through an interface
        value of another name) is listed in the check's comment as a closed list
- **Status:** open

---

### GAP-17 [low][deferred] two boolean flag parameters switch behaviour inside the new checks

- **Where:** `event/refusalmessages_test.go:284` (`reachedIdentity(held, seen, whole bool)`,
  branched at `:295`), `scripts/extensions_test.go:300` (`startsSomething(t, source, underTest
  bool)`, branched at `:323`)
- **What:** `readability.md:100` — "No boolean flag parameters that switch behaviour — two
  named functions." `whole` decides whether a type that renders itself is trusted; `underTest`
  decides whether the goroutine arm runs at all. At the call sites (`carriedIdentity` passes
  `true`, every recursive call passes `false`; `startsNothing` passes
  `slices.Contains(found.imports, "testing")`) the reader cannot tell what the boolean means
  without opening the callee.
- **Why this severity:** low — both are test scaffolding, both bodies are short, and neither
  is on a public surface.
- **Why this timing:** deferred — module-internal, no contract turns on it.
- **Close criteria:**
  - [ ] `reachedIdentity` splits into `carriedIdentity(held)` and `identityInside(held, seen)`,
        or the flag becomes a named type whose two values read at the call site
  - [ ] `startsSomething` splits into the declaration arm (always run) and a
        `startsNoGoroutine(t, source)` the caller runs for a linked package only
- **Status:** open

---

### GAP-18 [low][deferred] the delivery-promise table matches one English phrasing, so "once and only once" and "no duplicates" pass

- **Where:** `scripts/docs_test.go:768-787` (`deliveryWordings`), specifically
  `promise: exactly.?once` and the Russian `ровно один раз|ровно однажды|строго один раз|точно
  один раз`
- **What:** round 1's C7 replaced three fitted literals with a per-language table carrying a
  negation model, which is the right shape and is verified working (I reported both an English
  and a Russian promise from the real tree). What remains is that the `promise` cell is one
  English phrasing. `A projector sees each event once and only once`, `no duplicates ever reach
  your handler` and `deduplicated for you` are the same claim in words the row does not carry,
  and `docs/modules/en/event.md:233` is where a future author would write one.
- **Why this severity:** low — a documentation lint over prose, where no regex is complete;
  the blindness arm makes a row that matches *nothing* loud, and the shipped tree is clean.
- **Why this timing:** deferred — nothing depends on it and no contract turns on it.
- **Close criteria:**
  - [ ] the English `promise` cell carries `once and only once` and a de-duplication
        vocabulary alongside `exactly.?once`, with the Russian row extended in step
  - [ ] the fixture gains one of each new phrasing and asserts it reported
- **Status:** open

---

### GAP-19 [low][deferred] `[[FL-036]]` says every non-test `.go` file under `event/` has a row, and three do not

- **Where:** `docs/ai/flows/FL-036-a-decision-becomes-a-recorded-fact.md:276-278`;
  the three files are `event/testdata/crossings/{change/change.go,control/control.go,
  identity/identity.go}`
- **What:** the flow states "Every non-test `.go` file under `event/` has a row above,
  `doc.go` included". `comm -23` over `find event -name '*.go' ! -name '*_test.go'` against
  the reverse index returns exactly those three. They are compile-refusal fixtures the
  toolchain ignores, so the *intent* of the sentence is met — but the sentence as written is
  falsifiable by a one-line `find`, and it is the sentence GAP-4 was closed on.
- **Why this severity:** low — no code and no check depends on it; the three files are
  reachable from `event/crossings_test.go`, which the flow's *Tests that walk this flow*
  section does name.
- **Why this timing:** deferred.
- **Close criteria:**
  - [ ] the sentence says "every non-test `.go` file the toolchain builds" and names
        `event/testdata/` as the carve-out, **or** the three files get rows
- **Status:** open

---

### GAP-20 [low][deferred] three claims in the plan's own metrics and dispositions are false against the tree

- **Where:** `.agents/artifacts/plans/EVENTSOURCE_P1_PLAN.md:5186`;
  `.agents/artifacts/gaps/EVENTSOURCE_P1_S6_GAPS.md:606` (GAP-8's disposition);
  `.agents/artifacts/plans/EVENTSOURCE_P1_PLAN.md:4780` (counted separately as GAP-15)
- **What:** (a) the metrics row reads "Modules edited for one feature, threshold 3 | 3 in S6
  (`scripts/`, `docs/`, `Makefile`)"; `git diff Makefile` is empty and the S6 bullet itself
  says "no `Makefile` `COMMANDS` row" — the real count is 2, which is better than claimed.
  (b) GAP-8's disposition says "No S6 file exceeds a nesting depth of 3";
  `scripts/event_test.go:38-48` nests `for` → `if` → `for` → `if`, four control levels in a
  36-line function, against `architecture.md`'s ≤ 3. The shape is inherited verbatim from
  `scripts/tenancy_test.go:36-51`, so it is a copy of an existing breach rather than a new
  idea — which is GAP-15 again from the other side.
- **Why this severity:** low — the tree is better than (a) claims and the (b) breach is one
  test body with two-line innermost statements.
- **Why this timing:** deferred — nothing reads these two lines for behaviour.
- **Close criteria:**
  - [ ] the metrics row says 2 and names `scripts/` and `docs/`
  - [ ] either the nest is flattened when GAP-15's shared walker is extracted (it disappears
        with it), or the plan's metrics table gains an S6 nesting row that states the breach
        and its argument
- **Status:** open

---

### GAP-21 [low][deferred] S6 grew an already-over-budget file by 43 %, and tightened one floor without updating its message

- **Where:** `scripts/docs_test.go` — 736 lines before S6, **1055** after (`git diff --stat`:
  +319); the floor at `:528-530`
- **What:** two small things in one file. (a) `architecture.md`'s "Lines per file ≤ 400" is
  breached at 1055; the breach pre-dates S6, and S6 added the largest single block in it
  (`deliveryWordings`, `deliveryClaims`, `block`/`paragraphsOf` and the fixture — 319 lines
  that are about one subject and would read as `scripts/delivery_test.go`). (b) the symbol
  floor was correctly tightened from `checked == 0` to `checked < 1000`, but the message still
  reads "no citation named a file this tree holds, so nothing here was checked" — which is
  false for any `0 < checked < 1000`, and the tree currently checks **3818**, a number that
  appears nowhere in the source to say where 1000 came from.
- **Why this severity:** low — no behaviour, and the floor's direction is right.
- **Why this timing:** deferred.
- **Close criteria:**
  - [ ] the delivery arm moves to its own file under 400 lines, or the plan records why
        `docs_test.go` stays one file
  - [ ] the floor's message states the count it read and the count it wanted, the way
        `refusalmessages_test.go:46` and `mutablestate_test.go:41` already do
- **Status:** open

---

### GAP-22 [low][deferred] the "a package a program links" predicate is answered two ways, one of them by searching file text

- **Where:** `event/sources_test.go:121-143` (`linkedDirectories`, `strings.Contains(string(content), "\"testing\"")`)
  vs `scripts/extensions_test.go:277` (`slices.Contains(found.imports, "testing")`)
- **What:** the same derived predicate — round 1's C6/C2 argument that "a package that imports
  `testing` cannot be in a production binary" — is computed from `go list`'s import list in one
  place and from the raw bytes of the source in the other. The textual one matches the string
  `"testing"` wherever it appears: a doc comment, a string constant, a struct tag. A false
  positive silently drops a package **out of** the §INV-011 scope, which is the fail-open
  direction. The `len(linked) < 2` floor at `:139-141` is what stops it becoming vacuous, and
  it does stop it — with three directories, dropping either of the two linked ones trips the
  floor loudly.
- **Why this severity:** low — the floor makes the failure loud rather than silent, and the two
  packages cannot share a helper (different Go packages, both test-only).
- **Why this timing:** deferred.
- **Close criteria:**
  - [ ] `linkedDirectories` reads the parsed file's `Imports` (it already parses every file
        through `typedSources`) instead of the file's text
  - [ ] or the floor is raised to "every directory `extensionDirectories` found, minus the ones
        that parse an import of `testing`", so the two answers cannot differ
- **Status:** open

---

### GAP-23 [low][deferred] the regenerated surface baseline carries eleven lines that are not `event`'s

- **Where:** `docs/api/surface.md`, `git --no-pager diff docs/api/surface.md`
- **What:** the plan calls the regenerated file "the phase-1 baseline **a person reads the
  diff of**". Of the 98 changed lines, 87 are the three `event/…` sections; the other eleven
  are `cache`'s eight new `Option` constructors (`FreshFor`, `JitterBy`, `NoNegative`,
  `OnCorruption`, `OnInvalidateFailure`, `OnReadFailure`, `RetainFor`, `RetainUntilEvicted`),
  `jobs.ListCursor`, and the removal of `jobs.ScheduleOverlap`/`AllowOverlap` — all in-flight
  work from other branches of this working tree, not S6's. A person reading the diff as the
  phase-1 event baseline has to know to ignore them, and nothing in the plan or `[[D-121]]`
  says so.
- **Why this severity:** low — the file is explicitly a report and never a gate
  (`CLAUDE.md`), and every `event/…` line in it is correct.
- **Why this timing:** deferred.
- **Close criteria:**
  - [ ] the plan's S6 entry (or `[[D-121]]`'s surface paragraph) names the eleven non-`event`
        lines and their origin, so the next reader of that diff is not counting them as the
        vocabulary's
- **Status:** open

---

## Round 2 — verdict

**No open `[critical][immediate]` and no open `[high][immediate]` findings.** All eight
previously blocking items (GAP-1 to GAP-7, GAP-9) are genuinely closed — each verified by
breaking the shipped check against a scratch copy of the real tree and watching it go red,
not by reading its disposition. The section's checkpoint reproduces exactly, including
`make integration` twice in a row.

The eleven new findings are the residue of good fixes: three checks that are now type-driven
rather than spelling-driven still have named blind spots (GAP-13, GAP-14, GAP-16), one of the
three shared walks was not actually shared (GAP-15), and the rest are prose, metrics and
citation hygiene. Nothing here makes the shipped `event/` code wrong on any input — I probed
the real tree for every escape I found and it is clean of all of them today.

---

## Round 2 — dispositions, 2026-09-07 (S6 phase 5)

Eleven findings. Five closed in code, one closed in part with its residual
written into a doc, five carried into the plan's `## Debt`.

| Finding | Disposition |
|---|---|
| GAP-13 `[medium][immediate]` §INV-011 has no dataflow | **closed** — `localText` collects the `string` locals a declaration writes into and `textFlow.from` judges an argument that is one of them as what was written into it. All four probes are in `renderingFixture` and asserted reported. Residual — a string reaching a message through a struct field, a package-level value or another function's return — is in `[[FL-036]]`, not in a code comment |
| GAP-14 `[medium][immediate]` §INV-013 misses two shapes | **closed** — `writtenThroughItsOwnMethod` reports a package-level value of a named type declaring any pointer-receiver method, and `interfacesHolding` judges an interface by what was written into it. An interface naming no method is reported by the kind arm. Both shapes are in `mutableFixture`; the `error` sentinel, the `reflect.Type` token and a value-receiver type are the controls, and neither is exempted by name |
| GAP-15 `[medium][immediate]` the cost arm was not shared | **closed** — `costsNoMoreThanItNames` in `scripts/extensions_test.go`; each extension passes its own table and its own three sentences. Proved the way the event one was: a throwaway `tenancy/tenancytmp` module in `go.work` is reported by `TestNoTenancyPackageCostsMoreThanTheSeamItNames` |
| GAP-16 `[low][immediate]` a method value passed | **closed** — `controlBound` reports a `MethodVal` selector and a free function referenced in value position, with `calleesOf` keeping an ordinary call from being reported twice. `finish := tx.Commit; finish()` written into the real kernel is reported. What is still not seen is a closed list in the check's comment and in `[[FL-036]]` |
| GAP-17 `[low][deferred]` two boolean flag parameters | **closed** — `startsSomething` split into `startsBeforeMain` and `startsAGoroutine`; `reachedIdentity(…, whole bool)` split into `carriedIdentity`, `identityInside` and `identityOfAPart` |
| GAP-18 `[low][deferred]` one English phrasing | **closed in part** — `once and only once`, `one time and one time only` and «один и только один раз» are in the table with fixture lines asserted reported. The de-duplication vocabulary is **refused with the measurement**: `deduplicat` appears eleven times in `docs/` as prose about a mechanism, twice inside delivery vocabulary, and `no duplicates ever reach your handler` carries its own negation, so the negation model cannot classify it. In the plan's `## Debt` and in `[[FL-036]]` |
| GAP-8, GAP-10, GAP-11, GAP-12 | closed or carried in round 1's dispositions, unchanged |
| GAP-19 … GAP-23 `[low][deferred]` | carried into the plan's `## Debt` — the reverse index is the same repo-wide task as GAP-4, and the rest is prose, metrics and citation hygiene |

**What phase 5 added beyond closing the findings**, because a check with no
in-suite falsification is a check whose deleted arm goes unnoticed:
`scripts/extensionwalk_test.go` drives the three shared walks over a written
tree; `TestTheStructuralChecksReadOneTypedPackageFromManyGoroutines` proves the
three analysers are pure over the one memoised typed package they share;
`event/formatverbs_test.go` reads a format in `fmt`'s own order and is
differentially fuzzed against `fmt`, which found and fixed a **real** defect —
`%+[2]s`, `%-*d`, `%.*f` and `%6.*f` were read verb by verb, so an argument
index or a star behind a flag mis-mapped every judgement after it; and
`FuzzADeliveryClaimIsReportedAtALineTheDocumentHas` pins that the markdown walk
reports a line the document has. Every one of them was broken against the real
tree, watched go red and restored; the reports are in the plan's S6 phase-5
table.
