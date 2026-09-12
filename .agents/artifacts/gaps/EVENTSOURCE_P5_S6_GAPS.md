# EVENTSOURCE P5 — S6 (the decisions, the docs, the examples and the roadmap close-out) — GAPS

## Round 1 — econv-code-reviewer — 2026-09-12

**Verdict: RED. One `[high][immediate]` — GAP-1, the new usage guide's adoption path does not
compile.** *(GAP-1 closed 2026-09-12; see its Status line.)* Four further findings (`[medium]` ×3, `[low]` ×1) go to
[`EVENTSOURCE_BACKLOG.md`](EVENTSOURCE_BACKLOG.md) `## P5` as items **59–62** and are NOT fixed,
per the 2026-09-08 delivery policy.

Everything below was **driven**, not read. The section's deliverable is four decision records, nine
documentation files, two runnable examples and four doc checks, so this review spent its budget on
(a) re-executing every arm of the pasted checkpoint, (b) compiling and running the documents'
own code, (c) reproducing the measurement D-145 rests its gate arithmetic on, (d) attacking the
section's own checks, and (e) constructing five states nothing in the tree drives.

**The pasted checkpoint output is real.** Every arm re-executed at HEAD; numbers in §1. `make api`
regenerates byte-identical, `./scripts/checks.sh event-kernel` is `ok`, `make check` is green on
all **eleven** arms, and `go test -race -count=1 ./scripts/` reports **exactly one** `--- FAIL:`
line, the foreign i18n one, in 136.7 s against the record's 136.199 s.

**`make unit` is NOT green and this report does not claim it is.** One `--- FAIL:` line across 62
`ok` packages: `TestNoI18nPackageCostsMoreThanItsErrorSeam` in `./scripts`, which arrived with the
owner's i18n work, is not this phase's, and was neither repaired nor allowlisted here.

**The kernel boundary is clean.** S6 moved nothing under `event/`: no file under `event/` has an
mtime after the S5 review closed except `event/eventpg/go.{mod,sum}`, which `make tidy` rewrote
identically and which `git status --porcelain` does not list — §5.

**D-145's measurement is reproducible.** `BenchmarkStreamReplay` re-run on the same host gives
**11.48 – 11.62 ms** at 10 000 events and **110.25 ms** at 100 000 against the record's
12.91 – 14.88 and 108.4 – 111.3. The 100 000-event figure — the one the ~45 000-event crossing is
computed from — lands inside the recorded band. The gate arithmetic in a binding decision is a
measurement somebody can repeat, and it repeated — §2.

**Every ES-09 nuance the study records is in D-145. Twelve for twelve**, plus the four the study
says no source supplies and the phase had to invent. ES-05's seven, ES-07's eight and ES-08's six
doc-half nuances are placed too — §3.

**Three mutations, three catches.** The section's two load-bearing checks and the widened
reverse-index walk were each broken independently of the implementer's own five and each reported —
§4.

**Five hard states driven** — a wait across a cutover, a wait on a parked sequence, `Expired`
versus `Unresolved` at the exact horizon boundary, a `Resolve` inside the writing transaction, and
a bounded read whose prefix holds an unreadable event — §6. The sixth the brief asks for, a
snapshot whose fold changed under it, is **unconstructible by design**: D-145 ships no code and
`TestNoSnapshotAuthorityIsDeclaredOrPromised` is what keeps it so; that is asserted by mutation
rather than by reading — §4, mutation 2.

---

## 1. The checkpoint, re-executed

| Arm | Claimed in [PLAN] § *S6 — executed* | Measured now |
|---|---|---|
| `gofmt -l . \| wc -l` | silent | `0` |
| `make api` | "the diff is empty" | regenerated; `git diff --numstat docs/api/surface.md` → `116 0`, unchanged by the run |
| `receipt` row, `docs/modules/en/Index.md` | 1 | `1` |
| `receipt` row, `docs/modules/ru/Index.md` | 1 | `1` |
| six `en` event pages | 6 | `6` |
| six `ru` event pages | 6 | `6` |
| `FL-043` file | present | present (238 lines) |
| `docs/usage-guides/event-sourcing.md` | present | present (410 lines) |
| the two-armed flow loop over every non-test file under `event/` | both arms clean | clean; `event/eventtest/sections_topology.go` (red at HEAD per the section's own note) now named by `FL-036:353` |
| `-list` count of the eight doc checks | `8` | `8` |
| the eight, `-race` | `ok … 15.542s` | `ok` (run inside the whole-package run below) |
| `go test -race -count=1 ./scripts/` | one `--- FAIL:`, the i18n one, 136.199s | **one** `--- FAIL:`, `TestNoI18nPackageCostsMoreThanItsErrorSeam`, **136.740s** |
| `D-145` in `decisions/Index.md` · in `D-132` · `superseded` count | present · present · 0 | `4` · `3` · **`0`** |
| `### Приложение ES-0(5\|7\|8)` headings | 0 | `0` |
| `### Приложение ES-09` heading | 1 | `1` |
| `make examples` | `EXAMPLES_EXIT=0` | exit `0`; both new packages built |
| `checks.sh tidy` · `replaces` · `event-kernel` | ok · ok · ok | `check-tidy: ok` · `check-replaces: ok` · `check-event-kernel: ok` |
| `make check` | every arm, exit 0 | eleven arms, exit `0` |
| `make unit` | one foreign `--- FAIL:` | one `--- FAIL:` line, 62 `ok` packages |
| live `./event/eventpg` `-tags=integration -race` | `ok … 122.750s` | `ok … 126.309s` |
| `GOWORK=off go run ./event-wait` | three lines | three lines, same shape (numbers differ — GAP-6) |
| `GOWORK=off go run ./event-receipts` | eight lines | eight lines, same shape (`range=2..2` where the record has `1..1` — GAP-6) |

`docs/api/surface.md`'s `event` section carries exactly the three new lines the deliverable
checklist names — `var ErrVersion error`, `func (*Repo[S, ID]) Digest(...)`,
`func (*Repo[S, ID]) StateAt(...)` — with nothing removed or changed.

## 2. D-145's measurement, reproduced

```
$ FROSTGROVE_EVENTPG_TEST_DSN=… go test -tags=integration -run '^$' \
    -bench '^BenchmarkStreamReplay$' -benchtime 20x -count 3 ./event/eventpg/
cpu: Intel(R) Core(TM) i9-10900K CPU @ 3.70GHz
BenchmarkStreamReplay-20   20   11624522 ns/op   1162 ns/event
BenchmarkStreamReplay-20   20   11527399 ns/op   1153 ns/event
BenchmarkStreamReplay-20   20   11483497 ns/op   1148 ns/event

$ … -benchtime 10x -count 2 ./event/eventpg/ -args -eventpg.replay.events=100000
BenchmarkStreamReplay-20   10  110255669 ns/op   1103 ns/event
BenchmarkStreamReplay-20   10  110253068 ns/op   1103 ns/event
```

D-145 records 12.91 – 14.88 ms at 10 000 and 108.4 – 111.3 ms at 100 000, and 1 084 – 1 113
ns/event at length. Measured now: 11.48 – 11.62 ms, 110.25 ms, 1 103 ns/event. **The number the
crossing is computed from reproduces to three digits**, so the ~45 000-event threshold D-145
publishes and the roadmap repeats is arithmetic anyone can redo. The benchmark's stand is the one
D-145 describes: a 120-byte payload (`event/eventpg/replay_integration_test.go:58`), one record
type at revision 1, written in batches.

Gate 1 is correctly reported **unmet** — this is the repository's own stand over a loopback socket,
which is what D-145 says in its third reading and what the roadmap's `Статус` line repeats.

## 3. Nuance conformance

**ES-09 — twelve of twelve, in [[D-145]].**

| # | Nuance | Where |
|---|---|---|
| 1 | the check runs on stored metadata **before** deserialisation, and the ordering is the mechanism | § *What a snapshot is bound to*, with Axon's `.filter` before `.map` quoted |
| 2 | `@Revision` is a serialised-form version, not a state-computation one, and no source closes the gap | § *The state-computation version*, verbatim as the requirement the source cannot supply |
| 3 | javadoc rule 3 is a composition safety rule and inverting it disables every aggregate | § *Open questions*, first bullet, with `combine`'s AND named |
| 4 | ignore vs delete on drift — two shipped implementations disagree | § *History is never deleted; a snapshot is not history*, taking the port's side with `DO UPDATE` and the reason |
| 5 | `Exception \| LinkageError` — the ways a stored snapshot can fail exceed what the writer expects | § *The fallback is observable*, final paragraph |
| 6 | the fallback is silent, and vv cannot even log | § *The fallback is observable*, which is the clause that makes provenance a return value |
| 7 | the tail is half-open, and `<= :version` is the clause | inherited constraint 3, with the version-12-of-a-30-snapshot failure |
| 8 | the snapshot is written INSIDE the append transaction | inherited constraint 1, tied to [[D-118]] and [[D-126]] |
| 9 | cadence `% N == 0`, `N >= 2`, checked twice; a multi-event command jumps the boundary | inherited constraint 2 |
| 10 | do not read the reference's snapshot code for the shape | inherited constraint 4 |
| 11 | neither implementation prunes | inherited constraint 5 |
| 12 | Axon's storage warning is what ES-09 must refuse outright | § *History is never deleted*, quoted and refused |

The four the study says **no source supplies**: the state-computation version (two components, with
the three refused derivations and the honest *"nothing detects a forgotten bump"*), the observable
fallback, the equivalence proof (Gate 2, with the falsification-by-one), and the measured benefit
(Gate 1, asked before the file was written). All four present.

ES-09's appendix part 3 walked sentence by sentence: five bindings ✓, only a confirmed version
loads and the whole tail replays ✓, incompatibility → full replay and an unreadable **event** not
hidden ✓, measured benefit + equivalence ✓, history never deleted ✓. Part 5's *«оператор может
отбросить snapshot»* is named as deliberately open and is backlog item 7.

**ES-05, doc half — seven of seven.** The completeness-watermark clause is not merely present, it
is **enforced**: `highestStatedAsAWatermark` (`scripts/docs_test.go:1568`) requires the sentence on
both `projection.md` pages and `highestRestatedAsAPage` reports the [[D-128]] restatement, with a
two-language fixture control. Nuance 2 (a burnt gap) and 4 (the progress substrate surfacing only
as a timeout) land on `docs/modules/{en,ru}/eventpg.md`'s new paragraph, which says the stall is
*one* idle session behind two symptoms. Nuance 6 (`Commit` carries no position; one extra
`ReadStream`) is on `projection.md`. Nuance 5 (`FetchLatest`) and 7 (the scope sentence) are
`UC-036`'s `Out of scope`, which carries both *global linearizability* and *another replica's
freshness* as separate bullets.

**ES-07, doc half — eight of eight**, across [[D-142]], [[D-143]], `receipt.md` and `UC-037`.
Nuance 8 (the port's cross-aggregate refusal) is closed by construction rather than by a column
comparison: the fingerprint covers the **composed stream**, so a key presented against another
aggregate answers `Collided`. The residual — that the row's own `Stream` is never compared — is
already backlog items 34 and 39.

**ES-08, doc half — six of six** in [[D-144]] and `UC-038`. Nuance 5 — that an at-version read is
correctness and not convenience — is `UC-038`'s scenario paragraph, in the integration-event form
the study records (*"a replayed stream indistinguishable from N copies of the current state"*).

**No nuance is dropped.** GAP-1 is not a dropped nuance; it is wrong code in a document.

## 4. The checks, attacked

Three mutations, each distinct from the five the section's own record lists, each restored and each
verified restored with `git diff`/`git status`.

| Mutation | Reported? | What it said |
|---|---|---|
| `_examples/event-receipts/main.go`: `statement_timestamp()` → `now()` inside `claimInsertStatement` | **yes** | `TestTheExampleLedgerIsTheOneTheLiveSuiteProved` — *"claimInsertStatement differs between the live suite and \_examples/event-receipts"*, printing both strings |
| `event/receipt/receipt.go`: `type Snapshot struct{ At event.Version }` appended | **yes** | `TestNoSnapshotAuthorityIsDeclaredOrPromised` — *"../event/receipt/receipt.go:77:6 declares Snapshot, and full replay is the only authority a folded state has here"*. This is the arm that proves P-3's sixth package really is inside the walk, and it is the only executable guard [[D-145]] has |
| `FL-043`'s body: `event/receipt/resolve.go` renamed in the file table only | **yes** | `TestEveryProjectionSourceFileIsNamedByTheFlowReverseIndex` — *"event/receipt/resolve.go's row sends a reader to FL-043 and that flow never names the file"*. The **second** arm, which is the one the roadmap obligation turns on |

All three restored; `check-event-kernel: ok` afterwards and `git status --porcelain` unchanged.

## 5. Kernel boundary

```
$ git status --porcelain event/          # thirteen modified, nine new — all S1–S5's
$ find event -newermt "2026-09-12 17:02" -type f
event/eventpg/go.mod
event/eventpg/go.sum                     # rewritten identically by make tidy; not in git status
$ ./scripts/checks.sh event-kernel  →  check-event-kernel: ok
```

S6's own writes are confined to `docs/`, `_examples/` and `scripts/docs_test.go`. The claim *"moves
nothing under `event/`"* holds.

## 6. The hard states, driven

Driven from a scratch module outside the repository (`/tmp/s6drive`, `replace` onto this tree), so
nothing here rests on a test the section wrote.

| State | What happened |
|---|---|
| **a wait across a cutover** | `Generations.Active` answering generation 2 to a wait naming 1 → `ErrGeneration`, **terminal on poll 1**, one `Active` call, 0 ms. The census was never reached, which is the documented order |
| **a wait on a parked sequence** | census past the mark, `Park.Sequences` → 1, `Holds("driveorder/parked-1")` → true → `ErrParked` on poll 1, `Visibility.Parked = true`. `Holes` was a `panic("a wait must never ask Holes")` and never fired |
| **an expired receipt versus an absent one** | horizon `now()-24h`: zero `Issued` → `Unresolved`; `Issued` one minute ago → `Unresolved`; `Issued` 48 h ago → **`Expired`**; `Issued` **exactly at** the horizon → `Unresolved`, which is `!issued.Before(horizon)` and matches D-143 word for word |
| **a ledger that publishes a horizon after a row it answers for** | `ErrLedger`, *"it published a horizon later than a row it still answers for"* — the `MAX(recorded_at)` defect refused at the door |
| **a `Resolve` inside the writing transaction** | `ErrSpec`, naming the store's bound transaction. The out-of-transaction, still-open case was driven **live** by `_examples/event-receipts`, which prints `[standing unresolved]` and then `[standing found]` after the commit |
| **a historical read with an unreadable prefix** | `StateAt(v2)` through a declaration that cannot read the recorded type → `ErrUnknownType` **and the zero state**, byte-identical to `Load`'s refusal on the same stream. `StateAt(v4)` past the end and `StateAt(v0)` → `ErrVersion`, and **neither message names a version, a key or a head** |
| **a snapshot whose fold changed under it** | unconstructible: no snapshot type, field or function exists under `event/`, and §4's mutation 2 shows the guard that keeps it so is live over the new package |

---

### GAP-1 `[high][immediate]` — the new usage guide's adoption path does not compile, and two of its four errors are in the six lines that bind a store

- **Where** `docs/usage-guides/event-sourcing.md:140`, `:144`, `:237`, and `:39` / `:197`.
- **What** The one document this section shipped for a consumer to follow contains four API errors,
  three of them novel to this section:

  1. **`:144` — `orders, err := event.Bind(Orders, store)`.** The signature is
     `func Bind[S, ID any](b *Binding, a *Aggregate[S, ID]) (*Repo[S, ID], error)`
     (`event/binding.go:30`). The arguments are in the wrong order **and** a `*eventpg.Store` is
     passed where a `*event.Binding` is required. Every other place in the repository writes
     `event.Bind(event.Open(store), Orders)` — all five examples and both `eventpg.md` /
     `eventmemory.md` pages, in both languages. Compiled:

     ```
     probe.go:28:36: in call to event.Bind, type *eventpg.Store of store does not match
                     *event.Aggregate[S, ID] (cannot infer S and ID)
     ```

  2. **`:140` — `eventpg.New(eventpg.Spec{Source: source, Schema: eventpg.Schema{Name: "events"}})`.**
     `Spec.DB` is omitted and is the **first** thing `New` refuses
     (`event/eventpg/config.go:89-92`). This one compiles and fails at run time, which is worse
     than the compile error above because a reader can conclude the `Source` is the handle. Run:

     ```
     eventpg.New(Spec{Source, Schema}) -> eventpg: this store cannot be assembled from this spec:
       Spec.DB is nil, and a store over no database is a store that refuses everything at its first call
     ```

  3. **`:237` — `projection.WaitOf(spec, cover)` with the parenthetical *"cover is
     `projection.Whole()` unless partitioned"*.** `Whole()` answers a `Partition`
     (`event/projection/partition.go:33`); `WaitOf` takes a `Cover`
     (`event/projection/wait.go:68`). The step between them is `NewCover`, which the example gets
     right (`_examples/event-wait/main.go:140`). Compiled:

     ```
     probe.go:34:33: cannot use projection.Whole() (value of struct type projection.Partition)
                     as projection.Cover value in argument to projection.WaitOf
     ```

  4. **`:39` and `:197` — `supervisor.Add(following)`.** `runtime.Supervisor` has no `Add`:
     runners are supplied through `Spec.Runners` at construction, via `NewSupervisor(Spec{…})` or
     `Auto(runners…)` (`runtime/supervisor.go:27-43`, `docs/modules/en/runtime.md:41-42`). This
     one is **inherited** — `docs/modules/en/projection.md:62` has said it since before this phase
     — so it is named here as the third place it now appears rather than as S6's invention.

  The section's own `Held by` column names `TestEveryTestNameTheDocsCiteExists` and
  `TestEverySymbolTheDocsCiteIsDeclaredWhereTheDocSaysItIs` for this deliverable. Both **do** read
  `../docs` and both were green: the first matches backticked `Test…` names, the second matches
  `file.go:Symbol` citations. **Neither can see a call, an argument order or a struct literal**, so
  the gate named for this file is structurally unable to fail on any of the four.
- **Why this severity** `[high]`: this is a consumer's first contact with the subsystem, and Part III
  is the step without which nothing else in the guide can run. A reader following it writes two
  lines, gets `cannot infer S and ID` from a generic call whose correct spelling is not guessable
  from the error, fixes it against a module page, then gets `Spec.DB is nil` at run time from a
  constructor whose `Spec` literal looked complete, then reaches Part V and gets a third type
  error. The repository prices this itself — *"it is a consumer's reference and a wrong option name
  there is a compile error they hit and you did not"* — and a wrong **call signature** is strictly
  worse than a wrong option name. It is not `[critical]`: nothing in the library is wrong, no
  shipped behaviour changes, and every error is loud at compile or construction time rather than
  silent at run time.
- **Why this timing** `[immediate]`. `docs/roadmaps/Roadmap.md:443` now advertises this file as
  *"the adoption route"* and the roadmap's ES-05/07/08 close-out points at the same tree, so the
  phase's own close-out asserts a document that does not work. It is also the cheapest possible
  moment: four lines, no behaviour, and the two examples beside it already contain the correct
  spelling of every one of them.
- **Close criteria**
  - [x] `:144` reads `event.Bind(event.Open(store), Orders)`.
  - [x] `:140`'s `eventpg.Spec` literal carries `DB`, and the guide's snippet constructs and names
        the `*sql.DB` it and the `crud.Source` share — `sql.Open("pgx", dsn)` then
        `crudsql.Postgres(db)` over that same handle, because `New`'s third refusal is a `Source`
        and a `DB` that name two databases and a snippet that hides the handle cannot show it.
  - [x] `:237` shows the `NewCover` call — the stronger of the two the criterion offered — and the
        paragraph below it says a `Cover` is not a `Partition` and why a wait takes the second.
  - [x] `supervisor.Add(following)` is gone from all **three** places: the guide's `:39` and
        `:197` now read `runtime.Auto(following)`, and `docs/modules/en/projection.md` and
        `docs/modules/ru/projection.md` are corrected in the same pass, beside a sentence saying a
        supervisor takes its runners at construction.
  - [x] Every Go fence in the guide is compiled, by the third route the criterion offered.
        `_examples/event-guide` is the page's Go and nothing else: `make examples` builds, vets and
        tests it, and `TestEveryGoFenceInTheEventSourcingGuideIsCompiled` compares the page with
        the package line for line and in order, whitespace collapsed so gofmt owns the shape.
        Reverting the page reports drift; reverting the package fails to build. Two `...` elisions
        in Part III and Part VI went with the fix — neither block would have compiled — and the
        comparison supports **no** skip marker, so a fence that needs one is a fence to split.
- **How it was closed** Each of the four was reproduced with the compiler before it was touched, in
  a throwaway package under `_examples/` carrying the page's own lines, one fixed at a time so the
  next surfaced: `in call to event.Bind, type *eventpg.Store of store does not match
  *event.Aggregate[S, ID] (cannot infer S and ID)`; `cannot use projection.Whole() (value of struct
  type projection.Partition) as projection.Cover value`; `supervisor.Add undefined (type
  *runtime.Supervisor has no field or method Add)`; and, at run time, `eventpg: this store cannot
  be assembled from this spec: Spec.DB is nil, …`. Then four mutations, each reverting one shipped
  fix, each reported — the three novel ones by the fence comparison (including the `Spec.DB` one,
  which no compiler catches and a text comparison does), the inherited one by both that check and
  `TestNoDocCallsASupervisorMethodTheTypeDoesNotHave`, which reads `runtime.Supervisor`'s methods
  out of the source and finds `Add` is not among `Ready · Start · States · Stop`. All three
  controls of the first check refused to pass on the mutated page, so none of them can outlive the
  line it mutates.
- **Status: closed** — 2026-09-12. The section's checkpoint was widened from eight doc checks to
  ten in the same pass and re-run; [PLAN] § *S6 — [GAPS] GAP-1 closed* carries the output.

---

### GAP-1 — what this section's `Held by` column now says

The plan's **usage guide** and **Examples** rows name the fence comparison, and § *Doc checks
written or widened here* carries both new checks with their controls. The two name-matching checks
stay in the column: they are not wrong, they are blind to calls, and saying so beside them is the
point.

---

### GAP-2 `[medium][backlog]` — both `receipt.md` pages say the shipped example sweeps with a `jobs` periodic, and it does not

- **Where** `docs/modules/en/receipt.md:299`; `docs/modules/ru/receipt.md:306`.
- **What** *"`_examples/event-receipts` runs one as a `jobs` periodic"* / *"`_examples/event-receipts`
  запускает его периодической задачей `jobs`"*. The example ships
  `runtime.Every("receipt-sweep", time.Hour, held.sweep)` (`_examples/event-receipts/main.go:361`)
  and imports no `jobs` package at all; `_examples/go.mod` requires none. The section **knew** —
  its own deviation 2 and backlog item 58 record the change — and corrected the plan, the roadmap
  close-out table and the `_examples/README.md` row, but not the two pages that describe it to a
  consumer. Item 58's `Owed` line asks for an *added* sentence about `jobs.NewScheduler`; it does
  not record that the existing sentence is false.
- **Why this severity** `[medium]`, not `[high]`: nothing misleads a consumer about the framework's
  own behaviour — the sweep is the application's either way and the horizon contract is untouched.
  What it costs is a reader who opens the example expecting a scheduled durable job and finds a
  periodic runner, and a repository whose module page and whose backlog disagree about the same
  file in the same change.
- **Why this timing** Backlog. It is one clause in two files and it changes no contract.
- **Close criteria**
  - [ ] Both pages name the shape the example actually uses.
  - [ ] If `jobs.NewScheduler` is mentioned, it is mentioned as the heavier alternative rather than
        as what ships (item 58's `Owed`).
- **Status: open**

---

### GAP-3 `[medium][backlog]` — [[D-142]] records that its reciprocal sentence lives on a page that does not exist

- **Where** `docs/ai/decisions/D-142-an-operation-receipt-is-the-applications-table-beside-the-append.md:250-252`.
- **What** *"That sentence is on `docs/modules/{en,ru}/receipt.md` and on the `jobs` page, so the
  repository does not hold two answers and no document saying which."* There is no `jobs` page:
  `docs/modules/{en,ru}/` holds fifty-odd pages and none is `jobs`. The section's own backlog item
  57 says so in those words, and the plan carries the correction — *"corrected in S6: there is no
  `jobs` module page, so the reciprocal direction points at [[FL-035]]"* — which both `receipt.md`
  pages implement (`en:332`, `ru:339`). Only the decision was left saying the opposite, and a
  decision record is the binding layer.
- **Why this severity** `[medium]`: the decision's *argument* is sound and its by-symbol
  adjudication of `jobs` is accurate (all fifteen symbols it names were checked and all fifteen
  exist, including `jobs/placement.go:18`'s `PlacementOnce`). What is false is one claim about
  where a sentence lives, in the file a later agent reads to find out whether the obligation was
  met. It is the class this repository prices as a defect — *"a doc that names a symbol which no
  longer exists has failed at the one job it has"* — applied to a page that never existed.
- **Why this timing** Backlog. One clause; the obligation itself is met and recorded.
- **Close criteria**
  - [ ] D-142's sentence names `receipt.md` and [[FL-035]], or says the `jobs` page is owed and
        links item 57.
- **Status: open**

---

### GAP-4 `[medium][backlog]` — the new usage guide is in no index a reader reaches it from

- **Where** `docs/Index.md:60-75`; `docs/modules/en/Index.md:239-241`; `docs/modules/ru/Index.md`.
- **What** `docs/usage-guides/event-sourcing.md` has no row in `docs/Index.md`'s **Usage guides**
  list (which names ent, gorm, migrations, model-generation and tenancy), none in either
  `docs/modules/*/Index.md` guide list, and no link from any module page. The only document in the
  tree that points at it is `docs/roadmaps/Roadmap.md:443` — the file whose own rule is that it
  *"holds only what is not built"*, so the single pointer is on the page the close-out will delete
  it from. `CLAUDE.md` is explicit and unqualified: *"when you add a doc, add its row to the
  directory's `Index.md` in the same change. An index that does not list a file is worse than a
  missing file — an agent trusts the index and stops looking."*
- **Why this severity** `[medium]`: the guide exists and its content (GAP-1 aside) is sound, but a
  consumer following `docs/Index.md` — the entry point that file exists to be — never learns it is
  there. Mitigating: that list is already incomplete at HEAD (`usage-guides/repository.md` is
  missing too), so this is one more absence in a list nothing enforces rather than the first.
- **Why this timing** Backlog. One row, two or three files.
- **Close criteria**
  - [ ] `docs/Index.md`'s Usage guides list names `event-sourcing.md`.
  - [ ] The `event`, `projection` and `receipt` module pages link it from `See also`, or the
        modules index lists it beside ent/gorm/tenancy.
- **Status: open**

---

### GAP-5 `[medium][backlog]` — the doc check that was to hold "UC-032 is not silently widened" did not ship, and the omission is not among the recorded deviations

- **Where** [PLAN] § *The roadmap close-out*, the **Use cases** row's `Held by` column;
  `scripts/docs_test.go`.
- **What** The plan names, as what holds that obligation, *"a doc check asserting UC-032's two
  sections are byte-identical to their predecessor"*. No such check exists: `grep -rn 'UC-032'
  scripts/*.go` finds nothing, the four checks S6 added are
  `TestNoProjectionGuideRestatesHighestAsThePagesLastPosition`,
  `TestNoEventGuideOffersATimestampBoundary`,
  `TestTheThreeObligationsAWaitCannotCheckAreStatedTogether` and
  `TestTheExampleLedgerIsTheOneTheLiveSuiteProved`, and the checkpoint's own counted `-run` arm
  lists eight names none of which is it. The section records **three** deviations from its own text
  and this is not one of them.

  The property itself **holds today** and was verified directly: `git diff` on
  `UC-032-record-what-happened-and-rebuild-state-from-it.md` is `+20 −0`, all twenty lines a new
  `## See also` section appended after `Out of scope`; no line of `What must hold` changed and
  `Out of scope` gained nothing.
- **Why this severity** `[medium]`: nothing is wrong now, and the falsifier's absence costs only
  the next change. But *"UC-032 is not silently widened"* is the one constraint this phase's own
  framing states twice, and it is now held by nobody — a future section that adds one clause to
  `What must hold` gets no report from any arm of `make unit`.
- **Why this timing** Backlog. A byte comparison against a recorded copy of two sections is a
  small test, and the risk it covers is a future edit rather than a present defect.
- **Close criteria**
  - [ ] A check in `./scripts` asserts UC-032's `What must hold` and `Out of scope` against a
        recorded copy, with a control fixture that changes one clause and is reported.
  - [ ] Or the plan's `Held by` cell is corrected to name what actually holds it.
- **Status: open**

---

### GAP-6 `[low][backlog]` — both new examples accumulate state across runs, so their pasted output is not what a second run prints

- **Where** `_examples/event-wait/main.go:420-447` (`bootstrap`, `CREATE TABLE IF NOT EXISTS`);
  `_examples/event-receipts/main.go:598-610`.
- **What** Both examples create their tables `IF NOT EXISTS` and delete nothing, and the wait
  example's parked letters and the receipts example's streams survive the process. Re-run at HEAD
  against the same database:

  ```
  record:   parked after 2 poll(s): quarantined=0, 1 letter(s) held, sequences [waitorder/parked-…]
  now:      parked after 2 poll(s): quarantined=1, 2 letter(s) held, sequences [… …]

  record:   Once: verdict=[verdict recorded] range=1..1 complete=true
  now:      Once: verdict=[verdict recorded] range=2..2 complete=true
  ```

  Every line is the same shape and every assertion inside both programs still holds — the examples
  are correct, and `quarantined` and the version range are simply functions of how many times they
  have been run.
- **Why this severity** `[low]`: nothing is wrong, and the shipped `_examples/README.md` rows do
  not promise the numbers. What it costs is a reviewer: the plan pastes this output **as evidence**,
  and a reader re-running it to check the evidence cannot tell accumulation from drift without
  reading the schema.
- **Why this timing** Backlog. A fresh stream id per run and a per-run park identity would do it,
  and neither changes what the example demonstrates.
- **Close criteria**
  - [ ] Either the examples scope their rows per run (a fresh projection name or a `DELETE` at
        start), or any pasted output records that the numbers grow with the run count.
- **Status: open**

---

## 7. Commands run

```sh
gofmt -l . | wc -l                                             # 0
make api && git diff --numstat docs/api/surface.md             # 116 0, unchanged by the run
go test -list '^(…the eight…)$' ./scripts/ | grep -c '^Test'   # 8
go test -race -count=1 ./scripts/                              # 1 --- FAIL:, the i18n one, 136.740s
make unit                                                      # 1 --- FAIL: across 62 ok packages
make check                                                     # eleven arms, exit 0
make vet ; gofmt -l .                                          # clean ; silent
make examples                                                  # exit 0
./scripts/checks.sh tidy ; … replaces ; … event-kernel         # ok ; ok ; ok
FROSTGROVE_EVENTPG_TEST_DSN=… go test -race -count=1 -tags=integration ./event/eventpg/   # ok 126.309s
FROSTGROVE_EVENTPG_TEST_DSN=… go test -tags=integration -run '^$' -bench '^BenchmarkStreamReplay$' \
   -benchtime 20x -count 3 ./event/eventpg/                    # 11.48 – 11.62 ms @ 10k
   … -benchtime 10x -count 2 … -args -eventpg.replay.events=100000   # 110.25 ms @ 100k
cd _examples && GOWORK=off go run ./event-wait                 # three lines
cd _examples && GOWORK=off go run ./event-receipts             # eight lines
# the two-armed flow loop, the six index/heading greps, the D-132/D-145 arms  — all clean
# three mutations (example statement, a Snapshot type in event/receipt, FL-043's file table)
# five hard states, driven from /tmp/s6drive with a replace onto this tree
# the guide's own snippets compiled in /tmp/s6drive/guideprobe and run in guideprobe2
```
