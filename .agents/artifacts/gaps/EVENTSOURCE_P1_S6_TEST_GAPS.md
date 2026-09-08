# EVENTSOURCE_P1 — S6 structural checks, documentation and the five ADRs — TEST GAPS

## Round 1 — econv-test-reviewer (clean context) — 2026-09-08

Read in full before touching anything: `CLAUDE.md`,
`.agents/artifacts/plans/EVENTSOURCE_P1_PLAN.md` (§ S6, § Coverage matrix, § Risks and
refusals, § Debt), `.agents/artifacts/usecases/EVENTSOURCE_P1_USECASES.md` (§INV-007,
§INV-011, §INV-013, §INV-019, §INV-025, §INV-027, §INV-036),
`/home/user/.claude/skills/econv/references/{restrictions,universality,architecture,
building-blocks,data-integrity,gaps,microkernel,readability}.md`,
`docs/ai/flows/FL-036-a-decision-becomes-a-recorded-fact.md`, `docs/ai/decisions/D-121…D-125`,
and every file S6 owns: `event/{sources,fixtures,refusalmessages,renderedtypes,mutablestate,
transactioncontrol,formatverbs}_test.go`,
`scripts/{event,extensions,extensionlisting,extensionwalk,tenancy,docs}_test.go`,
`scripts/checks.sh`.

### Baseline, re-executed rather than read

| Command | Result |
|---|---|
| `go build ./...` · `go vet ./event/... ./scripts/... ./utils/...` · `gofmt -l event scripts utils docs` | clean, silent |
| `go test -race -count=1 ./event/...` | `event` **4.91 s**, `event/eventmemory` **1.05 s**, `event/eventtest` **2.52 s** |
| `go test -count=1 ./scripts/...` | **5.25 s** |
| `go test -race -count=3 ./event/...` · `go test -count=3 ./scripts/` | green, no flake |
| `-shuffle=on`, three runs of `./event/` and two of `./scripts/` | green, no order dependence |
| Checkpoint `-list` clauses (6 in `./scripts/`, 3 in `./event/`) | both hold |
| `-fuzz FuzzAFormatIsMappedVerbByVerbOrRefusedWhole -fuzztime 40s` | 3 748 435 execs, clean |
| `-fuzz FuzzADeliveryClaimIsReportedAtALineTheDocumentHas -fuzztime 40s` | 1 804 692 execs, clean |
| corpus artifacts left in `event/testdata` / `scripts/testdata` | none |
| `comm -23 <(find event -name '*.go' ! -name '*_test.go') <(grep -o 'event/…\.go' docs/ai/flows/Index.md)` | only the three `event/testdata/crossings/*` fixtures, which is correct |

Line budget: 342, 104, 242, 216, 182, 197, 281 (`event/`), 51, 272, 213, 112, 46 (`scripts/`) —
all under 400, as the plan claims. `scripts/docs_test.go` is 1 112, the stated pre-existing
exception.

The tree was restored after every mutation and verified byte-for-byte against a
`sha256sum` baseline of all 61 `.go` files in `event/` and `scripts/`; `git status` is
identical to the state at the start of the round. Three files outside those two directories
were touched transiently and restored from a clean index
(`event/repo.go`, `event/eventmemory/{append,log}.go`, `event/eventtest/declaration.go`,
`utils/optional.go`, `utils/vvflag/vvflag.go`, `docs/ai/flows/FL-036…md`); all six were
clean before the round and are clean now.

### Coverage of the section's matrix rows

| Row | Test that would fail on regression | Verdict |
|---|---|---|
| INV-007 (never retries) | `TestTheKernelNeverIssuesTransactionControlAndNeverRetries` | covered; positive control run — a `for attempt := 0; …` around `this.store.Append` in `event/repo.go` reported `repo.go:97:13 in Append: the store's Append is called inside a loop…` |
| INV-027 (no transaction control) | same | covered; all six control shapes falsified against the fixture |
| INV-011 (no identity in a refusal) | `TestNoRefusalRendersAnIdentityAPayloadAKeyOrACursor` + `TestAFormatThatShiftsItsOwnArgumentsIsRefusedRatherThanReadWrongly` + `FuzzAFormatIsMappedVerbByVerbOrRefusedWhole` | covered for the direct shapes; **two one-hop escapes are live in the real tree** — GAP-5 |
| INV-013 (no package-level mutable state) | `TestNoPackageLevelStateIsEverMutated`, `TestMerelyImportingTheEventExtensionStartsNothing`, `TestALifecycleAPackageStartsForItselfIsReportedAndTheIdiomIsNot` | covered against the kernel; **the walked set is not pinned** — GAP-2 |
| INV-019 (baseline is a report, not a gate) | none, by design (`CLAUDE.md`) | correct, no gap |
| INV-036 (delivery is at least once) | `TestNoDocPromisesExactlyOnceDelivery`, `TestAPromiseOfExactlyOnceDeliveryIsReportedAndARefusalOfOneIsNot`, `FuzzADeliveryClaimIsReportedAtALineTheDocumentHas` | covered for the wordings; **one documented half of the refusal window does not exist** — GAP-3, and one arm is untested — GAP-4 |
| C6 GAP-105, C10.6 | `TestEverySymbolTheDocsCiteIsDeclaredWhereTheDocSaysItIs`, `TestNoDocPromisesExactlyOnceDelivery` | covered; positive control run — a renamed citation in `FL-036` was reported at `FL-036…:341` |
| §9 documentation obligations | D-121…D-125 + index rows, FL-036 + both index tables, UC-032 + two index rows, six module pages + `en/Index.md:117-119` and `ru/Index.md:128-130` | all present |

Nothing in the section's suite is scope creep: every test maps to a matrix row.

---

### GAP-T1 [high][immediate] the cost walk is the one graph check with no falsification, and four of its arms can be deleted with the suite green

- **Where:** `scripts/extensions_test.go:35-72` (`costsNoMoreThanItNames`),
  `scripts/extensionlisting_test.go:45-53` (`firstPartyDependenciesIn`); the file that closed
  this same defect for the other three walks is `scripts/extensionwalk_test.go:1-112`.
- **What:** phase 5 added `scripts/extensionwalk_test.go` because "the three graph checks had
  **no** in-suite falsification; a deleted arm went unnoticed" (plan § S6, phase-5 table).
  There are four shared walks, not three. `crossingsInto`, `uncoveredDirectories` and
  `startsBeforeMain`/`startsAGoroutine` are each driven over a written fixture. The fourth —
  the cost walk that holds [[D-116]] for **both** extensions — is driven over nothing.
  Measured, one mutation at a time, whole suite `go test -count=1 ./scripts/` after each:

  | Mutation | Suite |
  |---|---|
  | `costsNoMoreThanItNames` body replaced by `return` | **ok 4.955 s** |
  | the `uncharged` arm (`if !charged { t.Error(…) }`) deleted | **ok 5.377 s** |
  | the `overreach` arm neutered (`|| true` in its skip condition) | **ok 5.471 s** |
  | the `core` reach arm neutered the same way | **ok 5.493 s** |
  | `firstPartyDependenciesIn` returns an empty map | **ok 4.906 s** |

  The check does work today — positive control: `_ "github.com/frostgrove/vv/jobs"` added to
  `event/eventmemory/log.go` reported three lines including
  `…/event/eventmemory reaches github.com/frostgrove/vv/jobs, and its row here says it costs
  the vocabulary and nothing else`. What is missing is the test that keeps it working.
- **Why this severity:** `TestNoEventPackageCostsMoreThanTheSeamItNames` and
  `TestNoTenancyPackageCostsMoreThanTheSeamItNames` are the only things that hold the cost
  table. With any one of those five edits in place, `event/eventmemory` growing an import of
  `jobs` — or a sixth `tenancy` package reaching `auth` — compiles, passes `make unit`,
  `make check` and `make vet`, and every consumer of that package silently carries a
  subsystem it never selected. That is exactly the failure [[D-116]] and the plan's
  optionality argument exist to prevent, and `make check-deps` cannot see it because an
  extension is first-party. It is also `gaps.md`'s "missing test for a stated invariant".
- **Why this timing:** phase 2 adds `event/eventpg`, whose row is the first one that will
  carry a non-empty allowance (`crud/adapter/crudsql`). The first edit to the walk that
  teaches it about a non-empty allowance is the edit that will silently break an arm, and by
  then there will be a third extension file written beside these two.
- **Close criteria:**
  - [x] `costsNoMoreThanItNames` splits reporting from walking the way the other three did —
        `costOverruns(t, tree, cost)` returns the complaints and the caller calls `t.Error`.
        The per-package `t.Run` is gone with it; the complaint sentences already name the
        package. The tree the walk reads is a parameter, so `packagesUnder`,
        `firstPartyDependenciesIn`, `extensionModules` and `modulesUnder` take it too and
        the repository is one caller of them rather than the only one. `listed` and
        `firstPartyDependencies`, dead since the round-1 rewrite, are removed.
  - [x] `TestAPackageCostingMoreThanItsRowSaysIsReportedAndOneCostingExactlyItIsNot` in
        `scripts/extensionwalk_test.go` writes a module tree — `contract`, `outside`, `seam`,
        `ext`, `ext/charged`, `ext/unnamed` — and reads it against two tables. The
        understated one reports all four sentences (`uncharged` on `ext/unnamed`,
        `overreach` on `ext/charged` reaching `seam`, `core` on the core reaching `outside`,
        and the package count), and nothing else: the complaint count is asserted at four.
        The stated one — contracts `./contract` and `./outside`, `ext/charged` charged
        `./seam`, `ext/unnamed` charged nothing — is silent.
  - [x] All five mutations turn the new test red, and no other test in `./scripts/` moves:

        | Mutation | The new test says |
        |---|---|
        | `costOverruns` returns no complaint | `the fixture's "the extension has 3 packages and 1 of them say what they cost" was not reported` |
        | the `uncharged` arm deleted | `the fixture's "github.com/x/ext/unnamed says nothing about what it costs" was not reported` |
        | the `overreach` arm neutered (`if true \|\|`) | `the fixture's "github.com/x/ext/charged reaches github.com/x/seam" was not reported` |
        | the `core` arm neutered the same way | `the fixture's "the core reaches github.com/x/outside" was not reported` |
        | `firstPartyDependenciesIn` returns an empty map | `the fixture's "the core reaches github.com/x/outside" was not reported` |

- **Status:** closed

---

### GAP-T2 [high][immediate] nothing pins which directories the two `event/` source checks walk, and a real `event/eventmemory` violation ships green under a plausible narrowing

- **Where:** `event/sources_test.go:155-197` (`extensionDirectories`, `linkedDirectories`),
  read by `event/mutablestate_test.go:36-53` and `event/refusalmessages_test.go:37-55`.
- **What:** the only guards on the walked set are three counting floors — `len(directories)
  < 3`, `declared < 24`, `len(linked) < 2` — and all three are satisfied by a set that
  contains the kernel and nothing else. Measured:

  | Mutation | Suite |
  |---|---|
  | `extensionDirectories` returns `[".", ".", "."]` (the kernel three times; `eventmemory` and `eventtest` never read) | `go test -race ./event/` **ok 0.62 s** |
  | the same, **with a real violation planted** — `var probeShared = map[string]int{}` and `probeShared[name]++` in `event/eventmemory/log.go` | **ok** — the violation ships |
  | `linkedDirectories`' `"testing"` predicate replaced by a string that never matches (scope silently widens to `event/eventtest`) | **ok 0.67 s** |

  Positive control, checks intact: the same planted map is reported twice —
  `eventmemory/log.go:96:5: probeShared is a map, which every copy of it writes through…`
  and `:98:35: probeShared is incremented or decremented`. So the scope is right today and
  nothing holds it there.

  Two further mechanisms make the narrowing easy to reach by accident. `linkedDirectories`
  decides "this package is linked into a program" with
  `strings.Contains(string(content), "\"testing\"")` over raw file bytes — a comment or a
  string literal containing the quoted word drops the package out of the §INV-011 walk
  (verified: a `// a note mentioning the "testing" package` line added to `event/store.go`
  drops the kernel and only the `< 2` floor catches it). And `scripts/extensions_test.go:146`
  decides the same predicate from `go list`'s `.Imports` — two implementations of one rule in
  two packages, which is the drift the plan says it removed between the tenancy grep and the
  event walk.
- **Why this severity:** §INV-013 is a replica-safety invariant: a package-level map in
  `event/eventmemory` is state every request in every replica shares. FL-036 states as
  delivered that the check "walks all three packages, including the suite", and
  `event/eventtest` is where a shared registry is most natural to write. A refactor of
  `extensionDirectories` that reduces it to the kernel — filtering directories with no
  non-test source, an inverted `entry.IsDir()`, a `_`-prefixed name rule — leaves every
  assertion in the section green while §INV-011 and §INV-013 stop being proved for two of
  the three packages. `gaps.md`: "missing test for a stated invariant".
- **Why this timing:** phase 2 adds `event/eventpg` as a fourth directory. The floors are
  constants (`< 3`, `< 2`) that do not grow, so from that point a store dropping out of scope
  is invisible even without a mutation — three directories still satisfy `< 3` and two linked
  packages still satisfy `< 2`. The floors have to be derived before the fourth package
  exists, not after.
- **Close criteria:**
  - [x] `TestTheChecksWalkEveryPackageTheExtensionHoldsAndLinkAllButTheSuite`
        (`event/sources_test.go`) compares the walked set against
        `directoriesHoldingSource`, a second reading of the tree that is deliberately unlike
        the one it checks: `filepath.WalkDir` recursively, skipping `testdata`, dot- and
        underscore-prefixed directories, keeping any directory `sourcesIn` finds non-test
        source in. A nested `event/eventpg` module is a directory like any other to both, so
        the set extends with no edit; a package deeper than one level is in the derived set
        and not the walked one, which is the right question to be asked.
  - [x] The same test asserts the difference between the walked and the linked set is
        exactly `[eventtest]`, so a store dropping out of the §INV-011 walk is named rather
        than counted. The predicate is now the package's **import list** — `underTest` reads
        `file.Imports` off the already-parsed files — which is what `scripts/extensions_test.go`
        reads from `go list`'s `.Imports`, and two fixtures pin it: a package writing the
        quoted word in a comment and in a string constant is linked, one importing `testing`
        is not.
  - [x] Five mutations, one at a time, whole `./event/` suite after each:

        | Mutation | Result |
        |---|---|
        | `extensionDirectories` returns `[".", ".", "."]` | red — `the checks walk [. . .] and the extension holds source in [. eventmemory eventtest]` |
        | the same, with `var probeShared = map[string]int{}` planted in `event/eventmemory/log.go` | red — the scope test; and the plant alone, checks intact, is red on `TestNoPackageLevelStateIsEverMutated` |
        | the `testing` predicate never matches | red — `the refusal walk reads [. eventmemory eventtest] and leaves out []` |
        | the predicate returns to a scan of the raw bytes | red — `a package that writes the word testing in a comment and in a string was read as one a program does not link` |
        | a comment carrying the quoted word added to `event/store.go` | **green**, which is the point: the escape mutation 49 half-caught is closed |

- **Status:** closed

---

### GAP-T3 [high][immediate] `sentenceBefore` always returns the empty string, so half the documented refusal window does not exist and nothing notices

- **Where:** `scripts/docs_test.go:1036-1042` (`block.sentenceBefore`), read by
  `:1010-1016` (`around`, `attachedTo`); documented as delivered in
  `docs/ai/flows/FL-036…:` "*the sentence before it in the same paragraph*" and in the plan's
  contract change 7.
- **What:** `sentenceBefore(offset)` computes `from := sentenceFrom(offset)` and then returns
  `text[sentenceFrom(from-1):from]`. `from` is already positioned past the terminator and the
  space, so `sentenceFrom(from-1)` finds the same terminator and returns `from` again — the
  slice is always empty. Probed directly on three inputs:

  ```
  "Delivery is never exactly-once here. The broker delivers each event exactly once."
      sentenceBefore="" attachedTo=" The broker delivers each event exactly once" refused=false
  "We do not promise it.  The broker delivers each event exactly once."   sentenceBefore="" refused=false
  "No such guarantee! The broker delivers each event exactly once."       sentenceBefore="" refused=false
  ```

  Mutation: `sentenceBefore` replaced by `return ""` — `go test -count=1 ./scripts/`
  **ok 5.266 s**. Neither the fixture test nor the real-tree test nor the fuzz target moves.
  No fixture line exercises the branch: every refusal in `deliveryFixture` is either in the
  same clause as the claim or under a refusing heading.
- **Why this severity:** it breaks §INV-036's doc arm in both directions, and both are
  reachable with ordinary prose.
  - False **negative**, demonstrated: the page `The broker is what carries them. Each event
    arrives exactly once.` yields `claims=[] checked=0 counted=1` — the delivery vocabulary
    sits in the previous sentence, `around()` is documented to include it and does not, so
    the promise is never even counted as checked. That is a page promising exactly-once
    delivery shipping green.
  - False **positive**: a page that refuses the claim in the sentence before and then states
    it is reported red. The plan names that outcome as R6 ("a structural check that goes red
    on a correct page gets loosened on its first run") and believes it was avoided.
  It is also an honesty defect: the plan and FL-036 both record a window that was never
  delivered.
- **Why this timing:** `FL-036` and `[[D-121]]`'s *Proven by* are the text phase 2 will
  extend for `eventpg`'s pages, and the two module pages already written are read against a
  model that does not behave as the doc says.
- **Close criteria:**
  - [x] `sentenceBefore` backs over the space run to the terminator before the sentence it
        was given and takes `sentenceFrom` of *that*, so the slice is the previous sentence.
        `TestTheSentenceBeforeAClaimIsTheOneThePagePutsThere` asserts it over the three probe
        strings above and over a paragraph with no preceding sentence.
  - [x] `deliveryFixture` gains `window.md`. Line 3 —
        *"The broker is what carries them. Each event arrives exactly once."* — carries the
        delivery vocabulary only in the preceding sentence and is reported; line 5 —
        *"We never promise it. The broker delivers each event exactly once."* — carries the
        refusal only there and is asserted **not** reported. `sentenceBefore` → `return ""`
        turns the test red twice: `window.md:3` stops being reported and `window.md:5` starts.
  - [x] Re-run over the real tree, the widened window read two more occurrences (12 → 14
        checked). One is `docs/ai/decisions/D-118…:88`, a genuine delivery paragraph whose
        refusal is in the preceding sentence — correctly silent, and the best evidence the
        window works. The other was reported: `docs/ai/usecases/modules/codegen/Codegen.md:748`
        wrote *"Every requested type must be found exactly once"* one sentence after the word
        "consumer". **The page is fixed, not exempted** — it now reads *"Every requested name
        must match exactly one declaration"*, which is what the sentence meant; nothing was
        added to the wording table and no name was excused.

- **Status:** closed

---

### GAP-T4 [medium][immediate] the paragraph splitter's list-and-table arm decides verdicts and is untested

- **Where:** `scripts/docs_test.go:984` (`if current == nil || markdownList.MatchString(written)
  || strings.HasPrefix(written, "|")`).
- **What:** mutation — the arm reduced to `if current == nil` — `go test -count=1 ./scripts/`
  **ok 5.5 s**, whole suite. The arm is not decorative: over the page

  ```md
  # Delivery

  - The projector delivers each event exactly once
  - It does not order streams
  ```

  the shipped splitter reports `page.md:3`; the mutant reports nothing, because the second
  bullet's "does not" lands inside the first bullet's clause window.
- **Why this severity:** a `docs/` page that promises exactly-once delivery in a bullet whose
  neighbour happens to contain a negation is excused, `make unit` stays green, and §INV-036 is
  violated in the text a consumer reads. Local to the doc arm, no runtime contract, hence
  medium.
- **Why this timing:** the fixture is being extended anyway for GAP-T3; adding the two-bullet
  case there costs nothing now and is a separate edit later.
- **Close criteria:**
  - [x] `window.md` lines 7–8 are the two-item list (promise, then a bullet carrying "does
        not") and lines 10–11 the two table rows, the same way.
  - [x] Three mutations, each red on
        `TestAPromiseOfExactlyOnceDeliveryIsReportedAndARefusalOfOneIsNot`: the arm reduced
        to `if current == nil` loses `window.md:7` **and** `window.md:10`; removing
        `markdownList` alone loses `:7`; removing the `|` prefix alone loses `:10`.

- **Status:** closed

---

### GAP-T5 [medium][deferred] §INV-011's disclosed one-hop escapes are live in the real tree and no test pins the residual

- **Where:** `event/refusalmessages_test.go:290-342` (`textFlow.from`, `fromCall`);
  disclosed in the file comment at `:33-35` and in `FL-036` ("*a string reaching a message
  through a struct field, a package-level value or another function's return*").
- **What:** both escapes were written into the real `event/eventmemory/append.go` and the
  suite stayed green:

  | Written into the real store | `go test ./event/` |
  |---|---|
  | `detail := struct{ where string }{where: string(req.Stream.Key)}` then `fmt.Errorf("%w: stream %s", errStreamMoved, detail.where)` | **ok 0.57 s** |
  | `whereItFailed(req)` where `func whereItFailed(req event.AppendRequest) string { return string(req.Stream.Key) }` | **ok 0.62 s** |

  Both ship a customer's stream key into a refusal message. The direct spellings are caught
  (control: `req.Stream.Key` and `req.Expected` rendered directly reported two lines at
  `eventmemory/append.go:42:98` and `:42:114`). The second shape — a helper taking the request
  value and rendering a part of it — is the ordinary shape a driver refusal is written in, and
  is what `eventpg` will write.
- **Why this severity:** it is disclosed rather than hidden, which is why it is not high; but
  §INV-011 is a data-exposure invariant and the residual is one line wide. Additionally no
  test pins the residual: there is no case asserting that the local-dataflow arm *does* follow
  a local while a struct field is knowingly not followed, so a future narrowing of `localText`
  is invisible too (that half is caught — `localText` returning an empty map turns the fixture
  red — but the boundary between the two is not stated in a test).
- **Why this timing:** phase 2's `eventpg` is where the shape appears in production code; the
  residual is recorded in `FL-036` and belongs in `## Debt` until then.
- **Close criteria:**
  - [ ] `renderingFixture` gains `renderedThroughAStructField` and
        `renderedThroughAHelperTakingTheWholeRequest`, asserted in whichever list matches the
        decision taken — reported if the arm is extended to a struct field and a one-level
        callee, or asserted **not** reported with the reason, so the boundary is a test rather
        than a paragraph.
  - [ ] `FL-036`'s residual paragraph names the test that pins it.

- **Status:** deferred — moved to `## Debt` in
  `.agents/artifacts/plans/EVENTSOURCE_P1_PLAN.md`, unchanged and with its owner named. The
  residual is disclosed in `FL-036` and in the file comment at
  `event/refusalmessages_test.go:33-35`; the shape it lets through is the one `eventpg` will
  write, so the fixture pair belongs in the section that writes it.

---

### GAP-T6 [medium][immediate] the four identity names are matched by string and nothing ties them to the vocabulary `event` declares

- **Where:** `event/renderedtypes_test.go:20-24` (`case "Key", "Cursor", "Version",
  "Position"`), against `event/identity.go:8-14`.
- **What:** `identityNamed` decides an identity from `named.Obj().Name()` — the spelling —
  with no reference to the types declared in the package under test, and the fixture
  (`event/fixtures_test.go:30-33`) declares its own `Key`, `Cursor`, `Version` and `Position`.
  The list is therefore proved against a copy of itself. Renaming the list turns the fixture
  red, which reads as coverage and is not: renaming `event.Cursor` to `event.Resume` in the
  kernel would leave both subtests green while §INV-011 stops covering the cursor.
- **Why this severity:** the four names *are* the domain vocabulary, which `universality.md`
  allows as constant — but only "declared in one place". They are declared in two, with
  nothing linking them, so the check drifts from the vocabulary silently. It is a duplication
  with a correctness consequence, not a fit-to-one-example hardcode, hence medium rather than
  the universality law's high.
- **Why this timing:** phase 2 adds a store whose refusals carry a position and possibly a
  sequence; the list is the thing that has to grow with it, and there is nothing to make that
  visible when it does not.
- **Close criteria:**
  - [x] The four names are one list, `identitiesARefusalMayNotRender`, which `identityNamed`
        reads; `TestEveryIdentityTheChecksNameIsOneTheVocabularyDeclaresAndEveryOneItDeclaresIsNamed`
        looks each of them up in the kernel's own scope. Renaming `Cursor` to `Resume`
        everywhere but the list is red: *"a refusal is refused Cursor and the vocabulary
        declares no such type"*.
  - [x] The complement walks the scope: every exported, non-alias defined type whose
        underlying type is a string or an unsigned integer must be in the list or in
        `identitiesNamedForSomethingElse`, which carries `Outcome` and `Support` with the
        reason each is not an identity. Adding `type Sequence uint64` to the vocabulary is
        red naming `Sequence`; an exemption left behind the type it exempted is red too.

- **Status:** closed

---

### GAP-T7 [medium][deferred] S6's only `scripts/checks.sh` deliverable has no test, and the function it edits fails open

- **Where:** `scripts/checks.sh:18` (`SUBSYSTEMS=(… tenancy event)`), `:116-139`
  (`check_utils`); `scripts/checks_test.go` covers `check-tidy`, `check-replaces` and
  `check-deps` and nothing else.
- **What:** the `event` element works — control: `utils/vvflag/vvflag.go` importing
  `github.com/frostgrove/vv/event` gives `a package under utils/ imports a subsystem — it is
  not a utility (D-058): crud, event`, exit 1 — but nothing in the suite asserts it, so
  removing `event` from the array is green everywhere. Separately, `check_utils` fails open
  when `utils/` does not build: the `if ! dependencies=$( { go list …; while …; done } 2>&1 )`
  takes its status from the `while` loop, not from `go list`, so the error text lands in
  `$dependencies` and the anchored grep finds nothing. Demonstrated: `utils/optional.go`
  importing `event` is an import cycle (`event` → `crud` → `utils`) and
  `bash scripts/checks.sh utils` prints `check-utils: ok`, exit 0. Every subsystem import from
  the root `utils` package takes that path, so the boundary S6 added `event` to is
  unenforceable for its most likely violation shape.
- **Why this severity:** the fail-open is pre-existing and outside S6's edit; the missing test
  is S6's, and the deliverable is one array element. No runtime contract is affected.
- **Why this timing:** neither blocks phase 2. Both belong in `## Debt`.
- **Close criteria:**
  - [ ] `scripts/checks_test.go` gains a `check-utils` case on the model of the `check-deps`
        cases: a written fixture whose `utils/` package imports a subsystem is refused, and one
        that does not is accepted.
  - [ ] `check_utils` reports a failing `go list` as a failure — the status taken from the
        `go list` itself — with a fixture whose `utils/` tree does not build asserted red.

- **Status:** deferred — moved to `## Debt` in
  `.agents/artifacts/plans/EVENTSOURCE_P1_PLAN.md`. The fail-open is pre-existing and outside
  S6's edit, and both halves are a `scripts/checks_test.go` change with no bearing on phase 2.

---

## Mutation log — every mutation attempted, and whether the suite caught it

Each mutation was applied alone, the named package's suite run, and the file restored from a
byte-exact backup before the next one. **Caught** means at least one test went red.

### The three `event/` source checks — analyser mutations

| # | Mutation | Caught | Reported by |
|---|---|---|---|
| 1 | `identityNamed` returns `""` for every type | yes | the tree floor (`no message renders a value that carries an identity through its own String…`) **and** the fixture (`renderedByItsOwnMethod was not reported`) |
| 2 | `localText` returns an empty map (local dataflow gutted) | yes | fixture — `renderedThroughALocalConversion was not reported` |
| 3 | `buildsAMessage` returns `false` | yes | `0 messages were read out of the packages a program links, so this walked the wrong files` |
| 4 | `writtenThroughItsOwnMethod` returns `""` | yes | fixture — `kept was not reported` |
| 5 | `mutationsOf` returns `nil` | yes | fixture — `counter was not reported` |
| 6 | `interfacesHolding` returns `nil` | yes | fixture — `rendered was not reported` |
| 7 | `controlBound` returns `""` | yes | fixture — `commitsThroughABoundMethod was not reported` |
| 8 | the free-function arm of `controlBound` removed | yes | fixture — `commitsThroughAHandedOffFunction was not reported` |
| 9 | `retriesItself` returns `false` | yes | fixture — `retriesByRecursion was not reported` |
| 10 | `appendsOnALog` returns `nil` (the loop arm) | yes | fixture — `retriesInALoop was not reported` |
| 11 | the `goto` arm disabled | yes | fixture — `jumpsBack was not reported` |
| 12 | `onALog` drops the `ReadStream` requirement (over-reporting) | yes | the control half — `appendsToABuilder was reported` |
| 13 | a package-level map written inside `renderedValues` | yes | `WARNING: DATA RACE` under `-race`, and `TestTheStructuralChecksReadOneTypedPackageFromManyGoroutines` red |
| 14 | `extensionDirectories` returns `[".", ".", "."]` | **no** — GAP-T2 | — |
| 15 | #14 **plus** a real `map[string]int` at package level in `event/eventmemory` | **no** — GAP-T2 | — |
| 16 | `linkedDirectories`' `"testing"` predicate never matches | **no** — GAP-T2 | — |

### The format-verb reader

| # | Mutation | Caught | Reported by |
|---|---|---|---|
| 17 | `shifted` returns `false` (`%[2]s`, `%*d` read instead of refused) | yes | five table rows |
| 18 | the precision `shifted` check removed (`%.*f`) | yes | two table rows **and** fuzz seed #4 |
| 19 | the width `digitsFrom` skipped | yes | three table rows (`%8T` mapped as `8`) |
| 20 | `%%` treated as a verb | yes | one table row **and** two fuzz seeds |

### The delivery-claim walk

| # | Mutation | Caught | Reported by |
|---|---|---|---|
| 21 | `refuses` returns `true` always | yes | `the fixture writes 7 promises and [] came back` |
| 22 | the `about` delivery-scope filter removed | yes | 40+ real docs pages reported |
| 23 | the refusing-heading arm removed | yes | tree **and** fixture |
| 24 | the negation window widened to the whole paragraph | yes | fixture — six of seven promises lost |
| 25 | `lineAt` returns `number + 1000` | yes | fixture **and** four fuzz seeds |
| 26 | the Russian promise/weaker wordings made unmatchable | yes | the blindness arm — `Russian is written in this tree and neither of its two wordings … was read once` |
| 27 | the Russian `written` detector made unmatchable | yes | fixture |
| 28 | `paragraphsOf` loses its list-and-table arm | **no** — GAP-T4 | — |
| 29 | `sentenceBefore` returns `""` | **no** — GAP-T3 | — |

### The four `scripts/` graph walks

| # | Mutation | Caught | Reported by |
|---|---|---|---|
| 30 | `crossingsInto` returns `nil` | yes | `TestAnImportOfTheExtensionFromOutsideItIsReportedAndOneInsideItIsNot` |
| 31 | the `init` arm of `startsBeforeMain` disabled | yes | `TestALifecycleAPackageStartsForItselfIsReportedAndTheIdiomIsNot` |
| 32 | `uncoveredDirectories` returns `nil` | yes | `TestADirectoryHoldingSourceThatNoPackageListedIsReported` |
| 33 | `publishedModules` returns only the root | yes | `only 1 published modules were found…` on both extensions |
| 34 | `startsNothing`'s `testing` predicate never matches | yes | `event/eventtest/sections_write.go:273:3 starts a goroutine` |
| 35 | `costsNoMoreThanItNames` gutted | **no** — GAP-T1 | — |
| 36 | its `uncharged` arm deleted | **no** — GAP-T1 | — |
| 37 | its `overreach` arm neutered | **no** — GAP-T1 | — |
| 38 | its `core` reach arm neutered | **no** — GAP-T1 | — |
| 39 | `firstPartyDependenciesIn` returns an empty map | **no** — GAP-T1 | — |
| 40 | `noBaseSubsystemDependsOn`'s `len(edges) < 80` floor disabled | no (a floor only fires when the walk is already broken; noted, not a finding) | — |
| 41 | `packagesUnder`'s prefix filter removed | no (a no-op: every `go list` pattern is already scoped; noted, not a finding) | — |

### Real violations written into the shipped tree (positive controls)

| # | Written | Caught |
|---|---|---|
| 42 | `fmt.Errorf("%w: stream %s at version %d", errStreamMoved, req.Stream.Key, req.Expected)` in `event/eventmemory/append.go` | yes — two lines, a `Key` and a `Version` |
| 43 | the same key routed through a one-field struct local | **no** — GAP-T5 |
| 44 | the same key routed through `whereItFailed(req)` | **no** — GAP-T5 |
| 45 | `var probeShared = map[string]int{}` + `probeShared[name]++` in `event/eventmemory/log.go` | yes — two lines |
| 46 | `for attempt := 0; attempt < 3; attempt++` around `this.store.Append` in `event/repo.go` | yes — `repo.go:97:13 in Append: the store's Append is called inside a loop…` |
| 47 | `func init() {}` and `go func() {}()` in `event/repo.go` | yes — both lines |
| 48 | `func init() {}` in `event/eventtest/declaration.go` | yes — the `init` arm is unconditional over all three packages, as documented |
| 49 | a comment containing the quoted word `"testing"` in `event/store.go` | yes today, by the `< 2` floor only — GAP-T2 |
| 50 | `_ "…/jobs"` imported by `event/eventmemory` | yes — three lines including `jobs` |
| 51 | a renamed test citation in `docs/ai/flows/FL-036…` | yes — `…:341 and no _test.go declares it` |
| 52 | `_ "…/event"` imported by `utils/vvflag` | yes — `check-utils` exit 1, `crud, event` |
| 53 | `_ "…/event"` imported by `utils/optional.go` (an import cycle) | **no** — `check-utils: ok`, exit 0 — GAP-T7 |

### What is good, and worth saying

- **Doubles discipline is exemplary.** Every falsification runs the real analyser over real Go
  source written into a temp directory and type-checked by the same path the tree takes. There
  is no mock of anything the project owns and no assertion on a configured double.
- **The control halves are present and load-bearing.** Mutation 12 was caught only because
  `appendsToABuilder`, `pagesARead`, `permittedFamily`, `sentinel`, `typeToken` and `frozen`
  are asserted **not** reported. That is the pattern `CLAUDE.md` asks for, applied
  consistently.
- **Failure messages name what broke in plain words** and, for the tree arms, carry the
  file:line a person opens. `the fixture's X was not reported, so the arm that would have
  found it proves nothing` is a better message than any `got != want` in this repository.
- **The two fuzz targets are real property tests**, not smoke tests: one differentially checks
  the verb reader against `fmt` itself, the other holds "a line a reader is sent to is a line
  the document has" over arbitrary bytes and two alphabets. Both survive a 40 s campaign.
- **Determinism is clean.** No clock, no randomness, no sleep, no network, no shared mutable
  fixture; `-race -count=3` and `-shuffle=on` green; the one concurrency test exists precisely
  to prove the three analysers are pure over the memoised typed package, and it catches a
  single map write.

---

## Round 1 — closure, 2026-09-08

Every `[high][immediate]` finding is closed, and both `[medium][immediate]` ones with them:
GAP-T1, GAP-T2, GAP-T3, GAP-T4 and GAP-T6 each carry the mutation that turns the new test
red in their close criteria above. GAP-T5 and GAP-T7 are `[deferred]` and are carried in the
plan's `## Debt` with their owners.

One page of `docs/` changed as a consequence rather than a test: the widened refusal window
reported `docs/ai/usecases/modules/codegen/Codegen.md:748`, and the sentence was rewritten to
say what it meant. Nothing was exempted, no wording row was dropped, and no name was excused.

The mutation log below is round 1's and is left as it was written. Rows 14, 15, 16, 28, 29
and 35–39 — the ten it records as **not** caught — are each caught now, by the tests named in
the close criteria; row 49's `< 2` floor no longer decides anything, because the predicate is
an import list. Rows 43, 44 and 53 are the two deferred findings and are still open.

## Round 1 — verdict

**Not green.** Three `[high][immediate]` findings are open: GAP-T1 (the fourth graph walk has
no falsification and five mutations of it are invisible), GAP-T2 (the walked set of the two
`event/` source checks is unpinned, and a real `event/eventmemory` violation ships green under
it), GAP-T3 (`sentenceBefore` is dead and the delivery-claim window is not the one the docs
describe, with a demonstrated false negative). GAP-T4 and GAP-T6 are `[medium][immediate]`;
GAP-T5 and GAP-T7 are `[medium][deferred]` and belong in the plan's `## Debt`.

---

## Round 2 — econv-test-reviewer (clean context, re-audit) — 2026-09-08

Read in full before touching anything: `CLAUDE.md`,
`.agents/artifacts/plans/EVENTSOURCE_P1_PLAN.md` (§ S6 including its phase-5 and phase-6
tables, § Coverage matrix, § Risks and refusals, § Debt),
`.agents/artifacts/usecases/EVENTSOURCE_P1_USECASES.md` (§INV-007, §INV-011, §INV-013,
§INV-019, §INV-025, §INV-027, §INV-036),
`/home/user/.claude/skills/econv/references/{restrictions,universality,architecture,
building-blocks,data-integrity,gaps,microkernel,readability}.md`,
`docs/ai/flows/FL-036-a-decision-becomes-a-recorded-fact.md`, `docs/ai/decisions/D-121…D-125`,
round 1 of this file, and every file S6 owns: `event/{sources,fixtures,refusalmessages,
renderedtypes,mutablestate,transactioncontrol,formatverbs}_test.go`,
`scripts/{event,extensions,extensionlisting,extensionwalk,tenancy,docs}_test.go`,
`scripts/checks.sh`.

### Baseline, re-executed rather than read

| Command | Result |
|---|---|
| `go test -race -count=1 ./event/...` | `event` **4.93 s**, `event/eventmemory` **1.05 s**, `event/eventtest` **2.51 s** |
| `go test -count=1 ./scripts/...` | **5.32 s** |
| `go test -race -count=3 ./event/...` | green — 5.64 s / 1.12 s / 5.55 s, no flake |
| `go test -count=3 ./scripts/` | green, 16.03 s, no flake |
| `-shuffle=on`, three runs of `./event/` and three of `./scripts/` | green, no order dependence |
| Checkpoint `-list` clauses (6 in `./scripts/`, 3 in `./event/`) | both hold |
| `make check` · `gofmt -l .` | nine arms green · silent |
| `make unit` | 0 FAIL (a transient `cache [build failed]` was **caused by this round** and repaired — see the honesty note below; not an S6 defect) |

### Round 1's five closures, re-verified by mutation rather than read

Every `[immediate]` finding round 1 recorded as closed was re-broken against the real tree.
All fourteen mutations are caught; none of the claims is unbacked.

| Round-1 finding | Mutation | Caught by |
|---|---|---|
| GAP-T1 | `costOverruns` returns `nil` | `TestAPackageCostingMoreThanItsRowSaysIsReportedAndOneCostingExactlyItIsNot` — *"the extension has 3 packages and 1 of them say what they cost" was not reported* |
| GAP-T1 | the `uncharged` arm deleted | same test — *"github.com/x/ext/unnamed says nothing about what it costs" was not reported* |
| GAP-T1 | the `overreach` arm neutered (`if true \|\|`) | same test — *"github.com/x/ext/charged reaches github.com/x/seam" was not reported* |
| GAP-T1 | the `core` arm neutered the same way | same test — *"the core reaches github.com/x/outside" was not reported* |
| GAP-T1 | `firstPartyDependenciesIn` returns an empty map | same test — *"the core reaches github.com/x/outside" was not reported* |
| GAP-T2 | `extensionDirectories` returns `[".", ".", "."]` | `TestTheChecksWalkEveryPackageTheExtensionHoldsAndLinkAllButTheSuite` — *the checks walk [. . .] and the extension holds source in [. eventmemory eventtest]* |
| GAP-T2 | the `"testing"` import predicate made unmatchable | same test — *the refusal walk reads [. eventmemory eventtest] and leaves out []* |
| GAP-T2 | `underTest` returned to a scan of the raw file bytes | same test — *a package that writes the word testing in a comment and in a string was read as one a program does not link* |
| GAP-T3 | `sentenceBefore` → `return ""` | `TestTheSentenceBeforeAClaimIsTheOneThePagePutsThere` **and** `TestAPromiseOfExactlyOnceDeliveryIsReportedAndARefusalOfOneIsNot` (`window.md:3` lost, `window.md:5` gained) |
| GAP-T4 | the splitter arm reduced to `if current == nil` | `…RefusalOfOneIsNot` — `window.md:7` **and** `window.md:10` lost |
| GAP-T4 | `markdownList` alone removed | same test — `window.md:7` lost |
| GAP-T4 | the `\|` prefix alone removed | same test — `window.md:10` lost |
| GAP-T6 | `type Sequence uint64` added to `event/identity.go` | `TestEveryIdentityTheChecksNameIsOneTheVocabularyDeclaresAndEveryOneItDeclaresIsNamed` — *the vocabulary declares Sequence as text or as a count and nothing here says whether a refusal may render it* |
| GAP-T6 | `Cursor` → `Resume` across every non-list occurrence in `event/` | same test, **both arms at once** — *a refusal is refused Cursor and the vocabulary declares no such type* and *the vocabulary declares Resume as text or as a count…* |

GAP-T5 and GAP-T7 remain `[deferred]` and are carried in the plan's `## Debt` with owners.
Neither was re-opened.

### Coverage of the section's matrix rows

| Row | Test that would fail on regression | Verdict |
|---|---|---|
| INV-007 (never retries) | `TestTheKernelNeverIssuesTransactionControlAndNeverRetries` | covered |
| INV-027 (no transaction control) | same | covered |
| INV-011 (no identity in a refusal) | `TestNoRefusalRendersAnIdentityAPayloadAKeyOrACursor`, `TestEveryIdentityTheChecksName…`, `TestAFormatThatShiftsItsOwnArgumentsIsRefusedRatherThanReadWrongly`, `FuzzAFormatIsMappedVerbByVerbOrRefusedWhole` | **the type walk's map, channel, array and all three pointer arms are undefended — GAP-T8** |
| INV-013 (no package-level mutable state) | `TestNoPackageLevelStateIsEverMutated`, `TestMerelyImportingTheEventExtensionStartsNothing`, `TestALifecycleAPackageStartsForItselfIsReportedAndTheIdiomIsNot` | **the kind walk's slice, channel and array arms are undefended — GAP-T8; and `startsNothing`'s own body is undefended — GAP-T9** |
| INV-019 (baseline is a report, not a gate) | none, by design (`CLAUDE.md`) | correct, no gap |
| INV-036 (delivery is at least once) | `TestNoDocPromisesExactlyOnceDelivery`, `TestAPromiseOfExactlyOnceDeliveryIsReportedAndARefusalOfOneIsNot`, `FuzzADeliveryClaimIsReportedAtALineTheDocumentHas`, `TestTheSentenceBeforeAClaimIsTheOneThePagePutsThere` | covered |
| C6 GAP-105, C10.6 | `TestEverySymbolTheDocsCiteIsDeclaredWhereTheDocSaysItIs`, `TestNoDocPromisesExactlyOnceDelivery` | covered; positive control run — renaming `TestTheSentenceBeforeAClaimIsTheOneThePagePutsThere` was reported by `TestEveryTestNameTheDocsCiteExists` at `FL-036…:359` |
| §9 documentation obligations | D-121…D-125, FL-036 + both index tables, UC-032, six module pages | all present; every one of the eleven phase-5/6 test names is cited in `FL-036` and therefore held by `TestEveryTestNameTheDocsCiteExists` |

Nothing in the section's suite is scope creep: every test maps to a matrix row. Doubles
discipline, determinism and assertion quality are as round 1 described them and are unchanged.

---

### GAP-T8 [high][immediate] six type-shape arms of the two `event/` source checks can be deleted with the whole suite green, and two real violations ship under them

- **Where:** `event/renderedtypes_test.go:93-116` (`identityInside`'s `*types.Map`,
  `*types.Chan` and `*types.Array` arms), `:26-38` (`identityNamed`'s pointer unwrap),
  `:76-90` (`carriedIdentity`'s pointer loop), `:121-136` (`identityOfAPart`'s pointer
  unwrap); `event/mutablestate_test.go:157-190` (`writtenThrough`'s `*types.Slice`,
  `*types.Chan` and `*types.Array` arms). The fixtures that would have to carry them are
  `event/fixtures_test.go:23-138` (`renderingFixture`) and `:140-203` (`mutableFixture`).
- **What:** both analysers are tables over the Go type kinds, and the two fixtures cover
  only the kinds the kernel happens to use today — a struct and a byte slice for §INV-011,
  a map, a `sync` type, a pointer, a named type with a pointer receiver and an empty
  interface for §INV-013. Every other row is decorative. Measured, one deletion at a time,
  whole `go test -count=1 ./event/` after each:

  | Arm deleted | Suite |
  |---|---|
  | `identityInside`'s `*types.Map` | **ok 0.71 s** |
  | `identityInside`'s `*types.Chan` | **ok 0.69 s** |
  | `identityInside`'s `*types.Array` | **ok 0.68 s** |
  | `identityNamed`'s pointer unwrap | **ok 0.67 s** |
  | `carriedIdentity`'s pointer loop | **ok 0.65 s** |
  | `identityOfAPart`'s pointer unwrap | **ok 0.69 s** |
  | `writtenThrough`'s `*types.Slice` | **ok 0.67 s** |
  | `writtenThrough`'s `*types.Chan` | **ok 0.79 s** |
  | `writtenThrough`'s `*types.Array` | **ok 0.67 s** |

  Each of the three files states in its own prose that these shapes are covered.
  `event/renderedtypes_test.go:10-13`: *"a struct carrying a `Key`, **a map keyed by one**,
  a slice of bytes, **a pointer to any of those**"*. `event/mutablestate_test.go:11-18`:
  *"a package-level **map, slice, channel** or pointer is state every request shares"*. The
  prose is ahead of the fixture in both files.
- **Why this severity:** the deletions are not hypothetical — each one lets a real violation
  through, demonstrated against the shipped tree:
  - §INV-011. Written into the real `event/eventmemory/append.go`:
    `func probePointer(req *event.AppendRequest) error { return fmt.Errorf("%w: %v", errStreamMoved, req) }`
    and `func probeKeyedMap(counts map[event.Key]int) error { return fmt.Errorf("%w: %v", errStreamMoved, counts) }`.
    With the checks intact both are reported (`append.go:68:46 in probePointer: a value
    carrying a Version…`, `:72:46 in probeKeyedMap: a value carrying a Key…`). With
    `carriedIdentity`'s pointer loop and `identityInside`'s map arm removed — two deletions a
    refactor of a nine-line type switch makes in one edit — `go test ./event/` is **ok
    0.84 s** and a customer's stream key and the aggregate's version travel in a refusal
    message. A refusal built over `*event.AppendRequest` is the ordinary shape a driver
    writes, and `eventpg` is where it will be written.
  - §INV-013. Written into the real `event/eventmemory/log.go`:
    `var probeSlots = make([]string, 8)` with
    `func probeFill(slots []string, name string) { slots[0] = name }` and
    `func probeUse(name string) { probeFill(probeSlots, name) }` — the package-level value
    written **only through a helper's parameter**, which is precisely the shape
    `event/mutablestate_test.go:11-18` says the kind arm exists for because `mutationsOf`
    cannot see it. Checks intact: reported (`log.go:96:5: probeSlots is a slice, which every
    copy of it writes through…`). `*types.Slice` arm removed: **ok 0.82 s**. That is
    process-wide shared state in a store, invisible in a unit test and a race between any two
    concurrent requests — the failure shape `CLAUDE.md` says this repository has already been
    bitten by.

  `gaps.md`: "missing test for a stated invariant", and both invariants are the
  data-exposure and replica-safety ones.
- **Why this timing:** phase 2's `eventpg` is where all three shapes arrive at once — a
  refusal built over a request pointer, a `map[Key]…` of staged rows, and a package-level
  slice of prepared statements. The fixture rows have to exist before the code that would be
  judged by them, not after; adding them later means the first `eventpg` review is the one
  that discovers the check never held.
- **Close criteria:**
  - [ ] `renderingFixture` gains a case per undefended arm, each asserted **reported**: a
        refusal rendering a `*Envelope` (pointer to a struct carrying a `Key`), one rendering
        a `map[Key]int`, one rendering a `map[string]Cursor`, one rendering a `[4]Version`
        and one rendering a `chan Position`. Deleting any one of the six arms turns
        `TestNoRefusalRendersAnIdentityAPayloadAKeyOrACursor`'s fixture subtest red, and the
        message names the case.
  - [ ] `mutableFixture` gains a package-level slice, a package-level channel and a
        package-level array of a mutable element, **each written only through a helper's
        parameter** so `mutationsOf` cannot answer for the kind arm, plus one control that
        stays silent (an array of a value nothing writes through). Deleting
        `writtenThrough`'s slice, channel or array arm turns
        `TestNoPackageLevelStateIsEverMutated`'s fixture subtest red.
  - [ ] The two prose paragraphs and `[[FL-036]]`'s statement of what the checks reach are
        true of the fixture, rather than ahead of it: every shape either has a fixture row or
        is written down as not reached.

- **Status:** open

---

### GAP-T9 [high][immediate] `startsNothing` and `noBaseSubsystemDependsOn` report through bodies nothing falsifies, and a real `init`, a real goroutine and a real base-subsystem import each ship green

- **Where:** `scripts/extensions_test.go:147-179` (`startsNothing`), `:93-109`
  (`noBaseSubsystemDependsOn`); the file that falsifies the *arms* they call is
  `scripts/extensionwalk_test.go:47-87`, which drives `startsBeforeMain`, `startsAGoroutine`
  and `crossingsInto` directly and never drives the two functions that decide whether their
  complaints reach `t.Error` at all.
- **What:** round 1's GAP-T1 found this exact shape in the fourth walk and closed it by
  parameterising `costOverruns` and driving it over a written tree. The same split was not
  made for the other two. Measured, one mutation at a time, whole `go test -count=1
  ./scripts/` after each:

  | Mutation | Suite |
  |---|---|
  | `startsNothing`'s `for _, complaint := range startsAGoroutine(…) { t.Error(…) }` replaced by `_ = startsAGoroutine` | **ok 5.26 s** |
  | the same for `startsBeforeMain` | **ok 5.30 s** |
  | both, so `startsNothing` walks the files and reports nothing | **ok 5.29 s** |
  | `noBaseSubsystemDependsOn`'s `for _, complaint := range crossingsInto(…) { t.Error(…) }` replaced by `_ = crossingsInto` | **ok 5.34 s** |
  | `underTest` narrowed from `slices.Contains(found.imports, "testing")` to that `\|\| len(found.files) < 12` | `TestMerelyImportingTheEventExtensionStartsNothing` **PASS** (only the tenancy test moved, on its own floor) |

  Each was run with a real violation present, so the finding is a shipped bug and not only a
  dead arm:
  - `func init() {}` and `go func() {}()` added to the real `event/eventmemory/log.go`.
    Checks intact, both are reported. With `startsBeforeMain` disconnected, only the
    goroutine is reported and the `init` ships. With both disconnected, `./scripts/` is **ok
    5.29 s** and a package-level `init` and a goroutine started at import time are both in
    `event/eventmemory`.
  - `_ "github.com/frostgrove/vv/event"` added to the real `cache/address.go`. Checks
    intact: `TestNoBaseSubsystemDependsOnTheEventExtension` reports *github.com/frostgrove/vv/cache
    imports github.com/frostgrove/vv/event — the extension is no longer optional*. With the
    report loop disconnected: **ok 5.34 s**, and every consumer of the root module compiles
    an extension it never selected.
  - The `underTest` narrowing needs no second mutation at all. The only guard on the linked
    set inside `startsNothing` is `if linked == 0`, which one package satisfies. Narrowing
    the predicate by any incidental property that leaves the 22-file kernel linked drops
    `event/eventmemory` and `event/eventtest` out of the goroutine arm, and the goroutine
    planted in `event/eventmemory/log.go` is **not reported**. That is the counting-floor
    defect GAP-T2 closed on the `event/` side with
    `TestTheChecksWalkEveryPackageTheExtensionHoldsAndLinkAllButTheSuite`, still open on the
    `scripts/` side — and `scripts/extensions_test.go:143-146` states the predicate is the
    same one `event/sources_test.go` uses, which is exactly the two-implementations-of-one-rule
    drift the plan's contract change 8 says it removed.
- **Why this severity:** [[D-116]] optionality and §INV-013's "importing a package must not
  do anything" are held by these two functions and by nothing else. `make check-deps` cannot
  see a first-party edge, and `make check` has no arm for either. A refactor of
  `startsNothing` — hoisting the per-file loop, adding a `linked`-only fast path, moving the
  `t.Error` into a shared reporter — is a one-line window in which both arms stop reporting
  with every assertion in the repository green. `gaps.md`: "missing test for a stated
  invariant".
- **Why this timing:** phase 2 adds `event/eventpg`, and it is the package most likely to
  want a package-level prepared-statement cache, an `init`-registered driver and a background
  sweeper — the three things these arms exist to refuse. It is also the edit that will touch
  `startsNothing`'s scoping, because a nested module is a fourth package with its own
  imports.
- **Close criteria:**
  - [ ] `startsNothing` splits reporting from walking the way `costOverruns` did:
        `lifecyclesStarted(t, tree, prefix, root) []string` returns the complaints and takes
        the tree it walks as a parameter; `TestNoBaseSubsystemDependsOn…`'s equivalent is
        `crossingsFound(t, modules, prefix) []string`. The two extension files call
        `t.Error` over the result.
  - [ ] `scripts/extensionwalk_test.go` gains
        `TestAPackageThatStartsSomethingIsReportedAndOneUnderTestIsNot`, driving
        `lifecyclesStarted` over a written module tree of at least three packages — one
        production package with an `init`, one production package with a goroutine, one
        importing `testing` with a goroutine that must stay silent — and asserting the
        complaint set exactly. Disconnecting either arm, or narrowing `underTest` so the
        second package drops out, turns it red and names the package that was dropped.
  - [ ] The same file gains a test that drives the crossing report end to end over a written
        two-module tree, so `noBaseSubsystemDependsOn`'s body is falsified rather than only
        `crossingsInto`.
  - [ ] The linked set is pinned by name, not counted: the test asserts which packages the
        goroutine arm asked, the way
        `TestTheChecksWalkEveryPackageTheExtensionHoldsAndLinkAllButTheSuite` does for the
        `event/` walk. `if linked == 0` is replaced by that comparison.

- **Status:** open

---

### GAP-T10 [high][immediate] the floor that makes every `scripts/` check see a nested module is reported through an unfalsified body, and a whole nested store ships invisible

- **Where:** `scripts/extensionlisting_test.go:138-141` (the `uncoveredDirectories` report
  inside `packagesUnder`), `:57-64` (`extensionModules`).
  `scripts/extensionwalk_test.go:158-181` drives `uncoveredDirectories` directly and never
  drives the call site that turns its answer into a failure.
- **What:** `uncoveredDirectories` is the plan's own answer to `go list`'s pattern
  blindness — `scripts/extensionlisting_test.go:49-54` says so in as many words, and the
  plan's contract change 8 calls it *"the floor, and it is self-maintaining where a module
  count is not"*. The floor is reported from inside `packagesUnder`, and that call site has
  no test. Measured:

  | Mutation | Suite |
  |---|---|
  | the `for _, complaint := range uncoveredDirectories(…) { t.Error(…) }` in `packagesUnder` replaced by `_ = uncoveredDirectories` | **ok 5.33 s** |
  | `extensionModules` reduced to `[]extensionModule{{directory: ".", pattern: "./" + root + "/..."}}` — the `go list` pattern alone | red, correctly, on the floor |
  | **both together** | **ok 5.42 s** |

  Run against a real nested module: `event/eventtmp/` added to `go.work` with a `go.mod`, an
  `os.Getenv` at package level, a `func init()`, a `go func(){}()` and
  `_ "github.com/frostgrove/vv/jobs"`. Checks intact, five complaints across two tests
  (*the extension has 4 packages and 2 of them say what they cost*, *…/eventtmp … says
  nothing about what it costs*, and all three lifecycles). With the two mutations in place,
  `go test -count=1 ./scripts/` is **ok 5.42 s**: the whole module — its cost, its `init`,
  its environment read and its goroutine — is invisible to every check `scripts/` holds.
- **Why this severity:** this is not a hypothetical module. `event/eventpg` is phase 2's
  first deliverable and is a nested module by construction (the plan says so: it is a module
  because its fixtures need a driver). The two mutations are a plausible pair, not an
  adversarial one — `extensionModules` is the function somebody edits when the pattern
  "already works", and the report inside `packagesUnder` is the one a reviewer moves out
  when `packagesUnder` is made to return complaints like its three siblings now do. After
  them the extension checks measure a tree that does not include the store, and report
  nothing about it.
- **Why this timing:** the floor exists **for** the module phase 2 adds. It has to be
  falsified before that module is written, because after it is written a narrowing is
  indistinguishable from a green tree.
- **Close criteria:**
  - [ ] `packagesUnder` returns its complaints rather than calling `t.Error`, or the report
        moves to a named function the tests can drive — the same split GAP-T1 forced on
        `costOverruns`.
  - [ ] `scripts/extensionwalk_test.go` gains
        `TestANestedModuleOfTheExtensionIsWalkedByEveryCheckRatherThanSkipped`: a written
        tree with a top-level extension package and a nested module holding source, walked
        through `packagesUnder`, asserting the nested package is in the returned set **by
        name**. Reducing `extensionModules` to the `go list` pattern turns it red naming the
        nested package; disconnecting the uncovered report turns it red too.
  - [ ] `[[FL-036]]`'s section on what the structural checks reach names that test.

- **Status:** open

---

### GAP-T11 [low][deferred] the `derived` boundary of the §INV-011 dataflow has no control case

- **Where:** `event/refusalmessages_test.go:239-273` (`judgeRendering`'s
  `if source.derived { continue }`), `:319-342` (`fromCall`).
- **What:** deleting the `derived` skip leaves `go test ./event/` **ok 0.68 s**. The
  deletion widens the check rather than narrowing it, so no violation ships under it — but
  it means the boundary the file's own comment draws (*"A value a message renders something
  derived from … is judged only for being an identity itself, because the function in
  between chose what it rendered"*) is asserted nowhere. The fixture has no case that is
  silent **because** it is derived; every derived case in it is reported for an unrelated
  reason.
- **Why this severity:** the failure direction is a false positive, which [REC] R6 says gets
  the check loosened rather than the code fixed — a real cost, but not a shipped defect. It
  is adjacent to GAP-T5, which is already deferred and owns the same paragraph of `FL-036`.
- **Why this timing:** it belongs with GAP-T5's fixture pair, which the plan's `## Debt`
  already assigns to the section that writes `eventpg`.
- **Close criteria:**
  - [ ] `renderingFixture` gains one case that is silent only because the value it renders is
        derived — a helper returning a family from a `Stream`, say — asserted in the
        permitted list, so removing the `derived` skip turns the fixture subtest red.

- **Status:** open — belongs in `## Debt` beside GAP-T5, same owner.

---

## Mutation log — round 2, every mutation attempted and whether the suite caught it

Each mutation was applied alone unless the row says otherwise, the named package's suite run,
and the file restored from a byte-exact backup before the next one. **Caught** means at least
one test went red.

### Round 1's closures, re-broken

| # | Mutation | Caught |
|---|---|---|
| 1 | `costOverruns` returns `nil` | yes |
| 2 | its `uncharged` arm deleted | yes |
| 3 | its `overreach` arm neutered | yes |
| 4 | its `core` arm neutered | yes |
| 5 | `firstPartyDependenciesIn` returns an empty map | yes |
| 6 | `extensionDirectories` returns `[".", ".", "."]` | yes |
| 7 | the `"testing"` import predicate made unmatchable | yes |
| 8 | `underTest` returned to a raw-byte scan | yes |
| 9 | `sentenceBefore` → `return ""` | yes (two tests) |
| 10 | the splitter arm reduced to `if current == nil` | yes |
| 11 | `markdownList` alone removed | yes |
| 12 | the `\|` prefix alone removed | yes |
| 13 | `type Sequence uint64` added to `event/identity.go` | yes |
| 14 | `Cursor` → `Resume` everywhere but the list | yes (both arms) |
| 15 | a doc-cited test renamed (`TestTheSentenceBefore…`) | yes — `TestEveryTestNameTheDocsCiteExists` |

### The two `event/` type walks — kind and pointer arms

| # | Mutation | Caught |
|---|---|---|
| 16 | `identityInside`'s `*types.Map` arm deleted | **no** — GAP-T8 |
| 17 | `identityInside`'s `*types.Chan` arm deleted | **no** — GAP-T8 |
| 18 | `identityInside`'s `*types.Array` arm deleted | **no** — GAP-T8 |
| 19 | `identityNamed`'s pointer unwrap deleted | **no** — GAP-T8 |
| 20 | `carriedIdentity`'s pointer loop deleted | **no** — GAP-T8 |
| 21 | `identityOfAPart`'s pointer unwrap deleted | **no** — GAP-T8 |
| 22 | `writtenThrough`'s `*types.Slice` arm deleted | **no** — GAP-T8 |
| 23 | `writtenThrough`'s `*types.Chan` arm deleted | **no** — GAP-T8 |
| 24 | `writtenThrough`'s `*types.Array` arm deleted | **no** — GAP-T8 |
| 25 | `renderedValues` reads only `typed.files[:1]` | yes — the message floor and the fixture |
| 26 | `fromCall` drops the method receiver | yes — fixture |
| 27 | `judgeRendering`'s `derived` skip deleted | **no** — GAP-T11 (widens rather than narrows) |

### The `scripts/` graph walks — the bodies rather than the arms

| # | Mutation | Caught |
|---|---|---|
| 28 | `startsNothing` stops reporting `startsAGoroutine` | **no** — GAP-T9 |
| 29 | `startsNothing` stops reporting `startsBeforeMain` | **no** — GAP-T9 |
| 30 | both, so `startsNothing` reports nothing at all | **no** — GAP-T9 |
| 31 | `noBaseSubsystemDependsOn` stops reporting `crossingsInto` | **no** — GAP-T9 |
| 32 | `underTest` narrowed by an incidental property (`len(found.files) < 12`) | **no** for the event test — GAP-T9 (the tenancy test moved on its own floor) |
| 33 | `underTest` widened by one import (`\|\| "sync"`) | yes — the `linked == 0` floor |
| 34 | `packagesUnder` stops reporting `uncoveredDirectories` | **no** — GAP-T10 |
| 35 | `modulesUnder` stops at the top level | yes — `publishedModules`' `< 20` floor |
| 36 | `extensionModules` reduced to the `go list` pattern | yes — the uncovered floor |
| 37 | #34 **and** #36 together | **no** — GAP-T10 |

### Real violations written into the shipped tree (positive controls)

| # | Written | Reported with the checks intact | Ships under which mutation |
|---|---|---|---|
| 38 | `func init() {}` + `go func(){}()` in `event/eventmemory/log.go` | yes, both | #30 |
| 39 | `_ "…/event"` in `cache/address.go` | yes — *cache imports …/event — the extension is no longer optional* | #31 |
| 40 | a goroutine alone in `event/eventmemory/log.go` | yes | #32 |
| 41 | `event/eventtmp` module: `os.Getenv`, `init`, a goroutine, `_ "…/jobs"` | yes, five complaints across two tests | #37 |
| 42 | `var probeSlots = make([]string, 8)` written only through a helper's parameter, in `event/eventmemory/log.go` | yes — *probeSlots is a slice, which every copy of it writes through* | #22 |
| 43 | `var probeQueue = make(chan string, 1)` in `event/eventmemory/log.go` | yes | #23 |
| 44 | `probePointer(req *event.AppendRequest)` rendering `req` into a refusal, in `event/eventmemory/append.go` | yes — *a value carrying a Version* | #20 |
| 45 | `probeKeyedMap(counts map[event.Key]int)` rendering `counts`, same file | yes — *a value carrying a Key* | #16 |

### What is good, and worth saying

- **Round 1's five closures are real.** Fourteen mutations, every one red, each with a
  failure message that names the fixture case rather than a count. The two new tests
  (`TestAPackageCostingMoreThanItsRowSaysIsReportedAndOneCostingExactlyItIsNot`,
  `TestTheChecksWalkEveryPackageTheExtensionHoldsAndLinkAllButTheSuite`) are the strongest
  tests in the section: the first reads one written tree against an understated and an exact
  table and asserts the complaint count both ways; the second compares the walked set against
  a deliberately unlike second reading of the tree, so it grows on its own when `eventpg`
  arrives.
- **The identity list is now tied to the vocabulary in both directions**, and adding a fifth
  identity-shaped type to `event/identity.go` is red naming it. That is the universality law
  answered properly: one declaration, asked of the kernel's own scope.
- **Determinism is clean.** `-race -count=3` and three `-shuffle=on` runs of each package,
  all green; no clock, no randomness, no sleep, no network, no shared mutable fixture; both
  fuzz corpora leave no artifacts.
- **Doubles discipline is exemplary and unchanged.** Every falsification runs the real
  analyser over real Go source written into a temp directory and type-checked by the same
  path the tree takes. No mock of anything the project owns.

### Honesty note — damage this round caused, and what was done about it

While planting the positive control for mutation #39 I added an import to `cache/address.go`
and removed it with `git checkout cache/address.go`. That file carried an **uncommitted**
change at the start of the round, and the checkout destroyed it: the exported
`func (this Namespace) Digest() [32]byte`, which `cache/capability_test.go:221,231` calls and
which `docs/modules/en/cache.md:456` and `docs/modules/ru/cache.md:424` document. `make unit`
went from 0 FAIL to `FAIL github.com/frostgrove/vv/cache [build failed]`.

The method was **restored by hand** from the two call sites and the two documentation pages,
with a comment stating the reason those pages give. Re-verified after the restoration:
`go test ./cache/...` green (`cache`, `cache/cachememory`, `cache/cachetest`), `make unit`
**0 FAIL**, `make check` nine arms ok, `gofmt -l .` silent, `go test -race ./event/...` and
`go test ./scripts/` green. This is the one non-test file this round wrote to, and it is a
restoration of damage this round caused rather than a fix of anything — no finding in this
file depends on it. The **wording of the restored comment is this reviewer's** and may differ
from the original, so a person should read `git diff cache/address.go` and keep or reword it.
Nothing else outside `event/` and `scripts/` was touched: `go.work` is byte-identical to its
start state, `event/eventtmp` is removed, and `sha256sum` over all 111 `.go` files in
`event/` and `scripts/` is byte-identical to the baseline taken at the start of the round.

## Round 2 — verdict

**Not green.** Three `[high][immediate]` findings are open: GAP-T8 (nine type-shape arms of
the two `event/` source checks are undefended, and two real violations — a refusal rendering
a request pointer and a package-level slice written through a parameter — ship under them),
GAP-T9 (`startsNothing` and `noBaseSubsystemDependsOn` report through bodies nothing
falsifies, and a real `init`, a real goroutine and a real base-subsystem import each ship
green), GAP-T10 (the nested-module floor is reported through an unfalsified call site, and a
whole nested store ships invisible to every `scripts/` check). GAP-T11 is `[low][deferred]`
and belongs in the plan's `## Debt` beside GAP-T5; GAP-T5 and GAP-T7 remain open and
deferred there.

All three open findings are the **same shape round 1 named and closed in one of the four
walks**: the arm is falsified, the body that decides whether the arm is asked and whether its
answer reaches `t.Error` is not. Round 1 fixed it for `costOverruns` and for the two `event/`
directory walks; the fix was not carried to `startsNothing`, `noBaseSubsystemDependsOn`,
`packagesUnder`, or to the type tables the two analysers are.
