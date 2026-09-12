# EVENTSOURCE PHASE 5 — GATE VERIFICATION

**Run 2026-09-12 against the working tree at `f161aee` + the uncommitted phase-5 change (41 modified
files, 9 new artifacts). PostgreSQL 17.9 at `localhost:55432`.**

**Verdict: GREEN.** Every `[critical]` and `[high]` finding in
`EVENTSOURCE_P5_PLAN_GAPS.md`, `EVENTSOURCE_P5_USECASES_GAPS.md` and `EVENTSOURCE_P5_S{1,2,4,6}_GAPS.md`
is closed **by code**, not by a note. Twenty-four blocking findings re-checked; seven fixes reverted
by mutation and every one of them was caught by the test its closure names; fifty-five hard states
driven from a program **outside this repository**; all four appendices walked sentence by sentence,
with one `[medium]` partial named.

Three red arms exist in this tree and **all three are foreign, all three reproduce at `HEAD`**, and
one of them is not the one the gate brief names. §4 has the transcript.

---

## 1. The blocking findings, re-checked

Each row was re-checked **against the code**, not against the closure paragraph. "Driven" means a
program outside this repository constructed the input the finding named and ran it (§2). "Mutation"
means the fix was reverted, the named test was run, it went red, and the file was restored
byte-identically (sha256 compared).

### `EVENTSOURCE_P5_PLAN_GAPS.md` — one `[critical]`, five `[high]`

| # | Finding | Closed by code? | Evidence |
|---|---|---|---|
| GAP-1 `[critical]` | the published one-statement CTE claim returns `(0 rows)` to the loser at READ COMMITTED and aborts it at the other two | **YES** | All four criteria land. (1) `_examples/event-receipts/main.go:73-78` and `event/eventpg/receipt_integration_test.go:39,54` both carry `INSERT … ON CONFLICT (key) DO NOTHING` **then** a separate `SELECT`; the CTE is gone. (2) `TestTwoCallersRaceOneKey` (`receipt_integration_test.go:657`) runs the three levels with `{"read committed", …, true}, {"repeatable read", …, false}, {"serializable", …, false}` and asserts the loser's `40001` concluded no verdict and that **the retry answers `Repeated`** (`:693`, `:698`). (3) `D-142` §*The serialisation point is the primary-key index **plus the level's conflict behaviour*** carries the measured three-by-three table and *"«Holds at every isolation level» is false"*. (4) the defect row is now the **`SELECT`-first ordering** — `receipt_integration_test.go:1808`, *"a claim whose select runs before its insert breaks TestTwoCallersRaceOneKey"*, asserting both callers append. **Mutation M6.** |
| GAP-2 `[high]` | the plan pre-authorised reporting a green `check-tidy` as foreign | **YES** | `PLAN:38` now reads *"`check-tidy` is **not** a second foreign red, and an earlier draft of this plan said it was"*. Re-measured today: `check-tidy: ok`, `check-replaces: ok`, inside a green `make check`. |
| GAP-3 `[high]` | `[SPEC]` §6 item 14 had no tagged test inside a claim of "items 11–24" | **YES** | `TestTheCollisionTableLiveBesideTheVersionVariant` exists and is tagged — `event/eventpg/receipt_integration_test.go:1716` — and `PLAN:819` § *The §6 item → test map* maps all twenty-four, routing item 23 into `event` untagged and item 24 to `BenchmarkStateAt` (`event/eventpg/stateat_integration_test.go:356`), both named rather than counted past. |
| GAP-4 `[high]` | a `Committed`-minted `Mark` recorded nothing about the spec that minted it | **YES** | `event/projection/wait.go:296` refuses `spec.Until.projection != spec.Of.Projection()` with `ErrSpec`. **Driven** — one projection's mark waited on another's spec is refused with *"Until was minted from another projection … the park would be asked under a key no letter of this projection was ever written under"*. |
| GAP-5 `[high]` | `jobs` ships the same mechanism and `D-142` was planned to name only its enum | **YES** | `D-142:178` is a full section, *"What `jobs` already does, and why a receipt is not one"*, naming `EnqueueOnce`, `PlacementOnce`, `samePayloadDigest`, `findIntent`'s `SELECT … FOR UPDATE`, and four reasons it is not this mechanism (`jobs.Unique` compares no payload; a receipt must be readable later by key; `jobs` stores a hash with no preimage; there is no horizon). |
| GAP-6 `[high]` | S6 added four doc files and a guide and ran eight of the checks that police them | **YES** | S6's checkpoint was widened from eight to ten; `TestEveryGoFenceInTheEventSourcingGuideIsCompiled` (`scripts/docs_test.go:1843`) and `TestNoDocCallsASupervisorMethodTheTypeDoesNotHave` (`:1887`) are the two added. |

### `EVENTSOURCE_P5_USECASES_GAPS.md` — one `[critical]`, ten `[high]`

These are spec-round findings; what matters at the gate is whether the *implementation* carries the
closure. Every one does.

| # | Finding | Closed by code? | Evidence |
|---|---|---|---|
| GAP-1 `[critical]` | the fingerprint covered the expected version, so `Repeated` was unreachable in the one scenario ES-07 exists for | **YES** | `event/repo.go:203-212` (`digestOf`) hashes the composed stream and each record's type/revision/payload — **no version**. **Driven:** the same decision fingerprints identically at v1 and at v2 after the stream moved under it (`sha256:a6a7d486…` both times), and different content differs. The anchor is *recorded* as `Receipt.First - 1` and never compared (`event/receipt/receipt.go`). |
| GAP-2 `[high]` | `Repeated` was reachable from a row recording no events, and a no-op decision was the ordinary way to make one | **YES** | `claim.go:110` — `!won && !held.Complete` → `ErrIncomplete`, **no verdict**. **Driven both sides:** a claim that committed without its completion resolves `Incomplete` and its retry is `ErrIncomplete`; a genuine no-op (`Append` with an empty batch) records `complete=t, range 0..0` and its retry is `Repeated`. Confirmed in `psql`: `stray-complete-… | 0 | 0 | f` beside `no-op-… | 0 | 0 | t`. |
| GAP-3 `[high]` | the claim protocol was a four-step orchestration every step of which was ignorable | **YES** | `receipt.Once` (`claim.go:126`) owns the order; `Claim` returns a **non-nil error** beside `Collided` and for an unresolved prior claim; `Held.Complete` refuses a verdict that is not `Recorded`. |
| GAP-4 `[high]` | `Complete` and `Resolve` carried no placement check while `Claim` carried three | **YES** | `Held.Complete` carries five refusals (`claim.go:151-190`) and `Resolve` carries the mirror (`resolve.go:outsideEveryTransaction`). **Driven:** a completion outside any transaction, a completion on a *different* transaction, and a `Resolve` **inside** the writing transaction are all `ErrSpec`. |
| GAP-5 `[high]` | a receipt covered one `Digest` over one stream, so an operation appending twice was half-fingerprinted | **YES** | **Driven:** a completion with another aggregate's commit → *"this commit is another aggregate's, and one operation key covers one append to the stream the claim names"*; a second `Complete` on one `Held` → *"this claim has already been completed"*. |
| GAP-6 `[high]` | the horizon compared an application clock against a ledger clock and `Issued` was mandatory | **YES** | `recorded_at` is `statement_timestamp()` and never a parameter; `Horizon` is `now() - $1::interval` computed in SQL. `ResolveSpec.Issued` is **optional** — `standingOfAnAbsentRow` returns `Unresolved` on the zero instant. **Driven:** a swept row with `Issued` before the horizon → `Expired`; the same row with **no** `Issued` → `Unresolved` with a non-zero `Horizon`; a never-spent key → `Unresolved`. |
| GAP-7 `[high]` | a `MarkOf` mark has no envelope, so the park question was undefined for a door the spec admits | **YES** | `wait.go:299` refuses `len(spec.Until.sequences) == 0 && !absent(spec.Park)`. **Driven:** `Observe` → `MarkOf` → `Wait` with the queue is `ErrSpec`; with `Park` nil it reaches the barrier. |
| GAP-8 `[high]` | `Wait` had no exit for a poll that fails | **YES** | The fifth exit is `waiting`'s switch (`wait.go:345-358`): terminal on poll 1, polled through after it, and the deadline wraps the last poll's refusal as a third `%w` (`stopped`, `wait.go:491`). **Driven** in both shapes. |
| GAP-9 `[high]` | `WaitSpec` restated the projection's declaration, three fields silently wrong-able | **YES** | `WaitOf(spec, over)` (`wait.go:68`) derives through `withDefaults(spec)` — the correction P-1 names — so a `Park` dropped beside a non-`ParkSequence` policy is dropped here too. Used by the driver throughout. |
| GAP-10 `[high]` | nothing scoped a wait to the generation the caller's read resolves to | **YES** | `WaitSpec.Generations` + `ErrGeneration`. **Driven:** a wait on a retired generation refuses; a cutover **inside the reaching poll** refuses with the ownership row read **twice on that one poll** (`Active` call count = 2, `Polls` = 1). |
| GAP-11 `[high]` | `Park.Holes` was asked outside a unit too, so the one-sentence announcement was wrong | **YES — by deletion** | `Wait` no longer calls `Holes` at all: the only non-test call site left is `event/projection/generation.go:280`, inside `Cutover`. `Visibility` has no `Holes` field (`docs/api/surface.md`). `Readiness` still has one and that is the cutover's. |

### Section gaps

| # | Finding | Closed by code? | Mutation |
|---|---|---|---|
| S1 GAP-1 `[high]` | `Digest`'s third step was a mutation survivor; two different unencodable decisions shared one fingerprint | **YES** — `event/repo.go:184-188` | **M4.** Loop deleted → `TestADigestIsTheBytesThisAppendWouldWrite/a_malformed_batch_is_refused…` reports *"two different decisions of one fact … are one fingerprint 551f9e1a…"*. Restored, sha256 `64ad9830…`. |
| S1 GAP-2 `[high]` | the digest preimage is a durable cross-build format with no frozen vector | **YES** — six golden vectors in `TestTheDigestPreimageIsFrozen` (`event/digest_test.go:362`) | **M5.** `binary.BigEndian` → `LittleEndian` → *"a batch of no records at all digests to 66d33f5b… where every fingerprint this framework has written was d332cf61…"*. Restored. |
| S2 GAP-1 `[high]` | the caller's own deadline/cancellation inside a poll answered `ErrTopology` | **YES** — `wait.go:350-355` | **M2.** Branch deleted → `TestADeadlineSaysWhichKindOfNotYetItWas` fails on two arms: the first-poll deadline (*"answered … this topology change is not one this projection can make"*) and the in-poll cancellation. Restored, sha256 `8eb01293…`. |
| S2 GAP-2 `[high]` | the mint verified only a page's **first** envelope | **YES** — `honest()` (`wait.go:192`) carries all three of `Repo.checkPage`'s arms | **M1.** Reverted to `read[0]` → `TestCommittedRefusesTheSixItCannotMint` fails on three subtests and `TestNoRefusalOfAWaitNamesAPositionOrAKey` on one. Restored. |
| S4 GAP-1 `[high]` | a wait reaching on its **first** poll never re-read the ownership row | **YES** — the `if !first` guard is gone from `poll` (`wait.go:385-416`) | **M3, both tiers.** Guard re-added → unit `TestACutoverUnderAWaitIsRefusedRatherThanAnswered` answers `{Reached:true At:6 … Polls:1}` and `<nil>`; live `TestACutoverThatCommitsWhileAWaitIsRunning` answers `{Reached:true At:2 … Polls:1}` and `<nil>`. Restored. |
| S4 GAP-2 `[high]` | an ordinary deadline inside a poll read as *"the store failed"* | **YES** — `expired()` (`wait.go:483`) drops the poll's refusal when it **is** the context error | **Driven.** A checkpoint store whose `Load` blocks until the budget expires: `Polls=1 ErrNotVisible=true ErrTopology=false`. Control beside it: a **non-context** refusal on poll 1 is still `ErrTopology`. |
| S6 GAP-1 `[high]` | the new usage guide's adoption path did not compile | **YES** | `docs/usage-guides/event-sourcing.md:159` reads `event.Bind(event.Open(store), Orders)`; `:155` carries `DB: db`; `:268` reads `projection.NewCover(projection.Whole())` with the paragraph at `:279` saying a `Cover` is not a `Partition`; `supervisor.Add` appears **nowhere** in `docs/` (`:44` and `:220` read `runtime.Auto(following)`). `_examples/event-guide` builds, vets and tests under `make examples`. |

**S3 and S5 were green in round 1** — no `[critical]`, no `[high]` — so there was nothing blocking to
re-check. Their `[medium]`/`[low]` findings stand in the backlog untouched, as the policy requires.

**No blocking finding is closed by a note.** Every row above names a file, a line, a driven answer or
a mutation.

---

## 2. The hard states, driven from outside this repository

`/tmp/p5verify` is its own Go module — `module example.com/p5verify`, `replace
github.com/frostgrove/vv => /home/user/ws/apps/photon/tmp/frostgrove/framework` and the same for
`event/eventpg` — built with `GOWORK=off` and using **only the exported surface**. It writes its own
`receipt.Ledger`, `projection.Park`, `projection.Generations` and read model over `database/sql` +
`pgx`, exactly as a consumer would, and it drives the live PostgreSQL the repository's own gate uses.

**55 assertions, 0 failures.** The minimum the brief names is covered, and each row below is a real
answer the program printed.

| Hard state | What actually happened |
|---|---|
| **`Resolve` against a transaction still in flight** | The writer holds its transaction open on its own connection after `Once` claimed, appended and completed. A resolver on the pool answers `[standing unresolved]` with a non-zero `Horizon`, **never `Found`** and nothing that reads as a rollback. After the commit the same key resolves `[standing found] range=1..1`. A `Resolve` issued *inside* a transaction is `ErrSpec`. |
| **An expired receipt versus an absent one** | Retention 2 s. A row written, then slept past, then swept by the application's own `DELETE` (4 rows removed): with `Issued` **before** the horizon → `[standing expired]`. A key never spent, `Issued` now → `[standing unresolved]`. The **same swept row with no `Issued` at all** → `[standing unresolved]` with the ledger's horizon carried back. |
| **The same operation key with different content** | `ErrCollision`: *"this key holds a complete row whose fingerprint is not this operation's"*, verdict `[verdict collided]`. **Nothing was appended** — the stream stayed at 1 event and the append counter did not move. The control beside it: the *same* content answers `[verdict repeated]` with the first attempt's range and no append. Confirmed in `psql`: one row for that key. |
| **A wait on a parked sequence** | `ErrParked`, `{Reached:false Behind:3 Parked:true Polls:2}`. **The control that makes it mean something:** the identical mark with `Park` nil reads `{Reached:true Behind:0}` — so the assertion is about the queue and not about the watermark. This is «scan checkpoint после parking не доказывает применение события», driven. |
| **A wait across a cutover** | Two shapes. (a) The cutover committed **before** the wait: `ErrGeneration`, never `Reached`. (b) The cutover committed **inside the poll that would have reached** — a `Generations` whose first `Active` answers the wait's generation and every read after it answers another: `ErrGeneration` with `Polls=1` and `Active` called **twice on that one poll**. |
| **A wait that times out** | `ErrNotVisible` **and** `context.DeadlineExceeded`, `{Polls:14 Behind:39}` — the last readable poll's `Visibility`, never a bare `DeadlineExceeded` and never the zero value. Beside it, a **cancellation** answers `context canceled` bare with `Visibility{}` and is **not** `ErrNotVisible`. And a deadline landing **inside the first poll** answers `ErrNotVisible`, not `ErrTopology`. |
| **A historical read at a version whose prefix holds an unsupported revision** | An event written through the raw `Store.Append` at `Revision: 9` for a fact retaining 1. `StateAt(v1)` → `{Total:10}`; `StateAt(v2)` → `{Total:30}`; `StateAt(v3)` → `event.ErrRevision` *"revision 9 against the 1 this fact retains"* **and the zero state** — a refusal, never a partial. `Load` over the same stream refuses byte-identically. `StateAt(v9)` past the end and `StateAt(v0)` → `event.ErrVersion` and the zero state. `StateAt` returns `(Order, error)` — **no append token**. |
| **A snapshot whose fold changed under it** | **Not constructible, and that is the shipped answer.** ES-09 shipped as `D-145`, a contract with no code. `event.Repo`'s methods are exactly `[Append Authority Digest Load StateAt Within]`; no identifier matching `snapshot` is declared under `event/`. §3 walks the appendix. |

Extra states driven for the blocking findings that needed one: a store lying about a page's **second**
envelope (`event.ErrBackend`, mark zero) and a store overrunning its own published page (same); a
completion with another aggregate's commit; a second completion of one claim; a no-op decision and
its `Repeated` retry; `Observe`→`MarkOf`→`Wait` with and without a queue; `Reached` refusing a
barrier asked of its own generation; one projection's mark waited on another's spec.

**Rows read in `psql`, not trusted from Go:**

```
 key                          |          fp          | first | last | complete
------------------------------+----------------------+-------+------+----------
 no-op-…                      | sha256:8c78a300226aa |     0 |    0 | t
 second-complete-…            | sha256:17283a326651a |     1 |    1 | t
 stray-complete-…             | sha256:dba7e5b7fdfcb |     0 |    0 | f     <- the Incomplete defect row
 collision-…                  | sha256:e2a205280cccc |     1 |    1 | t     <- one row, not two
```

The two zero-range rows are the whole of USECASES GAP-2: `complete` is what tells a completed no-op
apart from a claim nobody resolved, and the column carries it.

---

## 3. The appendices, part 3 walked sentence by sentence

### ES-05 — ожидание конкретного изменения в read model

| Sentence | Verdict |
|---|---|
| «ждать подтверждённую stream/version либо store-issued barrier конкретного projection generation, а не "пока lag станет нулём"» | **LANDED.** Two doors and no third: `WaitSpec.Committed(ctx, store, commit)` reads the commit's whole range back **after** the caller's transaction committed, and `MarkOf(Barrier)` over `Observe`. `Of` is an `Identity` carrying the generation. No head, no lag, no "caught up" — `UC-036`'s *Out of scope* says so and `docs/api/surface.md` publishes no such field. Both doors driven. |
| «Scan checkpoint после parking не доказывает применение события.» | **LANDED.** `poll` asks the park **first, on every poll**, before the census (`wait.go:385-399`), and `ErrParked` is terminal. Driven with the `Park`-nil control that shows the watermark alone would have said reached. |
| «Deadline возвращает timeout/degraded; stale-read разрешается явно.» | **PARTIAL.** *Timeout* landed and is driven: `ErrNotVisible` + `context.DeadlineExceeded` + the filled `Visibility`, with no stale-read flag anywhere — the branch is the caller's, which is "explicitly". ***Degraded* did not ship.** There is no degraded verdict and no paragraph saying why a wait cannot report a phase; `projection.State`'s `PhaseDegraded` is in-process and a waiter is usually not that process, and **that reason is written down nowhere in the repository** — it is recorded as `[medium]` backlog item 1 under `## P5`, whose own text says *"Owed: one paragraph … or a second verdict that does."* An owed paragraph is an open item, not a recorded argument. **Does not block** under the delivery policy; named here because a partial must be. |
| «Успех даёт видимость до барьера, не глобальную linearizability и не свежесть чужой read replica.» | **LANDED.** `UC-036` *Out of scope* carries all three clauses verbatim in its own words — *"Global linearizability"*, *"Another replica's freshness"*, *"'The consumer is caught up'"* — and `docs/modules/en/projection.md:525` repeats the first. |

### ES-07 — выяснение результата неопределённого append

| Sentence | Verdict |
|---|---|
| «operation identity + fingerprint + диапазон событий записываются рядом с append в той же caller-owned SQL-транзакции» | **LANDED.** `receipt.Receipt{Key, Fingerprint, Stream, First, Last}`; `sameUnit` (`claim.go:207`) requires the store to state transactions, one of its own bound to the context, and the ledger to resolve that **same** `Authority` — five refusals if not. `[[D-118]]`'s outbox rule holds: no second durable-intent table, the row is the application's own. |
| «Повтор проверяет прежнюю квитанцию; другое содержимое с тем же ключом — конфликт.» | **LANDED.** `Repeated` / `ErrCollision`, both driven, the collision as an **error** and not only a verdict. |
| «Отсутствующая квитанция не доказывает rollback, пока исходная транзакция не разрешилась.» | **LANDED.** `Unresolved` is the state named rather than guessed at; `Resolve`'s doc says *"AN ABSENT ROW IS NOT A ROLLBACK"* and names `idle_in_transaction_session_timeout` as the deployment's lever. `[[D-143]]` is the decision. Driven against a genuinely open transaction. |
| «Retention квитанций ограничивает окно проверки; framework не переигрывает доменное решение.» | **LANDED.** `Ledger.Horizon` + the `Expired` standing bound the window; the sweep is the application's (`receipt.go`: *"The sweep is the application's; nothing here prunes"*) and the example runs it as a supervised `runtime.Every`. `Once` calls the caller's own `work` and never re-decides; `Store.Append` gains no deduplication — `[[D-142]]`. Driven end to end. |

### ES-08 — историческое состояние по версии

| Sentence | Verdict |
|---|---|
| «читать полный префикс до указанной версии, не произвольный фильтр событий» | **LANDED.** `Repo.StateAt` pages through the same `ReadStream` a load does and truncates the last page in Go (`repo.go:331-357`); the page is checked whole **before** truncation. No filter, no predicate, and `Store.ReadStream` gained no ceiling. |
| «Результат не выдаёт append token.» | **LANDED.** `StateAt(ctx, ID, Version) (S, error)` — driven by reflection over the shipped signature. `[[D-144]]` records *"the absence of a second return **is** the mechanism"* and that a `Prefix`/`Provenance` struct was designed and refused. (The appendix's part-5 DX sketched `state + provenance`; part 5 is explicitly not an approved API and the divergence is argued in D-144.) |
| «Первая граница — точная версия; timestamp не объявляется business-time и не заменяет порядок commit.» | **LANDED.** `[[D-144]]`'s invariant: *"no timestamp parameter, overload or option anywhere"*. The surface confirms it. |
| «Неподдерживаемая revision или отсутствующий префикс возвращают отказ, не частичное состояние.» | **LANDED.** Driven: `ErrRevision` + the zero state over an unsupported revision; `ErrVersion` + the zero state past the end and at version 0, and no refusal names a version, a key or a head. |
| part 4's «[[UC-032]] … должен отдельно описать новый read-only сценарий» | **LANDED.** `UC-038` ships; `UC-032` gained a `See also` and **no clause of `What must hold` and nothing in `Out of scope` changed** — verified by reading the diff, which is 20 added lines, all under a new `## See also`. |

### ES-09 — совместимые snapshots с безопасным fallback

**LANDED as a deliberate deferral with the argument in the repository, and the code deliberately does
not ship.** `docs/ai/decisions/D-145-a-snapshots-contract-accepted-without-code.md` is in the doc
layer, is indexed, amends `[[D-132]]` without superseding it, and carries all five of part 3's
sentences as its contract: the five bindings compared **before deserialisation** (with Axon's
`.filter(…)` preceding `.map(deserialise)` cited as the proof the ordering is the mechanism); only a
confirmed version loads and the whole tail replays; incompatibility or corruption means full replay
**observable in the return value** while an unreadable **event** is a refusal never hidden by the
fallback; the measured benefit and the snapshot+tail ≡ full replay proof are both gates; history is
never deleted.

The gate was **asked before the file was written** and did not open: `BenchmarkStreamReplay` measured
12.91–14.88 ms at 10 000 events and 108.4–111.3 ms at 100 000, putting the crossing at ~45 000 events,
inside `[[D-132]]`'s recorded band — and Gate 1 asks for *a named deployment's own p99*, which does not
exist. The roadmap says so in its own words: *"Статус: контракт — [[D-145]]; оба gate не выполнены."*

**The refusal is enforced, not merely asserted.** `TestNoSnapshotAuthorityIsDeclaredOrPromised`
(`scripts/projection_test.go:276`) was **widened, not narrowed**: `checkedEventPackages` now requires
six surfaced packages including `event/receipt`. **Mutation M7:** appending `type Snapshot struct{
Version uint64 }` to `event/receipt/key.go` makes it report *"../event/receipt/key.go:69:6 declares
Snapshot, and full replay is the only authority a folded state has here"*. Restored byte-identically.

Three ES-09 `[medium]`s stand in the backlog (the `SnapshotFilter` composition rule, «оператор может
отбросить snapshot», and the panic/`recover` assumption) and are correctly not fixed.

---

## 4. The gate commands

All run 2026-09-12 from a tree that is byte-identical to the one the phase shipped (every mutation
restored and sha256-compared).

| Command | Result |
|---|---|
| `make check` | **GREEN, all arms.** `check-deps · check-tiers · check-utils · check-triplets · check-todo · check-replaces · check-tidy · check-otel-schema · check-otel-module · check-workspace · check-event-kernel` — every one `ok`. `check-tidy` and `check-replaces` are green, so PLAN GAP-2's correction holds. |
| `make vet` | **GREEN**, every module plus `./_examples`. `VET_EXIT=0`. |
| `gofmt -l .` | **silent.** |
| `make examples` | **GREEN.** `_examples/event-guide` builds, vets and tests; `event-receipts` and `event-wait` build and vet. `EX_EXIT=0`. |
| `make api` | Regenerated; **diff against the committed `docs/api/surface.md` is 0 lines.** |
| tagged suite ×3 | `ok github.com/frostgrove/vv/event/eventpg 128.489s` · `130.581s` · `133.608s`. Three consecutive green runs, `-race -count=1 -tags=integration`. |
| unset DSN | **FAILS, does not skip:** *"FROSTGROVE_EVENTPG_TEST_DSN is not set, and this suite proves nothing without a database, so it fails rather than skipping"*, `FAIL … 0.002s`. |
| `make unit` | **NOT GREEN, and this report does not claim it is.** See below. |

### `make unit` — three red arms, all foreign, and two of them are hidden

`make unit` exits 2. The gate brief names one foreign red. **There are three**, and `make unit`
never reaches two of them because it aborts at the first failing module:

| Arm | Red | Reproduces at `HEAD`? |
|---|---|---|
| `./scripts` — `TestNoI18nPackageCostsMoreThanItsErrorSeam` | *"the i18n extension reaches github.com/go-json-experiment/json/jsontext outside its error seam"* | **Yes** — the named foreign red. Not touched, not allowlisted. |
| `./i18n` — `github.com/frostgrove/vv/i18n/cmd/vv-i18n`, **29 `--- FAIL:` lines** (`TestExtractUsage*`, `TestExtractCommand*`) | *"known nested container usage = {… Complete:false}"* and siblings | **Yes.** Verified in a clean `git worktree` at `f161aee`: 29 failures, identical. |
| `./test` — `github.com/frostgrove/vv/test/auditflow`, `TestGeneratedAuditProfilesSeparateServingRuntimeFromDeploymentAuthority` | *"generated serving health check: audit serving runtime is not ready"* | **Yes.** Same worktree, same failure. |

The phase-5 change is entirely uncommitted, so `HEAD` **is** the pre-phase-5 tree — which makes the
worktree comparison decisive rather than suggestive. All three arms are the owner's i18n and audit
work. **None is phase 5's, and none is counted against it.** But the section reports that said *"one
`--- FAIL:` line and no other"* were reading a `make unit` that had already stopped: the sentence was
true of the output and false of the tree, and the two extra arms are named here so the next reader
does not inherit the same blind spot.

Every package under `event/` — `event`, `event/eventmemory`, `event/eventtest`, `event/projection`,
`event/receipt` — is green under `-race`, as are `./scripts/api-surface` and every other root package.

---

## 5. Mutations applied, and what caught each

Seven fixes reverted, seven caught, seven restored **byte-identically** (sha256 recorded).

| # | Mutation | Caught by | Restored |
|---|---|---|---|
| M1 | `honest()` → `read[0]`-only, cap dropped | `TestCommittedRefusesTheSixItCannotMint` (3 subtests) + `TestNoRefusalOfAWaitNamesAPositionOrAKey` | `wait.go` `8eb01293…` |
| M2 | the `ctx.Err()` case deleted from `waiting` | `TestADeadlineSaysWhichKindOfNotYetItWas` (2 arms) | `wait.go` `8eb01293…` |
| M3 | the first-poll exemption re-added to the ownership re-read | unit `TestACutoverUnderAWaitIsRefusedRatherThanAnswered` **and** live `TestACutoverThatCommitsWhileAWaitIsRunning` | `wait.go` `8eb01293…` |
| M4 | the `change.err` loop deleted from `Digest` | `TestADigestIsTheBytesThisAppendWouldWrite` | `repo.go` `64ad9830…` |
| M5 | `binary.BigEndian` → `LittleEndian` in `eightBytes` | `TestTheDigestPreimageIsFrozen` | `repo.go` `64ad9830…` |
| M6 | one comment appended to the example ledger's `claimSelectStatement` | `TestTheExampleLedgerIsTheOneTheLiveSuiteProved` | `main.go` `3e49701a…` |
| M7 | `type Snapshot` declared under `event/receipt` | `TestNoSnapshotAuthorityIsDeclaredOrPromised` | `key.go` `9d08772e…` |

M3 is the one that matters most for the "nobody verified the fixes" question: it is the only blocking
finding whose closure claimed evidence on **two** tiers, and both tiers do fail when it is reverted.

---

## 6. What is still open

Nothing blocking. For the record, and none of it is to be fixed here:

- **ES-05's «degraded»** — `[medium]`, backlog item 1 under `## P5`. The only appendix sentence that
  is a partial rather than a landing. What is owed is one paragraph or one verdict.
- **The `[medium]`/`[low]` backlog** — S1 GAP-3/4/5, S3 GAP-1…GAP-9, S4 GAP-3…GAP-6, S5 GAP-1/2/3,
  S6 GAP-2…GAP-6, and the eleven from the spec round. All recorded, all untouched, which is what the
  policy asks for.
- **S6 GAP-5** is worth one line because it is about this gate's own machinery: the doc check that was
  to hold *"UC-032 is not silently widened"* did not ship. It is `[medium]` and stays there — but it
  means that clause is held by review rather than by a test, so **this gate read the `UC-032` diff by
  hand** (20 added lines, all under a new `## See also`, nothing in `What must hold` or `Out of scope`)
  rather than trusting a check that does not exist.
- **Three foreign red arms**, §4. The owner's call, and two of them are not yet written down anywhere
  else.
