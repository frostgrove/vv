# tenancy/tenancydb (+ the crud.Source / crud.Beginner seam) — Dimension 5: data integrity, transactions, concurrency, resource lifecycle — AUDIT (2026-09-06)

Scope audited: `tenancy/tenancydb/database.go` (284 lines) and its two test files;
`crud/executor.go` (`Source`, `Beginner`, `Tx`, `InTx`, `bindingFor`), `crud/sqlrepo`,
`crud/adapter/crudsql`, `crud/adapter/crudpgx` — only where a borrowed lease meets them.
Dimension: `~/.claude/skills/econv/references/data-integrity.md`. Nothing was modified in the
repository; `diff` against a pristine copy confirms `tenancy/tenancydb/database.go` is byte-identical
to the tree as found.

---

## Map

**Entry points.** Exactly one: `Directory.Borrow(ctx, class) (*Lease, error)` (`database.go:119`).
`Lease.Source()` (`:71`) hands out a `crud.Source`; `Lease.Release()` (`:73`) gives it back through a
`sync.Once`. Operational verbs: `Evict(reference)` (`:237`), `Close()` (`:264`), `Cached()` (`:274`).

**Composition root.** None in this repository. `rg 'NewDirectory\(' --glob '*.go'` finds call sites
only inside `tenancy/tenancydb/*_test.go`. There is no `tenancydbfx`, no `_examples` wiring, and no
caller of `Evict` or `Close` anywhere outside the package's own tests. Everything below is therefore
a library contract, not a running deployment.

**State.** One `map[binding]*entry` behind one `sync.Mutex` (`:45-47`). `binding = {Reference, Epoch}`
(`:50-53`) — `Reference` is `struct{value string}` (`tenancy/reference.go:13`) and `Epoch` is a
`uint64`, so the key is comparable and identity-safe. `entry` (`:55-62`) carries
`source, expires, borrowers, evicted, ready chan struct{}, err`. Six `mutex.Lock()` sites: `:160`
(`reserve`), `:204` (`finish`), `:229` (`release`), `:238` (`Evict`), `:265` (`Close`), `:275`
(`Cached`). No lock nesting, no second lock, no lock-order inversion.

**Layers.** `tenancydb` depends on `crud` + `tenancy` + stdlib. Both the database mapping
(`Sources.Source`) and the identity proof (`Fence`) are consumer-supplied callbacks — this is the
package's only extension point, and it is invoked from `open` (`:186`, `:196`) *outside* the mutex.
`closeSource` (`:280`) — also consumer code, via `io.Closer` — is invoked from `release` (`:233`) and
`unlink` (`:260`) *inside* the mutex.

**Transactions.** `tenancydb` opens none and knows about none. The seam is
`crud.InTx/InNewTx` (`crud/executor.go:573,599`), which calls `BeginnerOf(source)` and pushes a
binding with `push(ctx, ds, tx, /*owned*/true, /*strict*/false)` (`:632`). Because `strict` is false,
`bindingFor` (`:435-474`) silently *ignores* a statement issued against a different data source while
that transaction is open — it neither matches nor refuses; `executorForSource` (`:409`) then falls
back to the raw source. That is the mechanism behind GAP-D3-6.

**Models/LLMs.** None.

**Tests that actually run.** `tenancy/tenancydb/database_test.go` (566 lines, 14 test functions) +
`support_test.go` (100). `go test -race -count=20 ./tenancy/tenancydb/` → `ok … 3.893s`. `go vet
./tenancy/...` clean. `gofmt -l tenancy/tenancydb/` silent. No integration test touches this package
(`test/integration/tenancy_test.go` has two functions, both shared-row).

**Documented position (binding, and it overrides a "not finished" verdict).**
`docs/roadmaps/2026-09-01-multitenancy-roadmap.md:386-399` marks M2 **"strategy delivered, profile
not advertised"**, and `:488` states the published profile is "Shared row only. The database-per-tenant
strategy is implemented and unit-proved but **not advertised**". `.agents/artifacts/plans/TENANCY_PLAN.md:374-380`
repeats it. **I therefore do not report "no two-real-database test" or "no outage rehearsal" as
defects — the project has already declared them open.** Everything below is a defect *inside* what
the project claims is delivered and unit-proved.

---

## Scorecard

| Law (data-integrity.md) | Verdict | Evidence |
|---|---|---|
| Transaction ownership / no work inside a transaction | **pass** (for `tenancydb`) | `database.go` contains no `Begin`, `Commit`, `Rollback`, no transaction of its own; consumer-supplied callbacks run outside the mutex (`:186`, `:196`) |
| One unit of work = one datasource (UC-41, UC-45) | **fail** | P12/P13: a second `Borrow` inside an open `crud.Tx` returns a *different* `crud.Source` and `crud` executes on it with no refusal; `crud/executor.go:632` pushes the tx binding with `strict=false` |
| Check-then-act | **pass** | `reserve` (`:159-177`) takes the slot under the same lock that checks the bound; the comment at `:153-158` states the reasoning and mutation M1 (`>=` → `>`) is killed |
| Read-modify-write on `borrowers` | **pass** | every mutation of `borrowers` is under `this.mutex`; increments (`:167`, `:174`) and decrements (`:134`, `:139`, `:147`, `:231` via `Lease.Release`'s `sync.Once`) balance one-for-one on every path — see "The borrower ledger" below. 200 interleavings × 20 runs under `-race`, no premature close, no double close |
| Resource lifecycle (release, close, drain) | **fail** | `closeSource` (`:280-284`) is a **no-op for every `crud.Source` this repository ships** (P1); `finish` (`:209`) deletes by map key, orphaning a successor entry whose source is then never closed (P3) |
| Bounded resources under growth (INV-23, UC-47) | **fail** | `MaxCached` bounds *map entries*. Live pools = entries + evicted-but-borrowed + orphaned + never-closed. P3: 1 pool alive after `Directory.Close()` with `MaxCached=1`. P7: one rotation of one tenant → 2 entries, 2 live pools |
| Liveness / deadlock | **fail** | P16: a panic in the consumer's `Sources`/`Fence` leaves `ready` unclosed forever — every waiter blocks to its own deadline and the slot never returns. P19: a source whose `Close` touches the directory self-deadlocks on the non-reentrant mutex. P17: 380 ms of head-of-line blocking for an unrelated tenant while one pool drained |
| Shutdown / drain semantics | **fail** | P4: `Close()` returns `nil` while an open is in flight, and a live, usable source is handed out afterwards. `Close() error` can only ever return `nil` (`:271`) |
| Ambiguous state | **at risk** | one fact ("this entry is finished with") is spread over `evicted` + `borrowers` + map membership + `ready` + `err`; `unlink` (`:256`) and `finish` (`:203`) each maintain a different subset, which is exactly how GAP-D3-2 arises |
| Selection fencing after a cache hit (INV-22) | **fail** | P9: 10 000 borrows → **1** fence call. P18: a remap with no epoch bump keeps serving the superseded database for a whole TTL. INV-22's scope is explicitly "including after cache hits" |
| Idempotence / retry safety | **pass** | `Lease.Release` is `sync.Once` (`:77`); mutation M8 is killed |
| Replication (N replicas) | **at risk** | the bound is per-process and undocumented as such; see GAP-D3-3 |
| Migrations | **n-a** | this package owns no schema |

---

## Metrics

| Metric | Threshold | Actual | Worst offenders |
|---|---|---|---|
| `crud.Source` implementations in this repo that satisfy `io.Closer` | all | **0 of 6** | `crudsql.DB` (`crudsql.go:127`), `crudsql.source` (`:116`), `crudpgx.Executor` (`crudpgx.go:27`), `crudtest.Recorder` |
| pools still open after `Directory.Close()`, `MaxCached=1` | 0 | **1** (P3) | orphaned by `database.go:209` |
| entries+pools held after one rotation of one tenant | 1 | **2 / 2** (P7) | `database.go:124` keys on epoch; nothing evicts the predecessor |
| `Fence` invocations per 10 000 borrows of one binding | 10 000 (INV-22) | **1** (P9) | `database.go:193-199` runs only inside `open` |
| entries expired 72 h past a 1-minute TTL with no traffic | all | **0** (P5) | `sweep` is called only from `reserve` (`database.go:165`) |
| sources created by a 64-tenant burst against `MaxCached=4` | ≤ 4 | **4** (P6) — this bound holds | slot reserved before open (`:174`) |
| head-of-line blocking of an unrelated tenant while one pool drains 400 ms | 0 | **380 ms** (P17) | `closeSource` under the mutex (`:233`, `:260`) |
| `Directory` methods reachable from a source's `Close()` without deadlock | all | **0** (P19) | non-reentrant `sync.Mutex` (`:45`) |
| consumer panics needed to retire the whole directory | ∞ | **`MaxCached`** (P16) | `open` (`:144`) has no `defer`/`recover`; `finish` (`:145`) is never reached |
| swallowed errors | 0 | **1** | `database.go:282` `_ = closer.Close()` |
| mutations of `database.go` that the repo's own suite fails to catch | 0 | **7 of 17** | M2, M3, M4, M5, M11, M12, M17 — see below |
| `go vet ./tenancy/...` | clean | clean | — |
| data races reported by `-race` over 20 runs of the repo suite + 20 runs of 19 new probes | 0 | **0** | — |

### Mutation evidence (repo suite, unmodified, against a mutated copy in the scratchpad)

`go test -race -count=3` against `probe/mut/tenancydb`, a byte-copy of `database.go` plus the
repository's own `database_test.go`/`support_test.go`.

| # | Mutation | Result |
|---|---|---|
| M1 | `reserve`: `len(entries) >= max` → `> max` | KILLED — `TestTheCacheIsBoundedRatherThanGrowingWithTenants`, `TestABindingIsGivenBackAfterItsBorrowerLifetime`, `TestNoMoreSourcesAreOpenAtOnceThanTheBudgetAllows` |
| M2 | `release`: `held.evicted && borrowers == 0` → `borrowers == 0` (close a *cached, live* entry's source) | **SURVIVED** |
| M3 | `sweep`: drop `&& held.borrowers == 0` (unlink an expired entry that has borrowers) | **SURVIVED** |
| M4 | `finish`: drop `|| this.closed` | **SURVIVED** |
| M5 | `Borrow`: a waiter that gives up on `ctx.Done()` never calls `release` | **SURVIVED** |
| M6 | `open`: `WithoutCancel(ctx)` → `ctx` | KILLED — `TestABorrowerThatGivesUpDoesNotFailTheOnesBesideIt` |
| M7 | `Borrow`: cache key drops `epoch` | KILLED — `TestARotatedGenerationDoesNotReachTheBindingItReplaced` |
| M8 | `Lease.Release`: drop `sync.Once` | KILLED — `TestOneLeaseReleasedFromManyGoroutinesCountsOnce` |
| M9 | `unlink`: drop `held.evicted = true` | KILLED — `TestEvictionWaitsForTheLastBorrower`, `TestOneLeaseReleasedFromManyGoroutinesCountsOnce` |
| M10 | `closeSource`: assert `io.Closer` but never call `Close` | KILLED — `TestEvictionWaitsForTheLastBorrower`, `TestOneLeaseReleasedFromManyGoroutinesCountsOnce`. **This mutation is the shipped production behaviour** (GAP-D3-1); the suite is green on the real code only because the fixture `closeable` (`database_test.go:17-28`) implements `Close() error` and no first-party adapter does |
| M11 | `scope`: drop the `class.Valid()` check | **SURVIVED** |
| M12 | `reserve`: drop `held.expires = now().Add(ttl)` on a cache hit | **SURVIVED** |
| M13 | `finish`: a failed open stays in the cache | KILLED — `TestADatabaseTheFenceDoesNotRecogniseIsRefused` |
| M14 | `unlink`: close even when borrowers remain | KILLED — `TestEvictionWaitsForTheLastBorrower`, `TestOneLeaseReleasedFromManyGoroutinesCountsOnce` |
| M15 | `release`: never decrement `borrowers` | KILLED — three tests |
| M17 | `scope`: accept a zero reference / zero epoch | **SURVIVED** |
| M18 | `open`: ignore the fence's error | KILLED — `TestADatabaseTheFenceDoesNotRecogniseIsRefused` |

**7 of 17 survive, and they cluster:** every surviving mutation is on the eviction / accounting /
close path (M2, M3, M4, M5, M12) or on `scope`'s input validation (M11, M17). The suite proves the
*capacity* bound and the *epoch* key thoroughly and proves the *lifecycle* barely.

### The borrower ledger (the question asked directly)

Increment sites: `reserve:167` (cache hit) and `reserve:174` (fresh entry) — exactly one per
successful `reserve`, both under the mutex.

Decrement sites, all through `release:231`, all under the mutex:

| Path | Reached from | Count |
|---|---|---|
| waiter, `ctx.Done()` before `ready` | `Borrow:134` | 1 |
| waiter, `held.err != nil` | `Borrow:139` | 1 |
| waiter, success → `Lease.Release()` | `Lease:77` (`sync.Once`) | 1 |
| opener, `open` returned an error | `Borrow:147`, after `finish:145` | 1 |
| opener, success → `Lease.Release()` | `Lease:77` (`sync.Once`) | 1 |

Every path out of `Borrow` decrements exactly once, and the `select` at `:131-136` is exclusive, so a
waiter cannot both bail and release a lease. **`borrowers` cannot go negative on any current path.**
Verified empirically by P11: 200 forced opener/waiter/give-up interleavings × 20 runs under `-race`,
asserting no source is closed under a live lease, that eviction does not close under a borrower, that
the last release does close, and that `Close()` is called exactly once. Zero failures.

The one asymmetry is the *panic* path (GAP-D3-4): `open` is called at `:144` with no `defer`, so a
panic in consumer code skips `finish` and both `release` calls, leaving `borrowers ≥ 1` and `ready`
open forever.

---

## Findings

### GAP-D3-1 [critical][immediate] `closeSource` is a no-op for every `crud.Source` this repository ships

- **Where:** `tenancy/tenancydb/database.go:280-284`. Affected sources:
  `crud/adapter/crudsql/crudsql.go:127` (`DB`, returned by `Open`/`Postgres`/`MySQL`/`MariaDB`/`SQLite`),
  `crud/adapter/crudsql/crudsql.go:116-124` (`source`, returned by `Source`),
  `crud/adapter/crudpgx/crudpgx.go:27,155` (`Executor`, returned by `Open`),
  `crud/crudtest/recorder.go:37` (`Recorder`).
- **Scale:** systemic — 6 of 6 `crud.Source` constructors in the repository; every eviction, every
  TTL sweep, every rotation and `Directory.Close()` itself.
- **Confidence:** CONFIRMED.
  `go test -race -run TestP1_ ./d3/` →
  ```
  crudsql.Postgres(*sql.DB)    does NOT implement io.Closer -> tenancydb.closeSource is a no-op for it
  crudsql.MySQL(*sql.DB)       does NOT implement io.Closer -> ...
  crudsql.SQLite(*sql.DB)      does NOT implement io.Closer -> ...
  crudsql.Open(*sql.DB)        does NOT implement io.Closer -> ...
  crudsql.Source(q,dialect)    does NOT implement io.Closer -> ...
  crudtest.Postgres()          does NOT implement io.Closer -> ...
  ```
  `go test -race -run TestP2_ ./d3/` (a source with `pgxpool.Pool`'s `Close()` signature — no error
  return, therefore not an `io.Closer`) →
  `evicted AND Directory.Close()d: the pool was never drained, and Close() returned nil`.
- **What / Why this severity:** `crudsql.DB` holds the `*sql.DB` in an unexported field and exposes it
  as `DB() *sql.DB`; it has no `Close` method at all. `crudpgx.Executor` holds a `Queryer` (in
  practice a `*pgxpool.Pool`) and has no `Close` either — and `*pgxpool.Pool.Close()` returns
  nothing, so it would not satisfy `io.Closer` even if it were exposed. Concretely: `MaxCached: 64`,
  `TTL: 10 * time.Minute` (the values in `docs/modules/en/tenancy.md:260-261`), 5 000 tenants, a
  `Sources` that returns `crudsql.Postgres(db)`. Every ten minutes each idle binding is swept and its
  `*sql.DB` — with its own `MaxOpenConns` worth of live sockets — is dropped on the floor with no
  reference and no close. After an hour the process holds hundreds of orphaned pools; it exhausts
  file descriptors or the server's `max_connections`. This is precisely the failure INV-23 and UC-47
  name ("the process does not exhaust file descriptors, exceed the database's connection limit"), and
  the docs assert the opposite at `docs/modules/en/tenancy.md:275-276`: "Eviction unlinks a binding
  immediately and closes its source only when the last borrower gives the lease back."
- **Why this timing:** it is the whole resource-lifecycle contract of the package. The public contract
  (`Sources` returns a bare `crud.Source`) is what makes it unfixable locally: `crud.Source` has no
  `Close`, so the directory cannot demand one. Fixing it means changing the `Sources` contract — a
  public API change that every consumer and every doc depends on. Doing it after the profile is
  advertised means a breaking change.
- **Close criteria:**
  - [ ] the directory obtains the close capability from a typed contract it can require, not an
        opportunistic `io.Closer` assertion (e.g. `Sources` returns a source *and* a
        `func(context.Context) error` teardown, or a `ClosableSource` interface)
  - [ ] a test asserts that a source built by `crudsql.Postgres(db)` is actually closed on eviction —
        i.e. the test does not supply its own `Close() error` shim
  - [ ] a source that offers no way to close is either refused at `NewDirectory`/`open` time or
        documented as the consumer's responsibility, in `docs/modules/{en,ru}/tenancy.md`
  - [ ] `closeSource`'s error reaches `Directory.Close() error`

### GAP-D3-2 [critical][immediate] `finish` deletes by map key, not by entry identity: an `Evict` during an in-flight open orphans the successor and leaks its pool

- **Where:** `tenancy/tenancydb/database.go:203-212` (`finish`, the `delete(this.entries, key)` at
  `:209`), reachable through `Evict` (`:237-245` — `unlink` does **not** check `borrowers` before
  removing the entry from the map) and `reserve` (`:166-176`).
- **Scale:** local in code (one line), systemic in effect (each occurrence permanently leaks one pool
  and one `MaxCached` slot's worth of accounting).
- **Confidence:** CONFIRMED.
  `go test -race -count=20 -run TestP3_ ./d3/` →
  ```
  the first borrow was refused with tenancy: the tenant capability is unavailable
  MaxCached=1, factory called 3 times, 1 sources still open after Directory.Close()
  ```
- **What / Why this severity:** the sequence, all with one key `{acme, epoch 1}`:
  1. borrower **A** reserves — `entryA` goes into the map, `borrowers=1` — and enters the consumer's
     `Sources.Source`, which is slow (`database.go:174`, then `:144`);
  2. an operator calls `Evict(acme)` → `unlink` deletes `entryA` from the map and sets
     `evicted=true`; `borrowers` is 1 so nothing is closed (`:256-262`);
  3. borrower **B** arrives, finds the key absent, creates `entryB`, opens **S2** successfully, and
     `finish` stores it (`:206`, `:211`) — the map now holds `entryB`;
  4. **A**'s open fails (or `this.closed` is set). `finish` runs `held.evicted = true` on *`entryA`*
     and then `delete(this.entries, key)` — which removes **`entryB`** from the map (`:207-210`).
  `entryB` is now unreachable from the directory *and* still has `evicted == false`. When B releases,
  `release` (`:228-235`) decrements to 0, sees `evicted == false`, and closes nothing. S2 — a live
  connection pool for a live tenant — is leaked with no reference anywhere. `Directory.Close()` cannot
  reach it either: it iterates the map (`:268`). The probe shows `MaxCached=1` ending with **one pool
  still open after `Close()`**, and a third borrow succeeding meanwhile — so the same tenant now has
  two live pools against a declared budget of one. Repeat the cycle and the leak is unbounded. This is
  a classic ABA on the map key: two different `*entry` values share one key across the window in which
  the mutex is dropped for `open`.
- **Why this timing:** it is a correctness defect in the eviction path, which is the exact path
  UC-48/INV-23 single out ("Eviction under load is exactly when a use-after-free style cross-tenant
  handoff would occur, and exactly when nobody is watching"). Any later work on eviction policy,
  LRU-under-pressure or a drain-on-shutdown builds on this function.
- **Close criteria:**
  - [ ] `finish` removes the entry only if `this.entries[key] == held` (identity, not key)
  - [ ] a test drives `Evict` during an in-flight open, then a second successful borrow, then a failed
        `finish`, and asserts the second source is closed when its last borrower releases
  - [ ] `unlink` and `finish` agree on one place that owns the `evicted` flag

### GAP-D3-3 [high][immediate] `MaxCached` bounds map entries, not open pools — and not connections, and not per deployment

- **Where:** `tenancy/tenancydb/database.go:171-173` (the check), `:274-278` (`Cached`, the only
  observability), `docs/modules/en/tenancy.md:258-279`, `.agents/artifacts/usecases/TENANCY_USECASES.md`
  INV-23 / UC-47.
- **Scale:** systemic — it is the package's headline guarantee.
- **Confidence:** CONFIRMED for the counting; the connection-multiplication arithmetic is arithmetic,
  not a measurement.
  - `TestP6` — the burst question, answered: `cohort=64 budget=4 -> 4 admitted, 60 refused
    ErrCapacity, 4 sources created, Cached()=4`. Because the slot is taken *before* the open
    (`:174`, and the comment at `:153-158` says so deliberately), in-flight entries occupy slots and
    the bound on *entries* is exactly `MaxCached`, **not** `MaxCached + in-flight`. That half is
    sound and mutation M1 defends it.
  - `TestP7` — `one tenant, one rotation: Cached()=2, sources created=2, still open=2`.
  - `TestP3` — `Cached()==0` while one pool is alive and unreachable.
- **What / Why this severity:** three distinct gaps between "the number `MaxCached` bounds" and "the
  number that takes a deployment down":
  1. **entries ≠ live sources.** An entry unlinked while borrowed (`Evict`, `Close`, and — with M3
     applied — `sweep`) frees its slot immediately while its pool stays open until the borrower
     returns. `Cached()` reports the freed number. Add GAP-D3-2's orphans and GAP-D3-1's never-closed
     pools and the true count is unbounded above while `Cached()` stays ≤ `MaxCached`. There is no
     counter of live sources at all.
  2. **a source is a pool, not a connection.** `MaxCached: 64` with a `*sql.DB` at
     `MaxOpenConns: 25` is a 1 600-connection ceiling. Nothing in `database.go` or
     `docs/modules/en/tenancy.md:278-284` says so; the doc's justification is "an unbounded
     per-tenant pool is how one tenant takes a deployment down", which reads as if the bound were on
     connections.
  3. **the bound is per process.** `data-integrity.md` line 1: "the application runs in N replicas at
     the same time." With 8 replicas the real ceiling is `8 × MaxCached × poolSize`. The documentation
     never states that `MaxCached` must be divided by the replica count.
- **Why this timing:** it is the sizing contract an operator uses to pick a number. Publishing the
  profile with this ambiguity means every deployment picks the wrong one.
- **Close criteria:**
  - [ ] the directory tracks and exposes *live sources* (opened − closed), not just map entries, and a
        test asserts it after an evict-under-borrower cycle
  - [ ] `docs/modules/{en,ru}/tenancy.md` states that the bound is per process and per source, and
        gives the `replicas × MaxCached × poolSize` arithmetic
  - [ ] UC-47's acceptance ("observed peak connection count ≤ the declared bound") is measured against
        a source that reports connections, not against `openings.peak`

### GAP-D3-4 [high][immediate] A panic in the consumer-supplied `Sources` or `Fence` poisons the slot permanently

- **Where:** `tenancy/tenancydb/database.go:144-149` — `this.open(ctx, scope)` is called with no
  `defer`, so `finish` (`:145`) and `release` (`:147`) are both skipped on a panic. `open` itself
  (`:183-201`) calls two pieces of consumer code, `:186` and `:196`.
- **Scale:** local, but the blast radius is the whole directory.
- **Confidence:** CONFIRMED. `go test -race -run TestP16_ ./d3/` →
  ```
  the same tenant, 10ms later: err = context deadline exceeded after waiting 200ms on a `ready` nobody will ever close
  the poisoned entry holds its slot past its TTL forever (borrowers never returns to 0); factory calls = 1
  ```
- **What / Why this severity:** a `Sources` implementation is application code that reads a secret
  store and builds a DSN — `panic: runtime error: invalid memory address` on a nil credential is an
  ordinary Tuesday, and a `Fence` runs a query. When it panics: `held.ready` is never closed, so every
  concurrent and every *subsequent* waiter for that binding blocks in `Borrow:132` until its own
  context deadline (200 ms measured, and with no deadline: forever). `held.borrowers` never returns
  to 0, so `sweep` (`:250`) will never expire the entry no matter how much time passes — the probe
  waits 10× the TTL and the slot is still held. The entry is immortal, the tenant is permanently
  unservable, and the slot is permanently consumed. `MaxCached` such panics and the directory answers
  `ErrCapacity` to every tenant forever. The panic itself propagates into the borrower's goroutine —
  in an HTTP handler that is one 500; the poisoning is the part nobody notices.
  Against `microkernel.md`'s four requirements for an extension point, `Sources`/`Fence` have a
  contract and a registration but **no failure policy**: `open` handles an `error` and does not
  consider a panic.
- **Why this timing:** the fix is a `defer` in `Borrow`/`open` that guarantees `finish` runs, which
  changes the shape of `Borrow`'s error handling. Doing it later means rewriting the same function
  that GAP-D3-2 also touches.
- **Close criteria:**
  - [ ] `finish` is guaranteed to run (deferred) for every path out of `open`, panic included
  - [ ] a panicking `Sources` and a panicking `Fence` each get a test: the entry is evicted, the slot
        is returned, waiters get a typed refusal rather than their own deadline
  - [ ] the failure policy for consumer callbacks is written down in `docs/modules/{en,ru}/tenancy.md`

### GAP-D3-5 [high][immediate] `closeSource` runs under the directory's single global mutex

- **Where:** `tenancy/tenancydb/database.go:228-235` (`release`) and `:256-262` (`unlink`, reached
  from `sweep:251`, `Evict:242`, `Close:269`) — all with `this.mutex` held (`:229`, `:238`, `:265`,
  and `sweep` runs inside `reserve`'s critical section at `:165`).
- **Scale:** systemic — 2 call sites covering every close path in the package.
- **Confidence:** CONFIRMED.
  - `go test -race -run TestP17_ ./d3/` →
    `an unrelated tenant's Borrow waited 380ms while one pool drained for 400ms`
  - `go test -race -run TestP19_ ./d3/` →
    `Release did not return in 1s: closeSource ran under Directory.mutex and Close re-entered it`
- **What / Why this severity:** `*sql.DB.Close()` blocks until every connection is returned or torn
  down; a pool against a database that has just become unreachable can block for the driver's full
  timeout. During that time **every** tenant's `Borrow`, `Evict`, `Close` and `Cached` is queued
  behind one `sync.Mutex`. That is UC-49 inverted: "A's failures do not consume B's share of the
  connection budget or block B's requests" — here A's *teardown* blocks B outright, measured at
  380 ms for a 400 ms drain. The second probe is worse and deterministic: because `sync.Mutex` is not
  reentrant, a source whose `Close()` calls back into the directory (a wrapper that evicts siblings, a
  metrics hook that reads `Cached()`) deadlocks the directory permanently, and nothing in the
  documentation forbids it — the consumer's source is application code and the reentrancy hazard is
  invisible from the `Sources` signature.
- **Why this timing:** it is structural: the close must move out of the critical section (collect the
  sources to close under the lock, close them after unlocking), which changes `release`, `unlink`,
  `sweep`, `Evict` and `Close` together.
- **Close criteria:**
  - [ ] no consumer-supplied code is invoked while `this.mutex` is held
  - [ ] a test asserts an unrelated tenant's `Borrow` completes in ≪ the drain time of a slow pool
  - [ ] a source whose `Close` re-enters the directory does not deadlock, or the prohibition is
        documented in `docs/modules/{en,ru}/tenancy.md`

### GAP-D3-6 [high][immediate] Nothing binds a lease to a unit of work: a second `Borrow` inside an open transaction returns a different source, and `crud` executes on it silently

- **Where:** `tenancy/tenancydb/database.go:119-151` (`Borrow` re-resolves on every call and returns
  a fresh `*Lease` with no notion of an enclosing unit of work); `crud/executor.go:632`
  (`push(ctx, ds, tx, true, /*strict*/ false)`); `crud/executor.go:435-474` (`bindingFor` — a
  non-strict binding for a *different* data source is skipped, not refused); `crud/executor.go:409-415`
  (`executorForSource` falls back to the raw source). Contract at stake:
  `.agents/artifacts/usecases/TENANCY_USECASES.md` UC-41 and UC-45.
- **Scale:** systemic — it is the shape of the whole seam, not a line.
- **Confidence:** CONFIRMED.
  - `go test -race -run TestP12_ ./d3/` →
    ```
    inside one open transaction: outer source ran [insert into invoices values (1) []]
                                 a SECOND source ran [insert into invoices values (2) []]
    nothing refused: no ErrPinned, no crud.ExecutorScopeMismatch, and row 2 is outside the transaction that committed row 1
    ```
  - `go test -race -run TestP13_ ./d3/` →
    ```
    ErrPinned stopped Authority.With from swapping the scope on the *same* context, and nothing else was stopped
    tenant A's source ran [update balances set amount = 0 []]
    tenant B's source ran [insert into audit values ('acme') []] inside A's transaction, on a fresh context
    A's transaction rolled back; B's row stands, because it was never in a transaction at all
    ```
  - `go test -race -run TestP14_ ./d3/` →
    ```
    the transaction ran [insert into invoices values (1) [] insert into invoices values (2) []] to completion on a closed pool, and committed
    ```
- **What / Why this severity:** three separate failures on one seam.
  1. **`ErrPinned`'s reach ends at `Authority.With`.** `tenancy/context.go:23-32` refuses a *second
     bind of a different tenant onto the same context* — P13 confirms it fires. It says nothing about
     `Directory.Borrow`, which derives the scope from whatever context it is handed. A helper that
     builds a fresh context (`carrying(t, context.Background(), …)` in the probe — in production, a
     cohort job's inner loop, exactly UC-45's stated trigger, or `Authority.Each`) borrows tenant B's
     source while tenant A's transaction is open, writes to it, and A's rollback leaves B's row
     standing. UC-45 says "the operation is refused… B's database is untouched"; it is not refused.
  2. **`crud` does not catch it either.** `inNewTx` pushes the transaction binding with `strict=false`
     (`crud/executor.go:632`), so `bindingFor` walks past a binding for a different data source
     instead of returning `ExecutorScopeMismatch` (`:466-468` is only reached when `b.strict`). The
     second source executes directly. Even same-tenant, P12 shows a second `Borrow` after an `Evict`
     returning a *different* source, so half the unit of work commits inside the transaction and half
     outside it, with no error anywhere. UC-41 ("all of them use the source selected at the outermost
     boundary; none re-resolves") is not enforced.
  3. **The lease has no link to the transaction.** P14: releasing the lease mid-transaction plus an
     `Evict` closes the pool under the open transaction, and the transaction runs to completion and
     commits against a closed pool with the test double — against a real `*sql.DB` it would fail
     mid-way, at the caller, in the shape the comment at `database.go:113-118` was written to avoid.
  The mitigating fact, and it is real: `docs/modules/en/tenancy.md:286-289` says the directory does
  **not** multiplex a repository across databases and that "binding is yours". So the *binding* is
  the consumer's job. But `.agents/artifacts/plans/TENANCY_PLAN.md:358-359` planned exactly the
  mechanism that would have made pinning the framework's job —
  `func (*Directory) Bind(ctx) (context.Context, error) // one crud.Session, once` — and it is not in
  the shipped API (`docs/api/surface.md`: only `NewDirectory`, `DirectorySpec`, `Directory`, `Lease`,
  `Sources`, `SourcesFunc`). UC-41/UC-45 are counted as covered by S4 in the plan, and for this
  topology nothing implements them.
- **Why this timing:** it is a missing public contract. Adding the pinning later changes `Borrow`'s
  signature or adds a sibling verb, and every consumer written against the current shape has to move.
- **Close criteria:**
  - [ ] either the directory pins one source per unit of work (the planned `Bind`/`crud.Session`) and
        a second selection inside it refuses with a typed error, or `docs/modules/{en,ru}/tenancy.md`
        and UC-41/UC-45 are amended to say the framework does not enforce this under database-per-tenant
        and the consumer must
  - [ ] a test: an open transaction on A, a borrow for B inside it, the write to B refuses
  - [ ] a test: a second borrow for the *same* tenant inside an open transaction returns the same
        source or refuses
  - [ ] the plan's coverage claim for UC-41/UC-45 under S4 is corrected

### GAP-D3-7 [high][immediate] `Close()` is not a barrier: it returns `nil` while opens are in flight and hands out a live source afterwards

- **Where:** `tenancy/tenancydb/database.go:264-272`; the interaction with `finish`'s
  `|| this.closed` at `:207`.
- **Scale:** local.
- **Confidence:** CONFIRMED. `go test -race -run TestP4_ ./d3/` →
  ```
  Directory.Close() returned nil while an open was still in flight
  a live, usable crud.Source was handed out after Close() returned
  and it executed a statement: [select 1 []]
  it is closed only when the borrower releases; a borrower that never releases keeps it forever
  ```
  and `go test -race -run TestP15_ ./d3/` → `the pool failed to close and Directory.Close() returned nil`.
- **What / Why this severity:** answering the probe question directly: **a source opened after
  `Close()` *is* eventually closed — but only if its borrower releases.** `finish` (`:207`) does check
  `this.closed` and marks the entry evicted, so `release` will close it. Three things still go wrong:
  1. `Close()` returns before the in-flight open completes, so a shutdown sequence that does
     `directory.Close()` and then tears down the driver races the open. The borrower gets a working
     lease *after* `Close()` returned nil and successfully executes a statement on it (probe output
     above).
  2. If the borrower never releases — it panicked, it is blocked, or it simply leaked the lease —
     the pool stays open forever with `Close()` having reported success.
  3. `Close() error` returns a literal `nil` at `:271` and `closeSource` discards
     `closer.Close()`'s error at `:282`. Forty pools can fail to drain and the shutdown path reports
     success. `restrictions.md` §2: "swallowing an error to keep a pipeline green".
- **Why this timing:** `Close` is a public contract that a composition root's shutdown hook will be
  written against; changing it from "returns immediately, always nil" to "drains, returns errors" is
  a behaviour change for every consumer.
- **Close criteria:**
  - [ ] `Close` either waits for in-flight opens and outstanding borrowers (with a context/deadline)
        or documents that it does not and returns the number still outstanding
  - [ ] `Close` returns a joined error of every failed source close
  - [ ] a test: `Close` during an in-flight open; a test: a source whose `Close` fails is reported

### GAP-D3-8 [medium][immediate] `TTL` is not a lifetime: `sweep` runs only inside `reserve`

- **Where:** `tenancy/tenancydb/database.go:247-254` (`sweep`), called from exactly one place,
  `reserve:165`. `DirectorySpec.TTL` is documented as mandatory at `:90-92` and
  `docs/modules/en/tenancy.md:261,278-280`; the plan calls it "borrower lifetime"
  (`.agents/artifacts/plans/TENANCY_PLAN.md:353`).
- **Scale:** local.
- **Confidence:** CONFIRMED. `go test -race -run TestP5_ ./d3/` →
  ```
  72 hours past a 1-minute TTL: the binding and its pool are still held, because nobody borrowed
  one borrow for an unrelated tenant expired it; TTL is 'age at next reserve', not a lifetime
  ```
- **What / Why this severity:** the meaning of `TTL` is "age checked the next time somebody calls
  `Borrow` on this directory, for any tenant". A process that goes quiet — night-time traffic, a
  worker that drains its queue, a replica taken out of rotation but not stopped — holds every pool and
  every credential it ever opened, indefinitely. That is not what "a binding nobody ever revisits
  survives its own rotation" (the error text at `:91`) promises, and combined with GAP-D3-10 it means
  a rotated credential is retained past its intended window with no upper time bound. There is no
  reaper goroutine and no `Sweep()` an operator could call. The one mitigating detail is real and
  worth recording: `sweep` runs *before* the capacity check (`:165` then `:171`), so a directory full
  of expired-and-idle entries does not answer `ErrCapacity` — it reclaims first.
- **Why this timing:** the fix is either a documented redefinition of `TTL` or a background reaper —
  both are contract-level.
- **Close criteria:**
  - [ ] either `TTL` is enforced independently of traffic, or `docs/modules/{en,ru}/tenancy.md` states
        that expiry is evaluated only on `Borrow` and that an idle directory retains everything
  - [ ] a test pins whichever meaning is chosen

### GAP-D3-9 [medium][immediate] Rotation doubles the footprint and can refuse a tenant its own database; nothing calls `Evict`

- **Where:** `tenancy/tenancydb/database.go:124` (the key includes the epoch — correct, and mutation
  M7 defends it), `:237-245` (`Evict`). `rg '\.Evict\(' --glob '*.go' .` finds no caller outside
  `tenancy/tenancydb/database_test.go`. `NewDirectory` is likewise never called outside the package's
  own tests — there is no `tenancydbfx`, no example, no lifecycle hook.
- **Scale:** systemic in effect — every rotation of every tenant.
- **Confidence:** CONFIRMED.
  - `go test -race -run TestP7_ ./d3/` → `one tenant, one rotation: Cached()=2, sources created=2, still open=2`
  - `go test -race -run TestP8_ ./d3/` → `a full directory refuses the rotated generation of a tenant it is already caching, for a whole TTL` (`MaxCached: 1`, `errors.Is(err, tenancy.ErrCapacity)`)
- **What / Why this severity:** keying on `{reference, epoch}` is right — the superseded binding must
  never be handed to the new generation — but nothing retires the predecessor. It keeps its slot, its
  pool and (the point of a rotation) its *revoked credential* until the TTL elapses, which per
  GAP-D3-8 may be never. Sizing consequence: a fleet-wide credential rotation instantaneously doubles
  the directory's occupancy, and a directory sized without that headroom answers `ErrCapacity` to
  tenants for whose databases it is already holding an open pool — P8 reproduces exactly that. The
  remedy exists (`Evict`) and has no automatic trigger: `finish` knows the epoch it just opened for
  and could retire the older epochs of the same reference; it does not. The question "who is supposed
  to call `Evict`?" has no answer in the code, in `docs/modules/en/tenancy.md` (which never mentions
  the method — `rg 'Evict' docs/modules/en/tenancy.md` returns only the prose at :275), or in
  `docs/ai/flows/FL-033-*.md:117-130`.
- **Why this timing:** it is a sizing rule consumers need before they pick `MaxCached`, and an
  operator procedure that has no home.
- **Close criteria:**
  - [ ] a successful open for `{ref, N}` retires cached `{ref, <N}` entries, or the obligation to call
        `Evict` on rotation is documented with the sizing rule (`MaxCached ≥ 2 × peak tenants` during a
        rotation window)
  - [ ] a test: `MaxCached=1`, rotate, borrow → the new generation is served
  - [ ] `Evict` and `Close` appear in `docs/modules/{en,ru}/tenancy.md`

### GAP-D3-10 [medium][immediate] The `Fence` runs on open only; INV-22 requires it after cache hits

- **Where:** `tenancy/tenancydb/database.go:193-199`, inside `open`, which runs once per entry.
  INV-22 (`.agents/artifacts/usecases/TENANCY_USECASES.md:1680-1690`): "A resolved datasource is used
  only after being confirmed to be the one the scope names… **Scope:** Database-per-tenant selection,
  **including after cache hits**, rotation and failover."
- **Scale:** systemic — every borrow after the first.
- **Confidence:** CONFIRMED.
  - `go test -race -run TestP9_ ./d3/` → `10000 borrows -> 1 fence calls, 1 factory calls`
  - `go test -race -run TestP18_ ./d3/` →
    `after the remap the statement went to "db-old"; fence calls = 1` /
    `the superseded database keeps serving for a whole TTL, and the fence -- the one check that could catch it -- never runs again`
- **What / Why this severity:** what guarantees the source is still that tenant's on the 10 000th
  borrow is **the cache key alone** — `{reference, epoch}` — plus the fact that `Borrow` re-derives
  the scope from the context on every call (`:120`, `tenancy/context.go:39-58`, which does re-check
  admission per class and is a genuine strength: `TestADatabaseIsNotHandedToAScopeTheClassNoLongerAdmits`
  proves it). The fence, which UC-43 and INV-22 make the *second line of defence* against "a resolver
  bug or a corrupted cache entry", is not part of that. P18 shows the concrete hole: the control plane
  moves a tenant to a new database without bumping the epoch — a failover, an endpoint change, a
  restore into a different host — and the directory keeps routing statements to the superseded
  database for a whole TTL, while the fence, which would have refused, never runs again. UC-46 says
  the epoch check is the intended mechanism for this, so the *design* is defensible; what is missing
  is (a) any statement anywhere that a remap **must** be accompanied by an epoch bump, and (b) INV-22's
  explicit "including after cache hits", which the code does not satisfy.
- **Why this timing:** it decides whether the fence is a defence-in-depth mechanism or a
  first-connection sanity check. That is a security contract statement, and it changes what an
  operator must do on failover.
- **Close criteria:**
  - [ ] either the fence re-runs on cache hits (with a declared budget — it costs a round trip), or
        INV-22 is amended to say the fence is an open-time check and the epoch is the cache-hit
        mechanism
  - [ ] "a remapping that does not advance the epoch is unsupported" is stated in
        `docs/modules/{en,ru}/tenancy.md` and in UC-46
  - [ ] a test pins whichever is chosen

### GAP-D3-11 [medium][deferred] `open` inherits whichever borrower won the reservation, and every waiter gets the result

- **Where:** `tenancy/tenancydb/database.go:183-201`; the comment at `:179-182` states the intent.
- **Scale:** local.
- **Confidence:** CONFIRMED. `go test -race -run TestP10_ ./d3/` →
  ```
  the factory saw marker=request-A (the opener's), while a waiter from request-B got the result
  open context: cancellation dropped, deadline in 7s (OpenTimeout), values kept
  every ctx value of request-A -- scope, logger, span, auth principal -- is what the factory and the fence see
  ```
- **What / Why this severity:** the mechanics verify exactly as designed and I could not construct a
  leak: `context.WithoutCancel(ctx)` drops cancellation (the probe cancels the opener's context before
  releasing the factory and `ctx.Err()` observed *inside* the factory is `nil`), `WithTimeout` supplies
  a `7s` deadline matching `OpenTimeout`, `defer cancel()` at `:185` releases the timer, and the scope
  value survives (`tenancy.From(ctx)` succeeds). Nothing cancellable leaks. Mutation M6 (using `ctx`
  instead of `WithoutCancel(ctx)`) is killed by the suite. What the probe does establish is the
  consequence nobody has written down: the `Sources` factory and the `Fence` see **request A's**
  context values while serving requests B, C and D — A's `port.Logger`, A's otel span (which will be
  ended while the child open is still running), A's authenticated principal. A `Sources` that reads
  anything request-scoped out of the context other than the scope it is passed explicitly is
  cross-request. No transaction handle can travel this way — `crud` transactions live in
  `ctx.Value(ctxKey{})` too, but a `Sources` has no reason to look and `crud` never reads it here.
- **Why this timing:** documentation and a contract note; no behaviour has to change.
- **Close criteria:**
  - [ ] `docs/modules/{en,ru}/tenancy.md` states that `Sources`/`Fence` receive an arbitrary
        borrower's context values with cancellation removed and `OpenTimeout` as the deadline, and
        must derive nothing request-specific from it beyond the scope argument

### GAP-D3-12 [low][deferred] `NewDirectory` does not check `TTL` against `OpenTimeout`

- **Where:** `tenancy/tenancydb/database.go:87-100` (validation), `:174` (`expires` is set at
  *reserve* time), `:203-212` (`finish` never refreshes `expires`).
- **Scale:** local.
- **Confidence:** CONFIRMED by reading; not separately probed. `expires` is computed before the open
  begins and never updated when it ends.
- **What / Why this severity:** with `TTL: 10 * time.Second` and the default `OpenTimeout: 30s`
  (`:14`), an entry whose open took 25 s is already expired when it becomes usable and is swept on the
  next `reserve` once its borrowers leave — the cache silently degrades to "open a pool per request"
  under exactly the conditions (a slow control plane) where that is most expensive. `NewDirectory`
  accepts the combination without comment.
- **Close criteria:**
  - [ ] `NewDirectory` refuses `TTL <= OpenTimeout`, or `finish` sets `expires = now + TTL`
  - [ ] a test covers a TTL shorter than the open

### GAP-D3-13 [high][immediate] The lifecycle half of the suite does not fail when the lifecycle is broken

- **Where:** `tenancy/tenancydb/database_test.go` — in particular the `closeable` fixture (`:17-34`),
  `TestEvictionWaitsForTheLastBorrower` (`:225-248`), `TestNoMoreSourcesAreOpenAtOnceThanTheBudgetAllows`
  (`:405-431`) and the `openings.live/peak` counters (`:52-73`).
- **Scale:** systemic — 7 of 17 mutations survive, and they are all on one path.
- **Confidence:** CONFIRMED — the full mutation table is in the Metrics section, produced by
  `go test -race -count=3` against a byte-copy of `database.go` carrying the repository's own tests.
- **What / Why this severity:** three specific ways the suite reports a guarantee it does not hold.
  1. **The close fixture is the only reason closing is observable.** `closeable` (`:17-28`) implements
     `Close() error`; no `crud.Source` in the repository does (GAP-D3-1). `restrictions.md` §5:
     "Sample/golden data lives in `tests/`" — here the *behaviour under test* exists only in the test.
     M10 ("assert `io.Closer`, never call `Close`") is killed by two tests while being the production
     behaviour.
  2. **`TestNoMoreSourcesAreOpenAtOnceThanTheBudgetAllows` does not measure what its failure message
     claims.** `openings.live` is incremented at the top of `Source` (`:52`) and decremented just
     before it returns (`:73`), so `peak` is the maximum number of *concurrent factory invocations*,
     not the number of sources alive. Its failure message — "the bound counts map entries, not
     connections" — names precisely the property it cannot see. The bound on entries is nonetheless
     real and independently confirmed by P6; the test is not wrong, it is weaker than it reads.
  3. **The surviving mutations are the audit.** M2 (a `release` that closes a *cached, non-evicted*
     source — i.e. a use-after-close for the next borrower), M3 (a `sweep` that unlinks an entry with
     live borrowers), M4 (a `finish` that ignores `Close` having happened), M5 (a waiter that gives up
     and never returns its borrow — a permanent leak of a borrow count, which pins the entry forever
     exactly as GAP-D3-4 does), M12 (no lifetime refresh on a cache hit), M11/M17 (`scope` accepting an
     invalid class or a zero reference/epoch). Each is a defect a reader would call obvious, and the
     suite is green on all seven.
  This is `restrictions.md` §5's "a test that cannot fail is a lie" measured, and it is why the other
  twelve findings could sit in a green tree.
- **Why this timing:** the fixes for GAP-D3-1..7 all land on the paths these mutations cover. Without
  tests that kill them, the fixes are unverifiable.
- **Close criteria:**
  - [ ] M2, M3, M4, M5, M12, M11, M17 each have a test that fails when the mutation is applied
  - [ ] a test asserts the number of *live, unclosed* sources across an evict/rotate/close cycle,
        rather than concurrent factory calls
  - [ ] at least one test uses a source that is closed the way a production source would be

---

## Answers to the four questions asked

**Can a statement land in the wrong tenant's database?** Not through the directory's own selection.
The key is `{reference, epoch}` (`:124`, mutation M7 killed), the scope is re-derived and re-admitted
per `Borrow` (`:120` → `tenancy/context.go:39`), and the factory is always given the borrowing
tenant's scope (`TestTheFactoryAndTheFenceAreToldWhichTenantIsAsking`, and P18 confirms the same
scope reaches the fence). It can happen two other ways, both confirmed: a control-plane **remap
without an epoch bump** keeps serving the superseded database for a whole TTL with the fence never
re-running (GAP-D3-10, P18); and a **second tenant's source used inside another tenant's open
transaction** is refused by nothing — not `ErrPinned`, not `crud` — so a rollback in tenant A leaves
tenant B's write standing (GAP-D3-6, P13). The second is UC-45's own scenario.

**Can a source be closed under a live borrower?** No. `unlink` (`:259`) and `release` (`:232`) both
require `borrowers == 0`, the ledger balances on every path, and mutations M9, M14 and M15 are all
killed. P11 confirms it over 200 interleavings × 20 runs under `-race`. The inverse is the problem:
sources that should be closed are not (GAP-D3-1, GAP-D3-2), and a borrower can keep using a source
the directory has already forgotten (P4, P14).

**Can the directory leak or deadlock?** Both, confirmed. Leaks: every eviction with a real adapter
(GAP-D3-1), an `Evict`-during-open ABA (GAP-D3-2), an idle process (GAP-D3-8), every rotation
(GAP-D3-9). Deadlocks and stalls: a source whose `Close` re-enters the directory hangs it permanently
(P19); a slow drain blocks every other tenant for the duration (P17, 380 ms measured); a consumer
panic makes one binding permanently unservable and its slot permanently held (P16).

**Is `MaxCached` a real bound?** On map entries, yes and exactly: a 64-tenant burst against
`MaxCached: 4` produced 4 sources, 60 `ErrCapacity`, `Cached() == 4` — the slot is reserved before the
open, so it is `MaxCached`, **not** `MaxCached + in-flight` (P6). On open pools, no: evicted-but-
borrowed entries free their slot while their pool lives, orphaned entries are invisible, and with
every first-party adapter nothing is ever closed at all. And a "source" is a pool, and the bound is
per process — the deployment-level ceiling is `replicas × MaxCached × poolSize`, which is nowhere
written down (GAP-D3-3).

**Is `ErrCapacity`-without-queueing the documented behaviour, and is it survivable?** Documented, yes:
`docs/ai/flows/FL-033-*.md:148` and `docs/modules/en/tenancy.md:320-324` both name it, and UC-47's
acceptance permits "refused **or** queued within a declared bound". `errors.go:29` deliberately does
not wrap `crud.ErrForbidden` so it cannot be mistaken for an authorisation refusal — that is a good
decision and it is written down at `docs/modules/en/tenancy.md:322-324`. Survivable with reservations:
`sweep` runs before the capacity check (`:165` before `:171`), so idle-and-*expired* entries are
reclaimed first and only idle-but-*unexpired* ones cause a refusal. `TestABindingIsGivenBackAfterItsBorrowerLifetime`
(`:275-300`) pins exactly that: with `MaxCached: 1`, tenant 2 is refused until tenant 1's TTL elapses,
even though tenant 1 has released. There is no LRU-under-pressure and no queue, so `MaxCached` must
be sized for peak *distinct tenants per TTL window*, not peak concurrency — and doubled for rotation
(GAP-D3-9). None of that arithmetic is in the documentation.

---

## Remediation order

1. **GAP-D3-1 — the close contract (L, blast radius: `Sources` public API, both adapter modules, both
   language docs, `docs/api/surface.md`).** Everything else about resource lifecycle is unverifiable
   while nothing is ever closed, and every test written for GAP-D3-2/5/7 would pass vacuously with a
   fixture that self-closes. It is also the only finding that changes a published signature, so it
   must land before the profile is advertised.
2. **GAP-D3-13 — tests that kill M2..M5, M11, M12, M17 (M).** Do this second, not last: the fixes for
   3–6 below all land on `finish`/`release`/`unlink`/`sweep`, and without failing tests they are
   unreviewable. Blast radius: `tenancy/tenancydb/database_test.go` only.
3. **GAP-D3-2 — identity-checked delete in `finish` (S, blast radius: one function).** Depends on 1
   only in the sense that its test needs a source that really closes.
4. **GAP-D3-4 — guarantee `finish` runs on panic (S, blast radius: `Borrow`/`open`).** Same function
   family as 3; do them in one pass.
5. **GAP-D3-5 — move `closeSource` out of the critical section (M, blast radius: `release`, `unlink`,
   `sweep`, `Evict`, `Close`).** Must come after 3 and 4, because it changes the same functions and a
   collect-then-close rewrite would otherwise conflict.
6. **GAP-D3-7 — `Close` drains and reports (M, blast radius: the `Close() error` contract and any
   consumer shutdown hook).** Depends on 5: draining outside the lock is what makes a bounded drain
   possible at all.
7. **GAP-D3-6 — pin the source to the unit of work, or write down that the framework does not (M–L,
   blast radius: `Borrow`'s contract, `crud/executor.go`'s strictness for tenant-selected sources,
   UC-41/UC-45, the plan's S4 coverage claim).** Independent of 1–6 and the largest design question;
   it can proceed in parallel but its *decision* should be taken before anything else is documented,
   because it determines what the seam promises.
8. **GAP-D3-3, GAP-D3-8, GAP-D3-9, GAP-D3-10 — the contract statements (M in total, blast radius:
   `docs/modules/{en,ru}/tenancy.md`, UC-46, INV-22, INV-23, `FL-033`).** These are one editorial pass
   plus, for 9, a small code change (retire older epochs on a successful open). Do them together and
   last, because 1–7 change what there is to describe.
9. **GAP-D3-11, GAP-D3-12 (S each).** Local; no dependants.

---

## What I did not check

- **`tenancy/tenancyrow`, `tenancy/tenancyjobs`, `tenancy/tenancystorage`, `tenancy/tenancycache`,
  `crud/decorators/security`.** Out of scope; the shared-row topology is the priority audit and is
  covered elsewhere.
- **Anything against a real database.** No PostgreSQL, no MySQL, no Docker was started. Every claim
  above is against `crudtest.Recorder`-backed doubles. UC-40, UC-43, UC-44, UC-49 and UC-50 all
  require two real databases and remain unproven — as the roadmap already states.
- **`crud/adapter/crudpgx` was read, not compiled or run.** It is a separate module; I did not
  resolve its pgx dependency. The claim that `crudpgx.Executor` has no `Close` method is from reading
  `crudpgx.go:27-29` and `:155-168` (the `var _ crud.Source = Executor{}` block lists every interface
  it is asserted against; `io.Closer` is not among them), not from an executed assertion. The same
  claim for `crudsql` **is** executed (P1). Marked accordingly.
- **`isNilValue`/`identityOf`/`SameDataSource` reflection paths in `crud/executor.go`** beyond what
  GAP-D3-6 needed. I did not audit whether a `crud.Source` with a non-comparable identity behaves
  correctly under `tenancydb` (`NewSession` refuses it at `crud/executor.go:293-296`, but `tenancydb`
  never builds a `Session`).
- **`closeSource(nil)`.** `open` returns `nil, ErrUnmapped` for a nil source (`:190-192`) and
  `closeSource` on a nil interface takes the `ok == false` branch safely — I read this but did not
  probe a `crud.Source` holding a *typed* nil pointer whose `Close()` would then panic on a nil
  receiver. That path is PLAUSIBLE and untested.
- **`OpenTimeout` exhaustion.** I did not test what a borrower sees when `open` hits the 30 s
  deadline: `tenancy.Classify` (`tenancy/errors.go:52-62`) will collapse the driver's
  `context.DeadlineExceeded` to `ErrUnavailable`, so `errors.Is(err, context.DeadlineExceeded)` fails
  for the opener while it succeeds for a waiter that hit its own deadline. Whether that asymmetry
  matters to a caller is an open question I did not pursue.
- **Goroutine accounting.** INV-23 names goroutines as a bounded resource. `tenancydb` starts none of
  its own, but I did not measure goroutine growth under the waiter-give-up path over a long run.
- **Dimensions other than 5.** One thing I noticed and am deliberately not filing: the three comment
  blocks in `database.go` (`:113-118`, `:153-158`, `:179-182`) sit on functions of 33, 19 and 19
  lines, against `CLAUDE.md`'s "roughly 40+ lines" rule. That is a readability-dimension call for
  whoever audits it, and the comments are load-bearing explanations of non-obvious invariants, which
  is the other half of that rule.

### Reproduction

The probes live outside the repository, in
`/tmp/claude-1000/-home-user-ws-gd-lease/82a92a15-ac6c-4104-9609-680bbbcbfeb1/scratchpad/probe/`
(module `probe`, with `replace github.com/frostgrove/vv => <repo>`):

- `d3/probe_test.go` + `d3/support_test.go` — the 19 probes P1..P19.
  `GOWORK=off go test -race -count=20 ./d3/` → `ok  probe/d3  39.831s` (19/19, no races).
- `mut/tenancydb/` — a byte-copy of `database.go` plus the repository's own test files, driven by
  `mutate2.sh` for the mutation table.

Baseline on the tree as found: `go test -race -count=20 ./tenancy/tenancydb/` →
`ok github.com/frostgrove/vv/tenancy/tenancydb 3.893s`; `go vet ./tenancy/...` clean;
`gofmt -l tenancy/tenancydb/` silent; `diff` confirms `tenancy/tenancydb/database.go` unmodified.
