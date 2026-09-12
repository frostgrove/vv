# EVENTSOURCE P5 — S5 (the receipt and the bounded read, live) — GAPS

## Round 1 — econv-code-reviewer — 2026-09-12

**Verdict: GREEN. No `[critical]`, no `[high]`. Three findings go to
[`EVENTSOURCE_BACKLOG.md`](EVENTSOURCE_BACKLOG.md) `## P5` as items 51–53 and are NOT fixed**, per
the 2026-09-08 delivery policy.

Everything below was **driven**, not read. The section's whole deliverable is two integration test
files under `event/eventpg`, so this review spent its budget on (a) re-running the pasted
checkpoint end to end, (b) attacking the two tests the section rests on, (c) constructing six live
states the section makes possible that no test in the tree exercises, and (d) reading rows out of
PostgreSQL 17.9 with `psql` rather than through Go.

**The pasted checkpoint output is real.** Every arm was re-executed at HEAD against PostgreSQL
17.9 (`postgres://vv:vv@localhost:55432/vv`, container `vv-postgres-1`, `PostgreSQL 17.9 on
x86_64-pc-linux-musl`) with `FROSTGROVE_EVENTPG_TEST_PSQL='docker exec -i vv-postgres-1 psql -U vv
-d vv'` set exactly as the section's record says. Transcript and numbers in §1. One number in the
record is off by one and it is the `+++` header of a diff — §1, note 7.

**The two most important tests were attacked and both held.** Two mutations, one of the kernel and
one of `event/receipt`, each killed by the tests the section's own table names. Both files are
byte-identical to their originals afterwards and `check-event-kernel` is `ok` — §2.

**No ES-07 or ES-08 nuance the study records is dropped.** Eight for eight on ES-07, six for six on
ES-08, each located in the code or in the contract that owes it — §3. The two the study calls out
as "do not survive re-derivation" (ES-07 nuance 1, the idempotence window; ES-08 nuance 1, the
refusal shape) were driven live and hold.

**Contract conformance is clean in both directions.** All 16 named tests exist, are listed by the
checkpoint's own `-list` pattern, and each carries the arms the plan and [SPEC] §6 items 11–22 and
24 name — §4. S5 publishes **no exported name**: `make api` regenerates `docs/api/surface.md`
byte-identical and its whole 116-line diff is S1–S3's.

**Kernel boundary is clean.** `git status --porcelain event/` lists exactly two new paths for this
section, both under `event/eventpg`, which `event_kernel_manifest` excludes by construction
(`scripts/checks.sh:530`). `check-event-kernel: ok` against the committed manifest — §5.

**The one red arm is foreign and is named.** `make unit` reports exactly one `--- FAIL:` line,
`TestNoI18nPackageCostsMoreThanItsErrorSeam` in `./scripts`. It is the owner's i18n work, untouched
here — **so `make unit` is not green and this report does not claim it is.** `make check` is green
on all **eleven** arms.

---

## 1. The checkpoint, re-executed

| Arm | Claimed in [PLAN] § *S5 — executed* | Measured now |
|---|---|---|
| `-list` count of the sixteen | `LISTED=16` | `LISTED=16` |
| the sixteen named, `-race` | `ok … 3.866s` | `ok … 3.871s` |
| the whole tagged package, `-race` | `ok … 127.352s` | `ok … 133.748s` (`RUN1=0`) |
| and again | `ok … 128.706s` | `ok … 125.945s` (`RUN2=0`) |
| `BenchmarkStateAt` 1 % | 1 071 – 1 236 ns/event | 1 233 · 1 350 · 1 439 ns/event |
| `BenchmarkStateAt` 50 % | 1 309 – 1 424 ns/event | 1 219 · 1 248 · 1 279 ns/event |
| `BenchmarkStateAt` 100 % | 1 205 – 1 371 ns/event | 1 217 · 1 228 · 1 422 ns/event |
| `./scripts/checks.sh event-kernel` | `check-event-kernel: ok` | `check-event-kernel: ok` |
| `gofmt -l .` | silent | `0` |
| `go vet -tags=integration ./event/eventpg/` | clean | clean, exit 0 |
| `go test -race -count=1 ./event/...` | five packages | five packages, all `ok` (7.596s 1.506s 4.478s 6.115s 1.020s) |
| `make check` | eleven `ok` arms | eleven, `check-event-kernel` among them, exit 0 |
| `make unit` | one foreign red arm | exactly one `--- FAIL:`, `TestNoI18nPackageCostsMoreThanItsErrorSeam` |

Seven further things this review measured that the record does not claim:

1. **Shuffled order is green.** `-shuffle=on` over the sixteen: `ok … 2.667s`. The section is not
   order-dependent.
2. **An unset DSN fails rather than skips.** `env -u FROSTGROVE_EVENTPG_TEST_DSN` →
   `FROSTGROVE_EVENTPG_TEST_DSN is not set, and this suite proves nothing without a database, so it
   fails rather than skipping.` `FAIL`.
3. **The `psql` cross-check is live and is not a no-op.** Pointing `FROSTGROVE_EVENTPG_TEST_PSQL`
   at a command that answers something else makes the very first assertion fail:
   `receipt_integration_test.go:502: psql prints [999 …] for the receipt row where this process read
   [1:2:true], so one of the two is not reading the database`. So `asked` really is true in this
   configuration and the cross-checks really ran.
4. **What `psql` itself printed**, captured through a wrapper around
   `docker exec -i vv-postgres-1 psql -U vv -d vv` during
   `TestTwoCallersRaceOneKey`, `TestTheLostConnectionRetryEndToEnd` and
   `TestTwoMisorderedCallersAndTheirOnceControl` — these are the database's own rows, not Go's
   account of them:

   ```
   ### SELECT count(*) FROM "eventpg_s5_race2".events  WHERE … key = 'A-race'      → 1   (READ COMMITTED)
   ### SELECT count(*) FROM "eventpg_s5_race4".events  WHERE … key = 'A-race'      → 1   (REPEATABLE READ)
   ### SELECT count(*) FROM "eventpg_s5_race6".events  WHERE … key = 'A-race'      → 1   (SERIALIZABLE)
   ### SELECT count(*) FROM "eventpg_s5_rolled{2,4,6}".events … key = 'A-rolled'   → 1,1,1
   ### SELECT count(*) FROM "eventpg_s5_*_receipts"                                → 1 on every arm
   ### SELECT count(*) FROM "eventpg_s5_retry".events  WHERE … key = 'A-retry'     → 1   (keyed retry)
   ### SELECT count(*) FROM "eventpg_s5_retry".events  WHERE … key = 'A-naive'     → 2   (naive control)
   ### SELECT count(*) FROM "eventpg_s5_misordered".events … key = 'B-after'       → 2   (claim after append)
   ### SELECT count(*) FROM "eventpg_s5_misordered".events … key = 'C-once'        → 1   (through Once)
   ### SELECT count(*) FROM "eventpg_s5_misordered".events … key = 'A-ignored'     → 2   (never completes)
   ```

   §UC-217's *"exactly one append lands at every level"*, §UC-227's naive control and §UC-251's
   *"the case asserts the damage"* are therefore facts PostgreSQL printed.
5. **The manifest fence.** S5's own `diff` requirement is that the manifest did **not** move. Both
   files it adds are under `event/eventpg`, `check-event-kernel` is `ok` against the committed
   manifest, and `git status --porcelain event/` lists no new path outside `event/eventpg` that S5
   owns.
6. **`make api` is idempotent here.** Re-run; `git diff --numstat docs/api/surface.md` is `116 0`
   before and after. No S5 name appears in it.
7. **The record says the `make api` diff is "S1–S3's 117 lines"; it is 116.** `git diff | grep -c
   '^+'` answers 117 because it counts the `+++ b/docs/api/surface.md` header; `--numstat` answers
   `116 0`. Recorded for accuracy, not as a finding — the substantive claim (nothing of S5's is in
   it) is true and was read line by line again here.

**Metrics counted, not eyeballed.** Two new files, `event/eventpg/receipt_integration_test.go`
(1 931 lines, 58 top-level funcs, 23 `t.Run` arms) and `event/eventpg/stateat_integration_test.go`
(383 lines, 6 `t.Run` arms); 2 314 lines total; **16** `Test` funcs and **1** `Benchmark`; **0**
non-test files; **0** exported symbols; **0** `t.Parallel`; **0** `t.Skip`; **0** `nolint`/`TODO`/
`FIXME`. `event/receipt` is 7 non-test files (`scripts/projection_test.go`'s new floor asks for
seven and gets seven).

---

## 2. The two most important tests, attacked

Both mutations are of code the section proves rather than of the section's own fixtures, and both
are in the plan's own eighteen-row table — which is the point: this re-ran two of its rows rather
than trusting the row.

| Mutation | Where | What failed |
|---|---|---|
| the digest covers the version the token was loaded at — `Digest` mixes `at.version` into the preimage | `event/repo.go:193` | `--- FAIL: TestTheLostConnectionRetryEndToEnd` **and** `--- FAIL: TestTheCollisionTableLiveBesideTheVersionVariant/the_version_variant_answers_Repeated_with_the_first_attempt's_range` |
| a claim answers `Recorded` whatever the insert reported — `verdictOf`'s `case won:` becomes `case true:` | `event/receipt/claim.go:274` | `--- FAIL: TestTwoCallersRaceOneKey` at **all three** levels: `/read_committed`, `/repeatable_read`, `/serializable` |

Neither test survives its mutation, and the first is killed by the two halves of one decision —
the process that died and the process that retried — exactly as the section's table claims.
Afterwards: `diff` against the pre-mutation copies is empty for both files, `gofmt -l .` is `0` and
`check-event-kernel: ok`.

---

## 3. Nuance conformance, ES-07 and ES-08

Every numbered nuance of [`EVENTSOURCE_P5_STUDY.md`](../usecases/EVENTSOURCE_P5_STUDY.md) §ES-07 §2
and §ES-08 §2, located.

**ES-07 — eight for eight.**

1. *Idempotence is a property of `(stream, expectedVersion, ordered ids)`, and a retry at the new
   version leaves the window — a receipt keyed on the caller's own key does not have this failure
   mode.* → `event/repo.go:160-166` states it as the reason the version is out of the preimage;
   proved live by `TestTheCollisionTableLiveBesideTheVersionVariant`'s fourth arm (`at.Version() ==
   3`, verdict `Repeated`, range `1..2`) and by `TestTheLostConnectionRetryEndToEnd` across a real
   process boundary (`at.Version() == 1`, fingerprints asserted equal). Killing it kills both — §2.
2. *A partial overlap is its own answer; a fingerprint over the whole range reproduces it for
   free.* → `digestOf` (`event/repo.go:203-211`) digests the batch, not the records; the
   length-prefix arm of `TestADigestCollidesOnAByteAStreamAndAnOrder` pins the injectivity a
   differing batch length rests on.
3. *An idempotence claim is only as strong as the concurrency check it is anchored to; vv has no
   "any version" append and the receipt must not introduce a weak side.* → `Store.Append` is
   untouched (`SchemaVersion = 2`, no migration, no signature change), and `Claim` is a second write
   beside the append rather than a change to it. Backlog item 50 already records that the two are
   **layered** and that nothing says so.
4. *Nothing about this is a retry; the server never retries.* → nothing in `event/receipt` loops,
   sleeps or retries; `go func` count in the package is **0**.
5. *The uncertain window vv has is the caller's `COMMIT`, not `Append`'s.* → `resolve.go:72-84`
   carries it verbatim, including `idle_in_transaction_session_timeout` as *"the deployment's lever
   and not this package's"*.
6. *"An absent receipt does not prove rollback" needs a third answer.* → `Unresolved`, and
   `TestUnresolvedWhileOpenAndAfterARollbackAreIndistinguishable` asserts the open and the
   rolled-back arms are the **same value** (`afterRollback.Standing != whileOpen.Standing ||
   afterRollback.Receipt != whileOpen.Receipt`), with the resolve measured not to block.
7. *Retention is what makes the window finite.* → `Horizon` + `Expired`; four standings across two
   horizon spellings, plus two skew arms. Driven further in §4 PROBE A and PROBE B.
8. *The port's fail-closed cross-aggregate check — the row records which aggregate the key was
   spent on, and a redelivery routed elsewhere is rejected rather than answered with the unrelated
   aggregate's state.* → reproduced by construction: the composed stream opens the digest preimage,
   so `TestTheCollisionTableLiveBesideTheVersionVariant`'s *"a different stream, with identical
   records"* arm answers `Collided` with `ErrCollision`. **Not** reproduced as a second comparison
   on the repeat path — see item 53 / backlog item 39, already `[low]`.

**ES-08 — six for six.**

1. *The appendix refuses what Marten returns `null` for, and that is deliberate.* → four refusals,
   all `ErrVersion`/`Err*`, never the zero state beside a nil error.
   `TestPastTheEndAnEmptyStreamAndVersionZeroLive` asserts `pastTheEnd.Error() ==
   neverWritten.Error()`.
2. *Marten pays a full prefix read to notice; vv knows at the same point and for the same money.*
   → measured: a bound past the end of a five-event stream at page 4 costs **2** statements, the
   same as the load. Driven independently in §4 PROBE D on an eight-event stream at page 4: bound 9
   and bound 1 000 000 both cost **3**, and so does the unbounded load — no confirming read, no
   runaway.
3. *The `fromVersion` + `state` half-open/half-closed asymmetry.* → not applicable: `StateAt` takes
   no base state and ES-09 ships no code. `TestNoSnapshotAuthorityIsDeclaredOrPromised` is `ok`.
   The plan carries the obligation as **P-13**.
4. *A timestamp is not business time and not commit order.* → `StateAt(ctx, ID, Version)` takes no
   instant; `grep` for `Isolation|SET TRANSACTION` in non-test `event/` is **empty** and so is any
   time parameter. §UC-235's doc sentence is S6's.
5. *An at-version read is correctness, not convenience.* → the whole of §UC-228/§UC-229.
6. *The result must not be appendable.* → `func (this *Repo[S, ID]) StateAt(context.Context, ID,
   Version) (S, error)` in `docs/api/surface.md`. No token, no second value.

---

## 4. The hard states, driven

Six states this section makes possible and that no test in the tree constructs. Each was written as
a temporary `//go:build integration` probe in `event/eventpg`, run against the live database, and
removed; the tree is byte-identical afterwards.

**PROBE A — a swept receipt versus one that was never written.** A completed row aged to
`now() - 3 hours` and then `DELETE`d, beside a key nothing ever claimed, resolved against a
`now() - 2 hours` horizon with the same `Issued`:

```
PROBE swept=[standing expired] receipt={Key:[operation key] Fingerprint:sha256:0000… First:0 Last:0 Complete:false RecordedAt:0001-01-01}
      never=[standing expired] receipt={Key:[operation key] Fingerprint:sha256:0000… First:0 Last:0 Complete:false RecordedAt:0001-01-01}
```

Identical, to the field. The design holds: a swept row and a key nobody spent are one answer, and
the framework does not claim to tell them apart.

**PROBE B — a `Resolve` against an unresolved transaction, with `Issued` below a retention
horizon.** A claim + append + complete held open in a second session; a ledger publishing
`now() - retention` with a one-minute retention; `Issued` ten minutes old:

```
PROBE in-flight-under-retention standing=[standing expired] horizon=2026-09-12 16:44:43.652799+05 issued=2026-09-12 16:34:43.65141+05
PROBE after-the-commit          standing=[standing found]
PROBE: an operation that committed read Expired while it was in flight
```

An operation that was about to commit read **`Expired`** — the standing [SPEC] §"Retention" tells
the caller to **stop** on. It is reachable with zero clock skew. → **backlog item 52 `[medium]`**;
it is neither a false `Found` nor a false "it did not happen", which is why it is not a blocker.

**PROBE C — a historical read at a version with an unsupported revision in the prefix.** Two arms,
`StreamPage: 3`, the broken row planted at version **2** and the bound at version **1** — i.e. the
unreadable row is *above* the requested prefix but *inside* the same page:

```
a wire type this declaration does not know      bound 1 → state="v1" err=<nil>
                                                bound 2 → state=""   err=event: the recorded type names no declared fact: "eventpg.probe.unknown.retired" …
a recorded payload over the bound the schema published
                                                bound 1 → state=""   err=event: the store failed
                                                bound 2 → state=""   err=event: the store failed
```

The kernel's own refusals (`ErrUnknownType`, `ErrRevision`, `ErrUpcast`) refuse the **row** and a
bound below it reads normally; the store's (`ErrBackend`) refuses the **page it scanned**, so a
perfectly readable prefix is refused because of a row the caller never asked for, and *which* reads
survive depends on `StreamPage`. → **backlog item 51 `[medium]`**.

**PROBE D — bounds at and past a page multiple.** Eight events, page 4:

```
bound 8       → state="v8" err=<nil>                       pages=2
bound 9       → state=""   err=…ErrVersion: this stream is shorter than the prefix…   pages=3
bound 1000000 → state=""   err=…ErrVersion…                pages=3
an unbounded load of eight events at a page of 4           pages=3
```

The bound at an exact page multiple costs **one page less** than the load (the short-page probe is
skipped by `at >= upTo`), a bound past the end costs exactly what the load costs, and a bound of
`1 000 000` does not run away. `Version` is `uint64` (`event/identity.go:10`), so there is no
negative bound to smuggle past the `version == 0` guard, and `page[:upTo-at]` is provably inside
`0 < upTo-at < len(page)` on every iteration.

**PROBE E — a `Complete` issued after the claim's own transaction committed.**

```
PROBE a completion issued after the claim's own transaction committed: sql: transaction has already been committed or rolled back (ErrSpec: false)
PROBE the row after that completion: found=true 0..0 complete=false
```

Fail-closed and loud, but it is a **sixth** state `Held.Complete` does not name among its five
refusals, and it surfaces as a bare driver error rather than any `receipt` sentinel. The residue —
a committed append beside a `0..0` incomplete row — is §UC-222's documented defect state, and the
`Held` is burnt before the ledger write (already backlog item 36 `[medium]`). Recorded as part of
item 53.

**PROBE F — a repeat whose row names another stream.** A completed row's `stream_key` rewritten by
hand, then the key re-claimed: verdict `Repeated`, `err=<nil>`, and `held.Receipt().Stream` carries
the rewritten key. `claimAnswered` compares the stream only on a win (`claim.go:260-268`). Harmless
against an honest ledger because the composed stream opens the digest preimage — and already
recorded as backlog item 39 `[low]`.

---

## 5. Boundaries and binding decisions

`git status --porcelain event/` — S5's own additions are exactly:

```
?? event/eventpg/receipt_integration_test.go
?? event/eventpg/stateat_integration_test.go
```

Both under `event/eventpg`, which `event_kernel_manifest` excludes (`scripts/checks.sh:530`), so
S5's *"moves nothing"* holds and `check-event-kernel: ok` against the committed manifest. Every
other modified or untracked path under `event/` belongs to S1–S4 and is inside the plan's
§ *The complete set of paths phase 5 may add to the manifest* — with the two exceptions noted in
item 53.

| Decision | Checked how | Result |
|---|---|---|
| **[[D-126]]** the store chooses no isolation level | `grep -rn "Isolation\|LevelSerializable\|LevelRepeatableRead\|SET TRANSACTION" event/ --include=*.go` excluding `_test.go` | **empty**. The three levels are bound by the caller, in the test, through `crudsql.Postgres(db).WithTxOptions(…)` (`receipt_integration_test.go:342-344`), which is the only side that may. |
| **[[D-118]]** one durable write in the caller's bound transaction, no second durable-intent table | `Claim`/`Complete` refuse unless `Ledger.Transaction(ctx).Same(Store.Transaction(ctx))` (`claim.go:223-245`); `eventpg` ships **no** receipt table — `SchemaVersion = 2`, `git diff event/eventpg/` is one unrelated S4 test file | holds. The table is the application's, created by the test fixture. D-142 is S6's and is scheduled. |
| **[[D-092]]** anything continuous is a `runtime.Runner`; constructors start nothing | `grep "go func"` in `event/receipt/*.go` non-test | **0**. `Claim`, `Once`, `Complete`, `Resolve` all run on the caller's goroutine, inside the caller's unit. |
| **[[D-101]]** migrating is a deployment profile choice | `event/eventpg/schema.go:16` still `SchemaVersion = 2`; no migration statement added | holds. |
| **[[D-128]]** / **[[D-129]]** / **[[D-130]]** / **[[D-133]]** | S5 touches no cursor, no checkpoint, no fence, no runner | not reached; no contradiction found. |
| **[[D-132]]** a measured cost is not a reason to build a snapshot | `BenchmarkStateAt`'s comment (`stateat_integration_test.go:352-355`) says so and the numbers land in backlog item 48, not in a decision | holds. |

`TestNoSnapshotAuthorityIsDeclaredOrPromised` is `ok`: ES-09 ships nothing, as [SPEC] §6 requires.
(The word "snapshot" appears once in `receipt_integration_test.go:46` in its PostgreSQL-MVCC sense —
*"the select is a second statement and therefore a second snapshot"* — which the guard does not
flag and which reads correctly in context.)

---

## 6. Findings

### GAP-1 `[medium][backlog]` — a store's page-level refusal denies a prefix the caller could read, and nothing caller-facing says so

- **Where** `event/repo.go:325-356` (`replay`'s check-then-truncate order) and `event/repo.go:60-65`
  (`StateAt`'s doc comment, the over-read argument); `event/eventpg/stateat_integration_test.go:296-299`
  (the only place the behaviour is written down).
- **What** `StateAt` over-reads up to one page and truncates in Go, and the page is scanned by the
  **store** before the kernel sees it. A row the store itself refuses — `errRowOutsideSchema` →
  `event.ErrBackend` — therefore refuses every bounded read whose bound falls in the same page,
  even when the requested prefix is entirely readable. Driven (§4 PROBE C): with `StreamPage: 3`
  and an over-length payload planted at version 2, `StateAt(ctx, id, 1)` answers
  `event: the store failed` while the same plant with an undeclared **type** answers `"v1"`, nil.
  Which historical reads survive a corrupt row is therefore a function of `StreamPage`.
- **Why this severity** `[medium]`, not `[high]`: the answer is an honest refusal, never a partial
  or wrong state, so §INV-118 holds. The harm is operability — a corrupt row at version 2 denies
  the operator the very `StateAt(…, 1)` they would use to diagnose it — and reproducibility: the
  same call succeeds or fails depending on a store's page size, which no caller can see.
- **Why this timing** Backlog. Closing it properly means the version ceiling on `Store.ReadStream`
  that [SPEC] §"The store contract is not widened" deliberately refused (and §INV-120 forbids
  moving), so the fix inside this phase is a sentence, not code. The plan's own arithmetic —
  *"a ceiling would buy one partial page of I/O"* — is the incomplete part: the over-read also buys
  correctness on a corrupt tail, and that term is missing from the argument.
- **Close criteria**
  - [ ] `StateAt`'s doc comment states that a store that refuses the page it scanned refuses the
        bounded read, whatever the bound, and that the kernel's own row refusals do not.
  - [ ] Both `event.md` module pages carry the same sentence beside `ErrBackend`.
  - [ ] [SPEC] §"The store contract is not widened, and the arithmetic is the argument" gains the
        missing term, or D-143/D-144 records it as accepted.
  - [ ] The plan's own arithmetic sentence is amended or a decision cites this as the accepted cost.
- **Status: open**

### GAP-2 `[medium][backlog]` — `Expired` is reachable for an operation that is in flight, with no clock skew, and the framework's own analysis bounds that window by skew alone

- **Where** `event/receipt/resolve.go:120-125` (`standingOfAnAbsentRow`), `resolve.go:17-22`
  (`Expired`'s own sentence); [SPEC] §"Retention, and why an expired receipt does not read as a
  rollback", the paragraph beginning *"The skew decides between two non-conclusions"*.
- **What** `standingOfAnAbsentRow` compares the caller's `Issued` against the ledger's horizon and
  nothing else. A key minted before the retention window opened resolves to `Expired` while its
  writing transaction is still open — and [SPEC] tells the caller that the difference between
  `Unresolved` and `Expired` is *"re-resolve, or **stop**"*. Driven (§4 PROBE B) against a
  `now() - retention` ledger with a one-minute retention and a ten-minute-old `Issued`: `Expired`
  while open, `Found` after the commit. **No clock skew was applied.** [SPEC] acknowledges the
  shape only as a skew consequence (*"A clock behind the database's answers `Expired` for an
  operation that is in flight"*), and backlog item 49 attributes it to the empty-table
  `coalesce(MIN(...), now())` spelling — but item 49's own recommended fix, `now() - retention`, is
  the spelling this was driven on, so it does not close it.
- **Why this severity** `[medium]`, not `[high]`: `Expired` is still a non-conclusion. It produces
  neither a false `Found` nor a false "it did not happen", the caller does **not** re-issue, and no
  duplicate append is reachable through it. What is wrong is the analysis: the residue is bounded
  by the deployment's retention, which is minutes, not by host-to-database skew, which is
  milliseconds — so *"why the error term is tolerable"* does not cover this case.
- **Why this timing** Backlog. A behavioural fix (making `Expired` conditional on something the
  resolver cannot see) is not available — there is no row to lock, which is ES-07's hard clause.
  What is owed is the sentence, and it belongs next to item 49's in the same pass.
- **Close criteria**
  - [ ] `Standing.Expired`'s comment states that it is reachable for an operation still in flight
        whenever `Issued` predates the horizon, independently of skew.
  - [ ] [SPEC] §"Retention" attributes the in-flight `Expired` to retention as well as to skew, and
        says what a caller that may hold very old keys does instead.
  - [ ] The module page's advice for `Expired` does not read as terminal without that caveat.
  - [ ] Backlog item 49 is amended so its recommended `now() - retention` fix is not read as closing
        this.
- **Status: open**

### GAP-3 `[low][backlog]` — three bookkeeping claims in shipped comments and in the plan name things that do not exist

- **Where** `event/eventpg/receipt_integration_test.go:32-35`; [PLAN] § *The complete set of paths
  phase 5 may add to the manifest*; [PLAN] § *S5 — executed*, the `make api` paragraph.
- **What** Three, all of the same class — a present-tense claim about something that is not there:
  1. The statement-constants comment asserts *"`_examples/event-receipts` publishes the same two,
     and `TestTheExampleLedgerIsTheOneTheLiveSuiteProved` compares them byte for byte and in order
     — so 'the reference implementation' is a fact this suite proved rather than a label a page
     applies."* Neither `_examples/event-receipts` nor that test exists; `grep -rn` finds the name
     only in this comment. S6 owes both, and until it lands the sentence is false about itself.
  2. The plan's allowed-path set omits `event/refusal_test.go` and `event/refusalmessages_test.go`,
     which did move (S1's own `event-kernel-moved` regex correctly includes them), and lists
     `event/replay_test.go`, which exists and did **not** move. S6 re-checks the phase's whole
     manifest against that list and will trip on both directions.
  3. The `make api` diff is **116** lines (`git diff --numstat` → `116 0`), not 117; the extra line
     is the `+++` header.
  Beside these, §4 PROBE E records a sixth `Held.Complete` state — a completion issued after the
  claim's transaction committed — that surfaces as a bare `sql: transaction has already been
  committed or rolled back` rather than as any `receipt` sentinel, and that §UC-248's five refusals
  do not name.
- **Why this severity** `[low]`: nothing is wrong at runtime and nothing is measured incorrectly.
  What it costs is the next reader, which is exactly what this repository's own doc rule prices —
  *"A doc that names a symbol which no longer exists has failed at the one job it has."*
- **Why this timing** Backlog, and S6 is where all three land anyway.
- **Close criteria**
  - [ ] `_examples/event-receipts` exists and `TestTheExampleLedgerIsTheOneTheLiveSuiteProved`
        compares the two statement sets byte for byte and in order — or the comment is narrowed to
        what is true.
  - [ ] The plan's allowed-path set lists `refusal_test.go` and `refusalmessages_test.go` and drops
        `replay_test.go`, or the section that moved them records why.
  - [ ] The `make api` count reads 116.
  - [ ] `Held.Complete`'s doc names the after-the-commit state, or [SPEC] §UC-248 records that it is
        the ledger's error to raise.
- **Status: open**

---

## 7. Commands run

```sh
export FROSTGROVE_EVENTPG_TEST_DSN='postgres://vv:vv@localhost:55432/vv?sslmode=disable'
export FROSTGROVE_EVENTPG_TEST_PSQL='docker exec -i vv-postgres-1 psql -U vv -d vv'

docker exec -i vv-postgres-1 psql -U vv -d vv -tAc 'select version()'   # PostgreSQL 17.9
go test -tags=integration -list '^(…the sixteen…)$' ./event/eventpg/ | grep -c '^Test'   # 16
go test -race -count=1 -tags=integration -run '^(…the sixteen…)$' ./event/eventpg/       # ok 3.871s
go test -race -count=1 -tags=integration ./event/eventpg/                                # ok 133.748s
go test -race -count=1 -tags=integration ./event/eventpg/                                # ok 125.945s
go test -count=1 -tags=integration -shuffle=on -run '^(…the sixteen…)$' ./event/eventpg/ # ok 2.667s
go test -tags=integration -run '^$' -bench '^BenchmarkStateAt$' -benchtime 20x -count 3 ./event/eventpg/
env -u FROSTGROVE_EVENTPG_TEST_DSN go test -count=1 -tags=integration -run '…' ./event/eventpg/   # FAIL, not skip
FROSTGROVE_EVENTPG_TEST_PSQL='echo 999' go test … -run '^TestClaimAppendCompleteAndTheRollbackControl$'  # FAIL — the cross-check is live
gofmt -l . | wc -l                              # 0
go vet -tags=integration ./event/eventpg/       # clean
go test -race -count=1 ./event/...              # five packages, all ok
make check                                      # eleven ok arms, exit 0
make api && git diff --numstat docs/api/surface.md   # 116 0, stable
make unit                                       # 62 ok, one FAIL: TestNoI18nPackageCostsMoreThanItsErrorSeam (foreign)
./scripts/checks.sh event-kernel                # ok
git status --porcelain event/
```

Two mutations (§2) and six probes (§4) were applied and removed; `diff` against the pre-mutation
copies is empty, `gofmt -l .` is `0`, `check-event-kernel` is `ok`, and no probe file remains.
