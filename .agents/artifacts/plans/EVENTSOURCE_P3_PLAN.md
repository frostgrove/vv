# EVENTSOURCE PHASE 3 — PROJECTIONS, CHECKPOINTS, CATCH-UP — IMPLEMENTATION PLAN

**Status:** plan, phase 3 of the PostgreSQL event-sourcing roadmap.
**Written against:** [`EVENTSOURCE_P3_USECASES.md`](../usecases/EVENTSOURCE_P3_USECASES.md)
(UC-099…UC-130, INV-066…INV-082) and its
[GAPS file](../gaps/EVENTSOURCE_P3_USECASES_GAPS.md) (all seven `[high]` closed);
phases 1 and 2 as frozen semantics; the shipped `event/`, `event/eventmemory/`,
`event/eventtest/`, `event/eventpg/`, `runtime/`; `scripts/checks.sh` and
`scripts/event_test.go`; PostgreSQL **17.9**, measured.
**Format precedent:** [`EVENTSOURCE_P2_PLAN.md`](EVENTSOURCE_P2_PLAN.md).
**[SPEC]** below means
[`EVENTSOURCE_P3_USECASES.md`](../usecases/EVENTSOURCE_P3_USECASES.md); a bare `§`
is a section of it.

**Delivery policy in force (2026-09-08).** Only `[critical]` and `[high]` block.
`[medium]` and `[low]` go to [`EVENTSOURCE_BACKLOG.md`](../gaps/EVENTSOURCE_BACKLOG.md)
under `## P3` and are left alone. **A gate never proceeds silently red:** if a
critical or high survives its fix round, the section's report says so in words,
and `go test` being green is never the report.

Three obligations frame everything below, and all three are executable:

- **Phase 3 moves the frozen kernel, and the move is recorded file by file** —
  [The kernel baseline move](#the-kernel-baseline-move). The arm is never loosened,
  never widened, and gains no exclusion for the new files.
- **`eventmemory.Checkpoints` and `eventpg.Checkpoints` both run
  `eventtest.RunCheckpoints`** — S2, and it is phase 3's central evidence that a
  third implementation is provable.
- **The live gate names its own command and an unset DSN fails it** — unchanged
  from phase 2, and S4 is where the claims that need a database are proved.

---

## What this plan delivers, and what it does not

Four kernel files touched or added (`checkpoint.go`, `bounds.go`, `reader.go`,
`fact.go`) plus the suite's own, three `event/eventmemory` files the ambient
checkpoint join reaches and a fourth that is new, one new root-module package
(`event/projection`, nine non-test source files), one new table and one schema
version in `event/eventpg`, one new conformance runner with twelve sections in
`event/eventtest`, a rewritten `check-event-kernel` arm that no longer needs git
**and a second command, `event-kernel-moved`, that is the fence every section is
reported against**, two module pages in two languages, one flow, four decision
records, one compiled example, and a live gate that fails rather than skips.

It does **not** deliver a snapshot ([the snapshot decision](#the-snapshot-decision-measured-first)),
an outbox, broker delivery, `eventfx`/`projectionfx`, OpenTelemetry, tenancy,
audit, `LISTEN`/`NOTIFY`, `WaitUntilCaughtUp`, a parallel or sharded projector, a
checkpoint sweeper, or a new module. Each refusal is [SPEC] §2's and is not
re-argued.

**`event/projection` is a package of the root module, and that has two
consequences the plan is written around.** First, it may import nothing
third-party — including in its own `_test.go` files, because `check-deps` lists
the root module with `-deps -test -tags=integration` and a test import is a
requirement of the published module. So **every live projection test lives in
`event/eventpg/`**, the satellite that already carries pgx, and `event/projection`
is proved without a database over `eventmemory` and `eventmemory.Checkpoints`.
Second, `scripts/extensions_test.go`'s `startsNothing` goroutine arm applies to it
(the arm exempts only packages that import `testing`), so **no non-test file in
`event/projection` contains a `go` statement.** The loop runs on `Run`'s own
goroutine; `Drain` is a channel and an atomic, never a spawned waiter.

---

## What was measured live before this plan was written

Two claims the whole phase rests on were driven against
`postgres://vv:vv@localhost:55432/vv` (PostgreSQL 17.9) **before** any contract
below was fixed.

### 1. The replay cost the snapshot decision turns on

A scratch schema, the deployed `eventpg` defaults (`StreamPage` 256, `MaxRead`
256), 120-byte payloads through a codec that returns the bytes it was handed, a
separate three-field `event.JSON` aggregate at 95 bytes, batches of 64 through
`Repo.Append`, five passes for each replay and three for each scan, after a
warm-up. `-race` off, one connection, ~0.1 ms round trip.

| Aggregate | Full replay, no-op codec | Full replay, `event.JSON` | Read past the end | Raw one-statement scan |
|---|---|---|---|---|
| 10 000 events (40 pages) | **10.19 – 10.93 ms** | **16.25 – 17.72 ms** | 0.062 – 0.067 ms | 5.67 – 7.05 ms |
| 100 000 events (391 pages) | **104.4 – 105.7 ms** | **163.4 – 170.6 ms** | 0.062 – 0.067 ms | 86.5 – 87.9 ms |

Two more from the same run:

- **A global walk of 220 000 events at `MaxRead` 256 costs 306 – 316 ms**, three
  passes, i.e. **1.39 – 1.43 µs per event end to end**. [SPEC] §5.1 states ~1.4 µs;
  reproduced.
- **Writing** 10 000 events took 238 ms and 100 000 took 2.38 s. [SPEC] §5.1 says
  238 ms and 2.33 s; reproduced.

**One number in [SPEC] §5.1 is optimistic and the plan corrects it rather than
repeating it.** §5.1 says the single-statement scan is 92 – 94 % of the paged
replay, so "paging is not the cost here — 391 pages add about 6 ms". Measured on
this host it is **83 %**: 87.3 ms against 104.9 ms, so the 391 pages cost about
**17.6 ms**, roughly 45 µs a page. The conclusion is unchanged — paging is a sixth
of the replay, not its bulk — but the sentence the module page carries must be the
measured one, because the whole point of the number is that a deployment on a 1 ms
link multiplies exactly this term. The raw scan here also builds no envelopes and
runs no fold, which accounts for part of the gap and is stated so the comparison
is not read as an A/B it is not.

### 2. The fenced checkpoint save, on all four of its paths — **over the `bytea` cursor the plan ships**

The statement [SPEC] §3.1 prescribes, driven directly against 17.9 on a temp
table with the real check constraints and with `cursor bytea NOT NULL CHECK
(octet_length(cursor) >= 1 AND octet_length(cursor) <= 4096)` — the column D11
settles, not [SPEC]'s `text`:

| Path | Result |
|---|---|
| advance 1, no row for the name | `INSERT 0 1` — the row appears |
| advance 1, a row already at advance 1 | `INSERT 0 0` — `c.advance = 0` is never true |
| advance 2, the row at advance 1 | `INSERT 0 1` — the row moves |
| advance 3, no row for the name | `INSERT 0 0` — `EXISTS` is false and nothing is created |

Four more readings taken at the same time, because they decide the DDL:

- **`cursor` is an unreserved keyword** (`pg_get_keywords()` catcode `U`) and
  works as an unquoted column name — on a `bytea` column too, and
  `octet_length(cursor)` needs no quoting either.
- **The check constraints round-trip.** Authored in the `>= … AND … <=` form the
  existing `bytesBetween` helper writes, `pg_get_constraintdef` renders
  `CHECK (((octet_length(projection) >= 1) AND (octet_length(projection) <= 128)))`
  and, for the cursor, `CHECK (((octet_length(cursor) >= 1) AND (octet_length(cursor)
  <= 4096)))` — the same shape level 3's token comparison already strips.
  `BETWEEN`, which [SPEC] §3.1's table uses for readability, must **not** reach
  the DDL: the P2 plan's D2 records what a second spelling costs.
- **A cursor of arbitrary bytes round-trips through the `bytea` column.** Saved
  `\x76766531ff00deadbeef` and then `\x00ff00ff` — a NUL and a `0xff`, neither of
  which is legal text — and read back byte for byte at `octet_length` 4. The same
  two bytes in a `text` column are two server errors, reproduced on this host:
  `insert into t values (chr(0))` → `ERROR: null character not permitted`, and
  `insert into t values (convert_from('\xc3'::bytea,'UTF8'))` → `ERROR: invalid
  byte sequence for encoding "UTF8": 0xc3`. This is the whole of D11's evidence.
- **The empty cursor is refused by the column**, measured:
  `execute save('empty', ''::bytea, 1, …)` → `ERROR: new row for relation "cp3"
  violates check constraint "cp3_cursor_check"`. The `>= 1` bound is not
  decoration: without it the same insert answered `INSERT 0 1` at `octet_length`
  0, which is D12's whole argument.

---

## The nine questions §12 left to the plan, decided — and four the plan raises itself

Each is decided here so it is not decided by whoever writes the line. D10, D11,
D12 and D13 are the plan's own: the code cannot be written without them, and two
of them **override [SPEC]** — the cursor column's type (D11) and the empty-cursor
refusals (D12) — which is said here rather than left in a diff.

### D1 — one migration list does both jobs, and the stamping `UPDATE` is guarded on the v1 fingerprint at *these* bounds  (§12.1)

`MigrationStatements(schema Schema) ([]string, error)` keeps its signature. Version
2 adds a table and alters nothing, so the existing `CREATE … IF NOT EXISTS` list
already transforms a deployed v1 into a v2 — the three existing tables are
untouched and the fourth appears. What must change is the last two statements:

```
INSERT INTO <schema>.schema_meta … VALUES (true, 2, <new log>, <v2 fingerprint>) ON CONFLICT DO NOTHING
UPDATE <schema>.schema_meta SET version = 2, fingerprint = <v2 fingerprint>
 WHERE singleton AND version = 1 AND fingerprint = <v1 fingerprint at these bounds>
DO $eventpg$ … assert version = 2 AND fingerprint = <v2 fingerprint> … $eventpg$
```

**The `UPDATE` is guarded on the deployed fingerprint and not on the version
alone, and that is the whole of this decision.** Guarded on `version = 1` only,
this build's default-bounds list meeting a v1 schema deployed at `MaxPayload 1024`
would create the fourth table, stamp its own version and fingerprint, and then
**pass its own assertion** — §UC-074's silent restamping, reached by a list that
looks correct. Guarded on the fingerprint, the `UPDATE` matches nothing, the
assertion fires `EVPG1`, and the whole transaction rolls back.

The guard needs the **v1 fingerprint at these bounds**, so the expectation model
becomes version-parameterised: `expected(resolved Schema, version int) expectation`
returns the v1 model (three tables) or the v2 model (four), and
`expectation.rendering()` writes `eventpg/schema/v` + that version in its header.
`Schema.Fingerprint()` keeps its signature and answers the current version's;
an unexported `Schema.fingerprintAt(version int)` answers a historical one and is
used by exactly one caller, the guard. A version whose model this build no longer
carries is a build that cannot migrate from it, and the assertion says so.

Three consequences, stated rather than discovered:

- **The three S1 goldens move** (`testdata/migration.golden`,
  `migration_narrow.golden`, `fingerprint.golden`) and the pinned digest literal
  moves with them. That is what a golden is for: a diff a person reads.
- **A new test pins the v1 fingerprint** as a literal, in both bound
  configurations, so a change to the v1 model — which must never happen — is a red
  test and not a schema this build silently declines to migrate.
- `MigrationStatements` grows from eleven statements to **thirteen**: one more
  `CREATE TABLE`, the `UPDATE`, and no new function or trigger (D2). *(Corrected
  from "fourteen" during S2: eleven plus the two additions this bullet itself
  names is thirteen, and thirteen is what the list renders.)*

### D2 — the checkpoints table carries no trigger, and level 3 compares its trigger set as empty  (§12.1, [SPEC] §3.1)

A checkpoint is not history, so the append-only trigger stays on `events` alone.
The rule this makes executable is the one that already exists: level 3 compares
the **expected** trigger set of every table in the model with the deployed one, and
the checkpoints table's expected set is empty — so a trigger added to it by hand is
caught by the comparison §UC-072 already runs, with no new code and no exemption.

### D3 — `Batch.Attempt` is per-process, and no redelivery count is persisted  (§12.2)

A redelivery after a restart arrives with `Attempt == 1`, which is what §UC-106
says and what the plan keeps. A durable count would have to be written on the path
whose entire property is that it wrote nothing — the failing pass — and a handler
that reads "this page has now been delivered 14 times across 3 processes" can do
nothing with it that `(Stream, Version)` idempotency does not already do. Recorded
in the decision doc so the next maintainer does not add a column for a dashboard.

### D4 — `Tolerate` is measured in backoff already waited, not on a wall clock  (§12.3)

`Ready` reports unhealthy when the **accumulated backoff of the current streak**
exceeds `Tolerate`. The sum is computed from the delays the projection itself
granted, so it needs no clock at all, it is exact, and a test ticker drives it —
which is what §UC-109's control asks for and what a wall clock would make a sleep.
A streak is reset to zero by any pass that ended in a save that landed. A single
failure followed by a success therefore never reports unhealthy, and that is the
pinned case.

What it undercounts is the handler's own execution time, deliberately and
statedly: a handler that blocks for ten minutes and then succeeds has waited no
backoff, and the readiness answer that would catch it is a pass timeout the phase
does not have (backlog `## P3` §4, `[medium]`, left alone).

**The alternative was rejected for a reason worth keeping.** A wall clock means
`Spec.Clock`, which [SPEC] §9.2 does not carry and the plan may not add; or
`time.Now()` inside the loop, which makes every readiness test a sleep and makes
this the first package in the subsystem to read a clock nobody injected — phase 1
made an injected clock a *store* obligation and this would be the exception.

### D5 — the quarantine sink is called **inside** the unit under `InUnit`  (§12.4)

[SPEC] §3.4 already makes the isolation pass one unit ("the applies and the
advance commit together, or none of them do"), so a sink called outside it is the
one write that survives the rollback of the advance — and its record then names an
envelope that is redelivered and quarantined again, double-recorded, with no error
anywhere. Inside the unit, a sink writing to the same resource is exactly
consistent with the advance, and a sink writing elsewhere is at-least-once, which
is the obligation the handler already carries.

The module page's contract row for a `Quarantines` implementer therefore reads:
**you are called inside the unit under `InUnit`, and you write through the context
you are given.** A sink that returns an error ends the isolation pass and halts
the projection (§UC-110's control), because a policy with nowhere to record is a
skip with extra words.

### D6 — `Fact.Read`'s value may alias the envelope's payload, and the copy per attempt is what makes that safe  (§12.5)

`Read` follows the replay path's rule unchanged: the value it answers may alias
`envelope.Payload`. What makes that safe here rather than merely stated is §3.3 —
each attempt is handed **its own** copy of every payload, so a value a handler
keeps aliases *that attempt's* bytes, which nothing in the framework writes again.
Pinned by a case that keeps the decoded value, lets the page go, fails retryably,
and asserts the kept value is unchanged after the second attempt has run.

### D7 — the `Router` has no route bound, it seals on first `Apply`, and a late registration is `event.ErrDeclaration`  (§12.6)

**No bound**, and the reason: a router holds one closure per `On` and one entry
per `Ignore`, so its size is bounded by the source code of the program that builds
it. A limit here would be a limit on how many event types an application may
model, which is not a resource this framework rations.

What the question actually exposed is concurrency, and that is decided: the route
maps are written only by `On`/`Ignore` and read only by `Apply`, so **the first
`Apply` seals the router** — the aggregate's own idiom — and a registration after
the seal is refused rather than racing. `Skipped()` is an atomic counter and is
safe for concurrent use; `Apply` after the seal takes no lock and reads two maps
nobody writes.

Every refusal `On`/`TryOn`/`Ignore`/`TryIgnore` makes is
**`event.ErrDeclaration`** — a nil router, a nil fact, a nil applier, a sealed
router, a family or type name the kernel's identifier rule refuses, and a
duplicate route for one `(family, type)`. It is the existing class for exactly
this defect, `On` panics and `TryOn` returns it ([[D-123]]), and §INV-076's count
of twenty-four sentinels does not move.

### D8 — the worked second-database checkpoint store is compiled, not quoted  (§12.7)

`_examples/event-checkpoints-elsewhere/` — a complete `event.Checkpoints` over its
own `*sql.DB`, with the fenced statement, the classification and the `Transaction`
answer, plus the `projection.Spec` that uses it at `Destination:
projection.Unchecked` and the paragraph saying what that costs (§UC-128). It lives
in `_examples` because `make examples` builds, vets and tests it, and a worked
example nothing compiles is a defect waiting for its first reader. It imports
`database/sql`, `crud`, `crud/adapter/crudsql`, `event`, `event/projection` and a
pgx driver `_examples` already carries, so **it needs no new `replace` line** — it
must not import `event/eventpg`, and the checkpoint statement it writes is its own.

The module page links it rather than reproducing it.

### D9 — the projection emits no line, and the decision doc says so in as many words  (§12.9)

Answer (a). Everything reaches an operator through `State`, `Observer` and
`Ready`; `event/projection`'s first-party closure stays `crud`, `errs`, `runtime`,
`utils`, which is exactly what `charged[".../projection"] = "./runtime"` allows,
verified: `go list -deps ./event ./runtime` is those five paths and nothing else.

(b) `port.Logger(ctx)` is [[D-062]]'s own answer and is refused because it widens
`scripts/event_test.go`'s `charged` value from one package pattern to a list —
changing the check's shape for one more spelling of what `Ready` and `Observer`
already carry. (c) `Spec.Logger *slog.Logger` needs no import at all and is
refused because it makes a package that publishes a typed `State` publish an
untyped duplicate of it; a composition root that wants lines writes a five-line
`ObserverFunc`. The cost of (a) is stated rather than hidden: a deployment that
supplies no `Observer` learns a handler panicked only from `Ready`, and learns
nothing of a retry streak below `Tolerate`.

### D10 — three questions the plan raises itself, because the code cannot be written without them

**D10a — which door a checkpoint store's failure enters.** `refuse(err, door)` is
the kernel's existing map and the door decides one thing: what an *unclassified*
error means. The safe guess differs here exactly as it does for a log, so
**`Tracker.Load` enters at the read door** (unclassified → `ErrBackend`; nothing
was written) and **`Tracker.Save` and `Tracker.Forget` at the append door**
(unclassified → `ErrUncertain`; a write whose fate is unknown). No new function,
no new sentinel, §INV-076 holds.

**D10b — a pass whose save may or may not have landed re-seats the fence with
`Tracker.Load`, and the two modes read the answer differently.** There are exactly
two such passes: an `ErrUncertain` from an autocommit save under `AfterApply`, and
an error from the caller's `Unit` after the inner `Save` had already returned nil
under `InUnit`. In both the tracker's in-memory advance and the stored one may
disagree, so the next thing the projection does is `Tracker.Load`, and then:

| Stored advance | `AfterApply` | `InUnit` |
|---|---|---|
| equal to the one this pass presented, **carrying the cursor it presented** | the save landed — continue from the in-memory cursor at that advance | the unit committed — continue from the in-memory cursor at that advance |
| equal to the one this pass presented, **carrying any other cursor** | another live instance took that advance while this pass's save did not land — take that row and rebuild the reader from **its** cursor, exactly as a refused save does | the same |
| one below it | **halt** — §UC-104's Must-not: the handler's rows are already committed and re-applying is a duplicate the resolution must not decide to make | **re-apply the page** — nothing committed, the writes rolled back with the advance, and the page is where the projection left it |
| anything else | halt | halt |

**Changed in S4's review.** The first row was one row and read the advance alone
as proof of authorship. It is not proof: the fence admits one writer at each
advance, and a rolling deploy's second instance reaches it the moment this pass's
unit rolls back and releases the row. Continuing from the in-memory cursor then
skips every position between the two cursors — applied by nobody, behind the
checkpoint at the next save, `Quarantined` zero and no error anywhere. Driven live
on 17.9: `[s-1 s-4]` in the read model over a log of four, the row at advance 2,
highest 4.

**Changed in S3 ([[D-133]]).** The resolution is now entered by an `ErrConflict`
too, and the row that answers is read against *which* of the two left the outcome
unread. The table above holds for the unconfirmed save; the fenced one reads its
own first two rows differently, because a save the store **refused** cannot be
what put the row where it is:

| Stored advance | after a refused save |
|---|---|
| equal to the one this pass presented, or above it | another live writer at this name — take that row, rebuild the reader from **its** cursor, drop the page, back off; the streak it opens is cleared only by a save that lands |
| one below it | the store refused a save over the very row its own fence admits — halt |
| anything else, absence included | the row was forgotten, reset or restored behind a running projection — halt |

The resolving load also runs through a **tracker of its own**, and the argument is
in [[D-133]]: the window's fifth re-check exists to catch a row that moved
*unnoticed*, the loop has just been told it moved, and the table above tells four
cases apart where the window has one refusal for all of them. The other four
re-checks run unchanged. And the pending resolution is **held across a failed
load**, so a backend that goes away during it is retried as the resolution rather
than as the pass: re-delivering would present a second save over one the tracker
cannot account for, which the door refuses.

The two rows differ because the *evidence* differs, not because the modes are
inconsistent: under `InUnit` a stored advance one below is proof that the whole
unit rolled back, and under `AfterApply` it is proof of nothing about the
handler's writes. This is also **what bounds an uncertain unit commit**: if a
commit landed and reported an error, the reload finds the advance already moved
and the loop continues rather than presenting the same advance twice.

**What the door holds and what this table still decides.** The table's third row
— "anything else: halt" — was, in the first cut of S1, a rule only this table
knew: `Tracker.Load` re-seated its fence to whatever the store last answered, in
either direction, so a consumer that did not implement the row silently resumed
at the origin (a forgotten row) or took turns with a second writer (a re-read
advance). That is now the door's, as the window in §"What `Tracker.Load`
re-checks" item 6: the two answers the table calls "anything else" are refused
`ErrWrongStore` before the consumer sees them, so a second consumer of
`Checkpoints` inherits the rule rather than re-deriving it (§INV-082). The
**first two rows stay the consumer's**, because they are a question about the
*handler's* writes rather than about the row: the door cannot know whether a
page was applied, so it admits both advances and this table decides which of
continue, re-apply and halt each mode owes. `Tracker.Save` also refuses a second
save over an unresolved one, so "must not re-save blindly" (§UC-104) is held
where a consumer cannot skip it, and the resolving `Load` is the only exit.

**D10c — the per-pass `InUnit` refusals never reach `Classifier`.** [SPEC] §3.4's
table sends *the handler's own error* to the classifier. The two checks §3.2(3)
makes inside the unit are the framework's own refusals, they happen before the
handler runs, and they halt directly — a `Classifier` an application supplied must
not be able to call the framework's wiring refusal retryable and spin on it
forever.

### D11 — the `cursor` column is `bytea`, because a cursor is opaque bytes and `text` is not

[SPEC] §3.1's table says `cursor text`. **The plan overrides it**, because the
kernel it is written against says the opposite: `event.Cursor` is `type Cursor
string` and the kernel's text rule — `checkText`/`checkName`, "valid UTF-8, no
NUL, no control character" — is applied to a key, a family and a wire type name
and to **no cursor anywhere**. A cursor is unconstrained bytes by design, and
§1.2's own words for what a checkpoint store does with it are "persists the
store's opaque cursor as bytes and reads nothing in it".

PostgreSQL `text` cannot hold those bytes, measured above. The same schema
already answers this correctly one table over: `events.payload` is **`bytea`**
(`event/eventpg/schema.go:240`) precisely because it holds bytes the kernel does
not constrain, while `family`, `key` and `type` are `text` because the kernel
*does* constrain them. The checkpoint's `cursor` is the payload's case, not the
key's.

**What it would cost to leave it `text`.** `eventpg.Checkpoints` takes a `DB` and
a `Source` and never sees a log, so a checkpoint table in PostgreSQL beside a log
that is *not* `eventpg` is a wiring the type system invites and §3.1 blesses
("the two may be one resource … or two"). Hand it a third-party `Log` whose
cursor is a packed binary struct — legal, unconstrained, and the obvious encoding
for a store that does not want base64's 33 % — and every `Save` fails with
SQLSTATE 22021 or 22P05. The backend survives it, so `outcomeOf` answers
`NotWritten`, `refuse` maps that to **`ErrBackend`**, and `pass.go`'s table calls
`ErrBackend` on a save *retryable without limit* — so the projection retries a
write that is structurally impossible, forever, and reports unhealthy only after
`Tolerate`. The two shipped logs happen to mint ASCII (`eventpg` a four-character
tag plus base64url, 58 bytes; `eventmemory` a fingerprint, a colon and decimal
digits), which is exactly why no test written against them would find it.

**The rejected alternatives, both of them.** *Base64 in a `text` column*: the
store owns an encoding the cursor does not have, adds a decode-failure path on
the read that has no honest classification, and spends 33 % of `MaxCursorBytes`
on a column an operator reads in `psql` about as often as they read `payload`.
*The kernel declares a cursor to be legal text and enforces it at both
store-honesty doors*: this is the larger change and it is the wrong one — it
narrows what every `Log` in existence may mint, retroactively, to make one
column's type work, and §1.2 has already said a cursor is opaque. Recorded as
rejected rather than left unsaid, because it is the shape a later reader will
propose.

The store binds `[]byte(checkpoint.Cursor)` and reads `[]byte` back into
`event.Cursor`; `[]byte` is one of the six types the driver rule admits and is
what `payload` already binds. Nothing base64s anything.

### D12 — an empty cursor is refused at three doors, because the empty cursor **is** the origin

`event.Read(log, "")` is the origin of the log, in both shipped stores, by
design and with a comment saying so (`eventpg/cursor.go:readCursor` answers
`walk{}`, `eventmemory/cursor.go:readCursor` answers position 0). So an empty
cursor stored at a non-zero advance is a *readable* checkpoint that resumes a
projection at the beginning of the log — §UC-101's Must-not ("it must not restart
from the origin — that re-applies the whole log against a live read model")
reached without a single error on any path, and §INV-068's falsification never
fires because nothing was unreadable.

The door already checks that **absence is total** and nothing checked that
presence is. Three refusals close it, and each is at a different door because
each catches a different party:

| Door | What it catches | Class |
|---|---|---|
| `Reader.checkPage` | a `Log` minting `""` beside a **non-empty** page | `ErrBackend` — a store dishonesty, beside the over-length case |
| `Tracker.Load` | a stored row at `Advance > 0` whose cursor is empty, by any hand | `ErrWrongStore` — the same class as the other three re-checks |
| `Tracker.Save` and `Checkpoints.Save` | a caller presenting `""`, and a row reaching `''` under the store | `ErrWrongStore` at the door; `event.Refused` at the store; `octet_length(cursor) >= 1` in the DDL |

An **empty page answered with an empty cursor is legal and stays legal** — that
is a fresh log read from the origin, and nothing was delivered to checkpoint
past.

**Why `Tracker.Save`'s class is not `Save`'s over-ceiling class.** The
over-ceiling refusal is `ErrTooLarge` and stays there; backlog `## P3` §34 asks
whether that is right and is `[low]`, untouched. The two are different defects
reached by different parties: an over-ceiling cursor is a *store* minting
something too big, and an empty one at `Save` can only be a caller presenting a
cursor that is not one — which is what `Tracker.Save`'s other refusal, the save
before a load, already calls `ErrWrongStore`. Both are terminal in `pass.go`'s
table, so the projection halts either way; the class is what an operator reads.

### D13 — the memory checkpoint store joins the log's transaction, and that is a shape change to `eventmemory.Tx`

`event/eventmemory/transaction.go` as it stands cannot carry a checkpoint save:
`Tx` holds `{log, identity, finished, staged []event.Envelope, counts}`, `Commit`
publishes `staged` and nothing else, and `Rollback` does `this.log.position +=
event.Position(len(this.staged))` — so the staged slice is load-bearing for burnt
positions and a checkpoint row cannot ride in it. `ambient` is a `*Store` method,
and a `*Checkpoints` is not a `*Store`.

So the join is three named changes, not one new file, and they are on
[the path list](#the-complete-set-of-paths-phase-3-may-add-to-the-manifest) with
the sentence each of them earns:

- **`Tx` gains a second staging area**, `saves []event.Checkpoint`, published by
  `Commit` after the envelopes and discarded by `Rollback`. **`Rollback`'s
  position arithmetic does not read it** — that is the property the change is
  written around and the case below pins, because a checkpoint row that burnt a
  log position would make a rolled-back unit skip an event.
- **`ambient` lifts from `*Store` to `*Log`**, and `Store.ambient` and
  `Store.Transaction` become one-line delegations. The context key is already
  `transactionKey{log *Log}`, so this moves no semantics: it makes the lookup
  reachable by the log's other peer. `Checkpoints.Begin(ctx) (*Tx, error)`
  mirrors `Store.Begin` — a transaction of the log, refused when *this* value is
  closed — and `WithTransaction` is untouched, so a unit begun through either
  peer is found by both.
- **The rows live on the `*Log`**, beside the envelopes and under the same mutex,
  for the reason the envelopes do: two checkpoint values over one log are one
  store, and a restart resumes through a value that did not exist when the row
  was written.

The fence is evaluated when `Save` is called, under the log's mutex, against the
rows as this transaction's own staged saves amend them; `Commit` re-validates it
exactly as `revalidate` re-validates versions, so a fence broken between the
stage and the commit is an error from `Commit` rather than a row that overwrote a
winner.

---

## The kernel baseline move

`check-event-kernel` exists because phase 2 had to prove it needed no kernel edit.
Phase 3 needs five files' worth, plus a new package, so **the arm is re-baselined —
deliberately, in the same commit as the code, and never loosened.**

### What moves, and why each cannot live elsewhere

| File | What moves | Why here |
|---|---|---|
| `event/checkpoint.go` *(new)* | `Checkpoint`, `Progress`, `CheckpointCapabilities`, `Checkpoints`, `Track`, `Tracker` | Two store packages implement it and a third must be provable against it; a contract living in the consumer would make every store depend on the consumer. `Track` is here for the reason `Read` is: a second consumer of `Checkpoints` inherits the re-checks (§INV-082) rather than re-deriving them |
| `event/bounds.go` | `MaxCursorBytes = 4096` | It is a ceiling on what a store mints, and the ceilings are one list |
| `event/reader.go` | `checkPage(page, cursor)` — the signature takes the cursor and bounds it | Store honesty is checked in one place. Backlog `## P3` §13 asked whether the entry was a branch or a signature change; it is a signature change and this is the answer |
| `event/fact.go` | `Fact.Family`, `Fact.Read` | Only `Fact` holds the reader chain; a decoder outside it would need the chain exported |
| `event/eventtest/` | `checkpoints.go`, `sections_checkpoints.go`, `defects_checkpoints.go`, `inventory.go` (the checkpoint inventory), and the in-flight case in `sections_resumption.go` | The suite is the kernel's evidence half |
| `event/eventmemory/` | `checkpoints.go` *(new)*, and `transaction.go`, `log.go`, `store.go` for the ambient join — D13 names what moves in each | The `InUnit` half of the phase is proved with no database at all, and this is the store that makes it reachable |
| `event/projection/` *(new package, 9 files)* | the whole consumer | [SPEC] §2 non-goal 10 — no new module, so it is a package of the root module and it is under `event/` |

**Nothing on the exported surface changes shape.** No sentinel is added or
removed, no exported type gains a field, and the one kernel signature that moves
is unexported (`checkPage`). `Reader.Next`, `Reader.Events`, `Reader.Cursor`,
`Read`, `ReadOnly`, every store interface and every one of the twenty-four
sentinels are untouched. **One unexported type does change shape and it is named
rather than discovered**: `eventmemory.Tx` gains a second staging area (D13),
and the case that pins what that must not disturb is in S2.

### The arm becomes a manifest, and that is the second half of the decision

[SPEC] §11.1 names two spellings and asks the plan to choose. **The plan chooses
(b), and sharpens it: not a content *digest* but a content *manifest*.**

A bare digest re-baselines atomically and names nothing — the arm's current
self-test asserts it "refused without naming the file that moved", and a single
sha cannot. A **manifest** — one `<sha256>  <path>` line per file under `event/`
outside `event/eventpg`, recorded in `scripts/event_kernel.sha256` — keeps every
property the git arm had, adds one it did not, and buys three things:

1. **It re-baselines in the same commit.** The recorded file is regenerated beside
   the code, so there is never a red `make check` between two commits — which is
   what spelling (a) costs and what a maintainer under time pressure resolves by
   deleting the arm.
2. **It names the file, in three directions.** A `diff` of the recorded manifest
   against the computed one names what changed, what appeared, *and what
   disappeared*. The git version caught removals through `git diff`; a naive
   digest-per-file walk that looked each recorded file up would not, so removal
   gets its own self-test case.
3. **It needs no git**, which closes backlog `## P2` §5 outright — both halves.
   The arm can run from a tarball or a vendor directory, and the re-baselining
   rule that entry said was unstated is now a command the failure message prints.

**The re-baselining is a recorded act, not a relaxation, and the difference is
mechanical.** Moving a 40-character sha is one opaque line in a diff; regenerating
the manifest is a diff that lists every file that moved, by name, next to the code
that moved it. Reviewing the second is possible and reviewing the first is not.

```sh
./scripts/checks.sh event-kernel            # compares, refuses when it cannot ask
./scripts/checks.sh event-kernel-baseline   # regenerates scripts/event_kernel.sha256
./scripts/checks.sh event-kernel-moved <predecessor> <allowed> [required…]   # the fence
```

The second is reachable as `make check-event-kernel-baseline`, is **not** part of
`make check`'s `all`, and is named in the failure message of the first. The third
is [the fence](#the-fence-is-a-command-and-not-a-shell-pipeline) and is in
neither.

### The arm's four refusal branches, unchanged in spirit from phase 2

Every one refuses rather than reporting ok, because a check that passes when it
cannot ask its question is backlog P1 §6:

1. `sha256sum` is not on the path → refuse.
2. `scripts/event_kernel.sha256` is missing or empty → refuse, naming it.
3. the computed manifest is empty — nothing was found under `event/` outside
   `event/eventpg` → refuse, because a moved directory would otherwise read as a
   clean tree.
4. otherwise `diff` the two; a non-empty diff fails and prints it.

Files are enumerated with `find event -type f -not -path 'event/eventpg/*'` and
sorted `LC_ALL=C`, so the manifest is byte-stable across machines and an untracked
new file is caught for free — the half the git version needed a second command
for.

### The fence is a command and not a shell pipeline

`check-event-kernel` is **green by construction** the instant
`event-kernel-baseline` runs. So the only thing standing between phase 3 and an
unrecorded kernel edit is the arm that reads *what the re-baseline moved*, and
that arm has to be as hard to silence as the check it guards.

The plan's first draft spelled it `! git diff HEAD -- scripts/event_kernel.sha256
| sed … | grep -qvE '<allowed>'`, and that arm **passes vacuously the moment the
section is committed**: `git diff HEAD` for a committed file is empty, `grep -qv`
finds nothing, exits 1, and the leading `!` turns that into success. The plan
asked for the re-baseline to land in the same commit as the code, so the intended
workflow silenced the arm. It also asserted only that no *unexpected* path
appeared, never that the expected ones did.

So the fence becomes a command of `scripts/checks.sh`, for three reasons: a
command can be given a predecessor that survives a commit, a command can be
tested (`scripts/checks_test.go`, which a pipeline in a plan cannot be), and a
command matches with bash's own `[[ =~ ]]` instead of `grep -qv` — which is the
one construct whose exit status an agent harness's `ugrep` wrapper inverts.

```
./scripts/checks.sh event-kernel-moved <predecessor> <allowed-ERE> [required-path…]
```

What it does, in order, and every refusal names what it could not ask:

1. `<predecessor>` missing or empty → **refuse**, naming it and the command that
   records one. A fence with nothing to compare against is backlog P1 §6.
2. `scripts/event_kernel.sha256` missing or empty → **refuse**, naming it.
3. compute the moved set: every path whose line differs between the two
   manifests, in either direction, so a file that **appeared**, one that
   **changed** and one that **disappeared** are all in it.
4. the moved set is **empty** → **fail**: a section that moved no kernel file did
   not deliver, and this is the arm that the committed-diff spelling silently
   lost.
5. any path in the moved set outside `<allowed-ERE>` → **fail**, naming every
   one. This is the unplanned kernel edit, reported out loud rather than
   absorbed.
6. any `<required-path>` **not** in the moved set → **fail**, naming it. A
   section that was to add `event/checkpoint.go` and did not is a section that
   did not deliver, and no arm before this one could see that.
7. otherwise print the moved set and `event-kernel-moved: ok`.

**The predecessor is recorded before the section's first file is written**, as
that section's opening act, and it is what makes the fence idempotent — a
reviewer can run the checkpoint before the commit, after it, and twice, and get
the same answer:

```sh
./scripts/checks.sh event-kernel-baseline && cp scripts/event_kernel.sha256 .git/event_kernel_before_s2
```

`.git/` is scratch tied to this clone: untracked by construction, invisible to
`find event`, `gofmt` and every check, and gone when the clone is. It is not an
artefact anybody reviews — the artefact is `scripts/event_kernel.sha256`, whose
diff a person reads.

### The complete set of paths phase 3 may add to the manifest

This list is the plan's own fence, and every section's checkpoint asserts against
it: a path appearing in a manifest diff that is not on this list is an unplanned
kernel edit and is **reported out loud rather than absorbed**.

```
event/checkpoint.go                       event/projection/doc.go
event/checkpoint_test.go                  event/projection/spec.go
event/tracker_test.go                     event/projection/projection.go
event/bounds.go            (modified)     event/projection/pass.go
event/bounds_test.go       (modified)     event/projection/page.go
event/reader.go            (modified)     event/projection/classify.go
event/reader_test.go       (modified)     event/projection/state.go
event/fact.go              (modified)     event/projection/router.go
event/fact_test.go         (new)          event/projection/errors.go
event/eventtest/checkpoints.go            event/projection/*_test.go
event/eventtest/sections_checkpoints.go   event/eventmemory/checkpoints.go
event/eventtest/defects_checkpoints.go    event/eventmemory/checkpoints_test.go
event/eventtest/inventory.go            (modified — the checkpoint inventory)
event/eventtest/sections_resumption.go  (modified — the in-flight case, UC-124)
event/eventtest/suite.go                (modified — see below)
event/eventtest/probe.go                (modified — see below)
event/eventtest/report.go               (modified — see below)
event/eventtest/sections_read.go        (modified — the walk that answers the
                                         cursor it reached, which the in-flight
                                         case needs and `from` cannot answer)
event/eventtest/*_test.go
event/eventmemory/transaction.go        (modified — see below)
event/eventmemory/log.go                (modified — see below)
event/eventmemory/store.go              (modified — see below)
event/eventmemory/transaction_test.go   (modified — see below)
```

Everything else under `event/` outside `event/eventpg` stays byte-identical, and
S5's checkpoint proves it.

**Why the three `eventmemory` files are on the list, named rather than
discovered.** The plan's first draft named only `checkpoints.go`, and the file it
did not name is the one that decides whether a rolled-back in-memory unit burns
positions correctly — which is not a file anybody should edit because a section
turned red. D13 is the decision; this is the sentence each file earns:

- **`transaction.go`** — `Tx` gains `saves []event.Checkpoint`, published by
  `Commit` and discarded by `Rollback`, which `Rollback`'s position arithmetic
  does not read; `ambient` lifts from `*Store` to `*Log`.
- **`log.go`** — the checkpoint rows live on the `*Log`, beside the envelopes and
  under the same mutex, so two checkpoint values over one log are one store.
- **`store.go`** — nothing. *(S2's finding: `Store.ambient` and
  `Store.Transaction` have always lived in `transaction.go`, so the delegations
  landed there and this file is byte-identical. It stays allowed and is not
  required.)*
- **`transaction_test.go`** — the existing rollback cases gain the one D13
  creates: a unit that staged **both** an append and a checkpoint save.

**If the join turns out to need more than these** — a fourth file, or a change to
`Commit`'s or `Rollback`'s own arithmetic rather than an addition beside it — S2
stops and reports it, exactly as it does for `sweep`. Widening the fence is what
this list exists to make visible.

**Why `suite.go`, `probe.go` and `report.go` are on the list, named rather than
discovered.** `RunCheckpoints` reuses `sweep`, `probe`, `verdict` and `word`
rather than growing a second copy of them: two verdict types would be two accounts
of one rule, and the three anti-vacuity rules would drift the way `scripts/`'s
tenancy grep and event walk already did. Reuse means `sweep` and the parts of
`probe` that are about running a section rather than about a `Store` become
generic over the factory kind — a small interface, or a type parameter — instead
of naming `Factory` and `event.Store` directly. That is three frozen files
changing for **one shared mechanism**, which is a different thing from three
frozen files each gaining a feature, and it is written down so the manifest diff
is expected rather than argued about afterwards. **If it turns out to be more than
a signature — if `sweep` needs a second body — S2 stops and reports it rather than
widening this list.**

---

## Coverage matrix

Every UC and INV of [SPEC]. **Section** is where it is delivered; **Checkpoint** is
the section whose command proves it; **Proved by** names the test or the
`eventtest` subtest. A lower-case name — `fence`, `names`, `resumption` — is a
`t.Run` subtest of `eventtest.Run` or `eventtest.RunCheckpoints`, and is only
meaningful because S2's mutation harness shows the suite catches a broken store
while exercising the real one.

### Use cases

**Every name below is in the **Tests** list of the section its Checkpoint column
names, and inside that checkpoint's own counted `-list` pattern.** The two lists
are reconciled in both directions: a name here that no arm counts is a use case
that can be reported closed on a green `go test`, which is phase 1's failure mode
exactly.

| UC | Section | Checkpoint | Proved by |
|---|---|---|---|
| UC-099 a projection runs for the first time | S1 (`Tracker`) + S3 (the loop) | S1 + S3 | `TestAFreshTrackerAnswersTheZeroCheckpointAndSavesAtAdvanceOne` (S1), `TestAFirstRunStartsAtTheOriginAndASecondResumes` (S3) |
| UC-100 a projection resumes in another process | S2 (`Sibling`) + S4 (live) | S2 + S4 | `durability` — `passed` for live `eventpg`, `not certified` for `eventmemory`, both pinned by name (S2); `TestAProjectionResumesThroughASecondValueOverOneBacking` (S4) |
| UC-129 a store answers a checkpoint that is not this projection's | S1 (the door) + S2 (the suite) | S1 + S2 | `TestTheDoorRefusesAnAnswerAboutAnotherName` (S1), `names` — driven by `TestEveryCheckpointDefectIsReportedByItsOwnSection` (S2) |
| UC-101 a checkpoint names another log, or a retired format | S3 | S1 + S3 | `TestAnUnreadableCursorHaltsAndNeverRestartsAtTheOrigin` (S3); and the route that is *not* a refusal, which D12 closes: `TestPresenceIsTotalAndAnEmptyCursorIsNeverAResumePoint` (S1) |
| UC-102 a checkpoint is forgotten under a running projection | S2 (`Forget`) + S3 | S2 + S3 | `forget` (S2), `TestAForgottenCheckpointRefusesTheNextSaveAndHalts` (S3) |
| UC-103 two replicas run one projection name | S2 (the fence) + S3 (what the loser does) | S2 (live) + S3 | `TestEightSaversAtOneAdvanceLeaveOneWinnerAndSevenConflicts`, `concurrency` (S2); `TestTwoLiveInstancesOfOneNameTakeTurnsAndNeitherHalts`, `TestTwoLiveInstancesUnderInUnitApplyEachEventOnce`, `TestTheAdvanceIsClaimedBeforeTheHandlerInsideAUnitAndAfterItOutside` (S3); `TestASettlementResumesFromTheRowsCursorAndNeverFromItsOwn`, `TestASettlementTakesTheRowsCursorWhenAShorterWinnerLeftIt` (S4 — the settlement's own half of the same case) |
| UC-104 a checkpoint save is unconfirmed | S3 (the resolution) | S4 (live) | `TestAnUnconfirmedSaveIsResolvedByOneLoadAndNeverBySaving`, `TestASettlementTakesTheRowsCursorWhenAShorterWinnerLeftIt` (S4, live); `TestASettlementResumesFromTheRowsCursorAndNeverFromItsOwn` (S4, no database) |
| UC-105 a page and the advance in one transaction | S3 (memory) + S4 (live) | S3 + S4 | `TestTheAdvanceAndTheHandlersRowsCommitTogetherInMemory` (S3), `TestTheTwoModesLeaveDifferentStateAtOneKillPoint` (S4) |
| UC-106 the process dies between the write and the checkpoint | S4 | S4 | `TestTheTwoModesLeaveDifferentStateAtOneKillPoint` (S4) |
| UC-107 `InUnit` is asked for and cannot be honoured | S3 (a–d) + S4 (e) | S3 + S4 | `TestInUnitIsRefusedAndNeverDowngraded` (S3, four halves), `TestThreeWiringsToASecondDatabaseAreToldApart` (S4, the fifth) |
| UC-108 a page is delivered twice | S3 | S3 | `TestARedeliveryCarriesTheSameIdentitiesInMemoryOfItsOwn` (S3) |
| UC-109 a handler fails and then succeeds | S3 | S3 | `TestARetryReAppliesTheLogsOwnPageAfterABackoff` + the mutating-handler control (S3) |
| UC-110 a handler fails permanently for one event | S3 (memory) + S4 (live) | S3 + S4 | `TestAPermanentFailureHaltsAndQuarantineIsEnvelopeGranular` (S3), `TestAQuarantineIsEnvelopeGranular` (S4) |
| UC-111 a payload this build cannot read | S1 (`Fact.Read`) + S3 | S1 + S3 | `TestFactReadDecodesThroughItsOwnChainAndRefusesTheFourHistoryClasses` (S1), `TestAHistoryClassFailureHaltsAndNamesNoData` (S3) |
| UC-128 a projection writes to a database this framework does not know | S4 | S4 | `TestThreeWiringsToASecondDatabaseAreToldApart` (S4) |
| UC-130 a router covers two aggregates, one registration missing | S3 (memory) + S4 (live) | S3 + S4 | `TestARouterRoutesDeclaresAndRefusesTheUnclaimed` + both controls, once in `event/projection` (S3) and once in `event/eventpg` (S4) |
| UC-112 a projection drains and then follows | S3 | S3 | `TestADrainingProjectionReachesFollowingAndStaysThere` (S3) |
| UC-113 a projection meets a position still in flight | S4 | S4 | `TestAProjectionDoesNotPassAPositionAWriterCouldStillCommit` (S4) |
| UC-114 a poll finds nothing | S3 | S3 | `TestNIdlePollsIssueZeroSavesAndNRoundTrips` (S3) |
| UC-115 a projection is given a store rather than a log | S3 | S3 | `TestASpecCarryingAStoreIsRefusedAndReadOnlyIsAccepted` (S3) |
| UC-116 the log's store is closed under a running projection | S3 | S3 | `TestAClosedStoreHaltsAndATransientBackendRecovers` (S3) |
| UC-117 the host supervises a projection | S3 | S3 | `TestTheSupervisorHoldsAProjectionAndNewStartsNothing` (S3); `startsNothing`, the arm of `TestMerelyImportingTheEventExtensionStartsNothing` (`./scripts/`, counted in S3) |
| UC-118 a projection is drained at shutdown | S3 | S3 | `TestADrainFinishesThePassInFlightAndAHaltedOneReturnsAtOnce` (S3) |
| UC-119 a halted projection answers readiness | S3 | S3 | `TestAHaltedProjectionIssuesNothingAndReportsThroughReady` (S3) |
| UC-120 a projection is rebuilt beside the live one | S4 | S4 | `TestARebuiltProjectionDrainsBesideTheLiveOneAndTheRowsAgree` + the wrong-`v2` control (S4) |
| UC-121 the rebuilt projection is wrong and the cutover is rolled back | S4 | S4 | `TestARollbackRestoresNothingBecauseNothingWasTouched` (S4) |
| UC-122 a retired projection's checkpoint is forgotten | S2 | S2 | `forget`, `TestForgetTouchesOneNameAndAnAbsentOneIsNotARefusal` (S2, untagged) |
| UC-123 a checkpoint-store implementer runs the conformance suite | S2 | S2 | `TestTheCheckpointStoreSatisfiesTheContract` (both stores), `TestEveryCheckpointDefectIsReportedByItsOwnSection` (the five-defect harness), and the two admission self-tests below |
| UC-124 the suite walks the log while a lower position is uncommitted | S2 | S2 (live) | `resumption` extended, and `TestTheNewestPositionCursorNowFailsResumption` |
| UC-125 a deployment measures its own replay cost | S4 | S4 | `BenchmarkStreamReplay` (counted as a benchmark), `TestTheReplayBenchmarkMeasuresTwoOrdersApart` (S4) |
| UC-126 a snapshot accelerates a replay | — | — | **deferred**, [the snapshot decision](#the-snapshot-decision-measured-first) |
| UC-127 a snapshot is stale, corrupt or unreadable | — | — | **deferred**, same |

### Invariants

| INV | Section | Checkpoint | Proved by |
|---|---|---|---|
| INV-066 a checkpoint is a store-minted cursor, never a position | S1 + S4 + S5 | S4 + S5 | UC-113's live case (S4); `TestNoExportedFunctionTakesAPositionAndAnswersACursor` (a `go/types` walk over the regenerated surface) and `TestCursorIsNeverCompared` (an AST walk for a binary op over `event.Cursor` outside a store's own parser) — both in `scripts/projection_test.go`, counted in S5 |
| INV-067 a checkpoint is saved once per page the projection finished with | S3 + S4 | S3 | UC-114 (zero over N idle polls), UC-109 (unchanged across two failed attempts), UC-112 (one per applied page), UC-110 under both policies |
| INV-068 an unreadable checkpoint never restarts a projection | S1 + S3 | S1 + S3 | UC-101 paired with UC-099's absence; the stored row asserted byte-identical after the halt (S3); and D12's two other doors — `TestPresenceIsTotalAndAnEmptyCursorIsNeverAResumePoint` and `TestAReaderRefusesAnEmptyCursorBesideANonEmptyPage` (S1) |
| INV-069 the advance is fenced, the fence is the door's, a loser takes its turn ([[D-133]] — it stopped until S3) | S1 + S2 + S3 | S1 + S2 (live) + S3 | `TestASaveBeforeALoadIsRefused`, `TestTheFenceIsTheDoorsAndNoCallerComputesIt` (S1); UC-103, `fence`, the hundred-advance control (S2) |
| INV-070 `InUnit` is checked as far as it can be seen | S3 + S4 | S3 + S4 | UC-107's five halves, UC-105/106's kill-point pair, UC-128's three wirings |
| INV-071 the log is read outside every unit of work | S3 + S4 | S3 + S4 | `TestEveryReadArrivesOutsideEveryUnit` — a recording `Log` decorator asserting every `ReadAll` arrived with no executor of that backing bound (S3); `TestAProjectionInAUnitPassesABurntGap`, the burnt-gap case under a deadline (S4) |
| INV-072 delivery is at least once, and no wording says otherwise | S5 | S5 | `TestNoDocPromisesExactlyOnceDelivery` over the new pages, the flow and the four decisions, with `TestAPromiseOfExactlyOnceDeliveryIsReportedAndARefusalOfOneIsNot` — its own self-falsification — beside it; `TestNoCommentInTheProjectionPackagePromisesExactlyOnce`, the source-comment walk, in `scripts/projection_test.go` — because the existing test reads `docs/` only (`scripts/docs_test.go:801`) |
| INV-073 a projection never skips an event it could not apply | S3 + S4 | S3 + S4 | UC-110 under both policies; the isolation pass's retryable control; `TestASettlementTakesTheRowsCursorWhenAShorterWinnerLeftIt` (S4, live — the skip a settlement could make without any policy at all) |
| INV-074 nothing starts, nothing is discovered, nothing is logged | S3 | S3 | `TestTheProjectionStartsNothingAndReadsNoEnvironment` — the AST walk for `go`, `init`, `log.`, `fmt.Print*`, `os.Getenv`; and the three existing `./scripts/` walks the new package falls under, all counted in S3: `TestMerelyImportingTheEventExtensionStartsNothing`, `TestNoEventPackageCostsMoreThanTheSeamItNames` and `TestNoBaseSubsystemDependsOnTheEventExtension` |
| INV-075 a projector holds a log and cannot append | S3 | S3 | UC-115's pair; a compile-time assertion that `Spec` has no `event.Store` field |
| INV-076 phase 3 adds no sentinel and no class | S1 + S2 | S1 + S2 (live) | `TestEachCheckpointDoorMapsItsUnclassifiedFailureItsOwnWay` and the vocabulary table test still reading twenty-four (S1); `TestEveryFailureTheCheckpointStoresAnswerCarriesOneOfTheSeven`, over both stores (S2) |
| INV-077 progress is an observation and cannot become a resume point | S1 + S5 | S5 | `TestNoConstructorTakesAProgressAndAnswersACursor`, the surface check over the regenerated `docs/api/surface.md`, in `scripts/projection_test.go`; UC-121 |
| INV-078 a cursor a store mints fits the published ceiling | S1 + S2 | S1 + S2 (live) | `TestAReaderRefusesACursorOverTheCeiling` and the third defective store of `TestTheDoorRefusesAnAnswerAboutAnotherName` (S1); `TestASaveOfACursorAtExactlyTheCeilingLands` (S2, live) |
| INV-079 full replay stays the only authority | S5 | S5 | `TestNoSnapshotAuthorityIsDeclaredOrPromised` — the §INV-008 grep extended to `projection`, and no snapshot symbol in the surface baseline — in `scripts/projection_test.go` |
| INV-080 the suite reports a cursor that skips an in-flight position | S2 | S2 (live) | UC-124's decorator failing `resumption`, paired with the unmodified store passing it |
| INV-081 a projection's state is a function of the log and its declared routes | S3 + S4 | S3 + S4 | UC-130's pair on both sides of the covered-family line, in both packages; `Ignore` applying nothing and refusing nothing |
| INV-082 a checkpoint store's answer is re-checked at the door | S1 + S2 | S1 + S2 | UC-129's three defective stores plus the honest control, and the empty-cursor fourth (S1); `names` in the suite (S2) |

---

## Contracts before code

Every signature below is written before the code, by file and by package. Receiver
name is `this` throughout. Comments are exceptional; the ones quoted here are the
ones a genuinely complex function or a non-obvious invariant earns.

### `event/checkpoint.go` — the contract, the door and the fence  *(kernel, new)*

```go
package event

// Written at one point and one only: the save that carries it. So it describes a
// page that was applied or quarantined, never one that was merely delivered, and
// a projection retrying or halted on the page it just read publishes what it had
// before that page.
type Progress struct {
	Highest     Position
	Applied     uint64
	Quarantined uint64
	At          time.Time
}

func (this Progress) zero() bool // Highest, Applied, Quarantined all 0 and At.IsZero()

// The resume authority is Cursor. Advance is a fence and not a position;
// Progress is an observation and not a resume point.
type Checkpoint struct {
	Projection string
	Cursor     Cursor
	Advance    uint64
	Progress   Progress
}

func (this Checkpoint) Fresh() bool { return this.Advance == 0 }

type CheckpointCapabilities struct {
	Transactions Support
	Persistence  Support
}

type Checkpoints interface {
	Capabilities() CheckpointCapabilities
	Backing() Backing
	Transaction(ctx context.Context) (Authority, error)

	Load(ctx context.Context, projection string) (Checkpoint, error)
	Save(ctx context.Context, checkpoint Checkpoint) error
	Forget(ctx context.Context, projection string) error
	Close() error
}

func Track(checkpoints Checkpoints, projection string) (*Tracker, error)

// What it holds is a window rather than one number: advance is the fence — a
// save presents one above it — and floor is the lowest advance the row can still
// be at. They part when a save leaves the row in one of two states: unresolved,
// where the store did not say whether it wrote, and staged, where it wrote
// inside a transaction this tracker does not commit.
type Tracker struct {
	checkpoints Checkpoints
	projection  string
	floor       uint64
	advance     uint64
	loaded      bool
	unresolved  bool
}

func (this *Tracker) Projection() string
func (this *Tracker) Capabilities() CheckpointCapabilities
func (this *Tracker) Backing() Backing
func (this *Tracker) Transaction(ctx context.Context) (Authority, error)
func (this *Tracker) Load(ctx context.Context) (Checkpoint, error)
func (this *Tracker) Save(ctx context.Context, cursor Cursor, progress Progress) (uint64, error)
func (this *Tracker) Forget(ctx context.Context) error
```

**`Save` answers the advance it presented — changed after S3's review, and it is
the other half of "the fence is the door's".** A caller that re-derived it would
hold a second copy of one fence, and the two part the moment a pass issues two
saves; the settlement of D10b is `found.Advance` compared against that number, so
it would then be decided on an advance no store ever saw. The number is answered
on a refusal too, because a refused save is exactly when it is needed. Zero is the
answer to a refusal made *before* an advance was presented at all — the four
pre-flight refusals below.

**Why each is exported.** `Checkpoint`, `Progress`, `CheckpointCapabilities` and
`Checkpoints` are the contract two shipped stores implement and a third is proved
against — a third-party store cannot be written against an unexported interface.
`Track` and `*Tracker` are exported because they are the *only* way to reach a
`Checkpoints`: if the door were unexported, `event/projection` would be the only
consumer that could have one, and the second consumer would call the store raw
(§INV-082). `Fresh` is exported because `Advance == 0` is a rule a caller should
not have to know.

**The one clause this plan adds to what [SPEC] §3.1 asks of an implementer.**
`Save` **refuses an empty cursor** and answers `event.Failure(event.Refused, …)`
for it, and the row it would have written does not move. It is in the store and
not only in the door because `RunCheckpoints` runs against the raw `Checkpoints`
(§UC-123), so a store that persisted one would be certified by a suite that never
asked; and because the door is not the only thing that will ever call a
`Checkpoints`. D12 is the argument, the `bounds` section is where it is
certified, and `octet_length(cursor) >= 1` is where `eventpg` says it a second
time.

**What `Track` refuses.** A nil `Checkpoints` (through `nilByAnyRoute`, not a bare
`== nil`) → `ErrWrongStore`: the wiring class, "assembled from values that do not
belong together". A `projection` the kernel's identifier rule refuses
(`checkName`, ≤ `MaxNameBytes`) → `ErrDeclaration`: it is the class of a
programmer's declaration mistake and `TryDeclare` already returns it from the
`Try*` half of the same idiom. `projection.New` wraps whichever it gets in
`ErrSpec`.

**What `Tracker.Load` re-checks, in this order, each `ErrWrongStore`:**

1. the store's own error, mapped at the **read door** (D10a);
2. **absence is total** — when `Advance == 0`, `Cursor` must be empty,
   `Projection` must be empty and `Progress.zero()` must hold. `Progress` is
   compared field by field and never with `==`: `time.Time`'s equality carries a
   monotonic reading and a `*Location`, so a store answering a zero instant in
   another location would be refused for a difference that is not one;
3. **presence is total in the one field that resumes** — when `Advance > 0`,
   `Cursor` must be non-empty. The empty cursor **is** the origin of the log in
   both shipped stores and is a refusal in neither, so a row at a non-zero
   advance carrying one is a readable checkpoint that restarts the projection at
   the beginning of the log against a live read model (D12, §UC-101's Must-not).
   Nothing but this arm fires on it: not the store, not `event.Read`, not the
   reader, not the fence;
4. **the answer is about the name that was asked for** — when `Advance > 0`,
   `Checkpoint.Projection == this.projection`. This is the check the whole door
   exists for (§UC-129);
5. `len(Cursor) <= MaxCursorBytes`;
6. **the answer is inside the window this tracker has already established** —
   `floor <= Advance <= advance` (`advance + 1` while a save is unresolved), and
   a tracker that has never loaded has established nothing and takes whatever the
   store holds, because a process that restarts is how a projection ordinarily
   begins. This one is about the *tracker* rather than the answer's shape, which
   is why it runs after the other four: they name a defect an operator reads off
   the row, and it names a row that moved while a tracker was live. **Below the
   floor** is the row retired, reset or restored from a dump — the next save
   lands at advance 1 over a live read model, a fresh row and a walk that resumes
   from the origin (§UC-102's Must-not, §UC-101's). **Above the fence** is a
   second writer at the same name, and taking its advance is what makes two
   processes take turns over one checkpoint, each re-reading the other's number
   and saving one above it (§INV-069: "never retried at a re-read advance").
   Neither is reached by any other refusal — the store's own fence admits both,
   because both present exactly one above what the row holds.

**Why a window and not a floor.** A save the tracker watched a store commit for
itself cannot go back, so the floor rises with it and a later `Load` answering
less is refused. A save the tracker did *not* commit can: an unresolved one may
not have landed, and one staged in the caller's transaction may still roll back
under it, and both are exactly the reload D10b prescribes. The tracker tells the
two apart by asking the store the one question `Transaction` exists to answer —
`Tracker.autocommits`, which is `Transaction(ctx)` after a save that returned nil
and nothing else. A valid authority means the
row is staged in a transaction this tracker does not commit, so the floor stays
where it was. It issues no statement (§3.1), and guessing it wrong in the
cautious direction only widens the window.

**Why presence is total in the cursor and not in the other three.** The cursor is
the resume authority; `Progress` is an observation nobody resumes from (§INV-077)
and `Highest` at a non-zero advance is a number the door has no independent
account of. A door that refused a store for a `Progress` it did not like would be
refusing correct stores over a field it does not read, which is the failure
mode`Progress{} == held.Progress` would already have shipped.

On success `this.advance, this.loaded = held.Advance, true`. On any refusal the
tracker's fence does not move and **nothing is written over the row** (§INV-068).

**What `Tracker.Save` does.** Refuses, before issuing, in this order:

- a save before a load (`!this.loaded`) → `ErrWrongStore`, with a message that
  says a save before a load is a loop assembled wrong;
- a save over one the store never confirmed (`this.unresolved`) → `ErrWrongStore`.
  Either advance such a save could present is a guess about a row nobody read,
  and one `Load` settles it: this is §UC-104's "must not re-save blindly" held at
  the door rather than re-derived by every consumer of `Checkpoints`;
- an **empty cursor** → `ErrWrongStore`, with a message that says the empty
  cursor is the origin and a checkpoint at the origin is not a resume point. It
  can only arrive from a caller — `Reader.checkPage` refuses a log that mints one
  beside a non-empty page, and a page is what a save follows — so it is the same
  class as the refusal above it, and D12 records why it is not the class of the
  one below;
- a cursor over `MaxCursorBytes` → `ErrTooLarge`, because the checkpoint store
  has a column of exactly that width and the alternative is a constraint
  violation the store cannot classify. (Backlog `## P3` §34 asks whether
  `ErrTooLarge` is the right class for a *store's* over-long mint; it is `[low]`
  and stays untouched.)

Otherwise it builds `Checkpoint{Projection: this.projection, Cursor: cursor,
Advance: this.advance + 1, Progress: progress}`, calls `Save`, maps at the
**append door** and answers that advance beside the mapped error.
`Checkpoint.Advance` is therefore a field the *store* reads, no caller computes
and every caller can read back (§INV-069). What the answer moves:

| The save answered | `advance` | `floor` | `unresolved` |
|---|---|---|---|
| nil, and `Transaction(ctx)` answers no authority | the presented advance | the presented advance | — |
| nil, and `Transaction(ctx)` answers a valid one (or errors) | the presented advance | unchanged — the caller's commit decides | — |
| `ErrUncertain`, `context.Canceled`, `context.DeadlineExceeded` | unchanged | unchanged | set: the ceiling is one higher and the next save is refused until a `Load` |
| anything else — `ErrConflict`, `ErrBackend`, `ErrClosed`, `ErrRefused`, `ErrCursor` | unchanged | unchanged | — |

The fourth row trusts the outcome vocabulary, which is the point of having one
(§INV-052): a store that says `NotWritten` and wrote is caught at the next
`Load`, by the window, rather than silently believed.

**What `Tracker.Forget` does not do.** It does not reset the fence — and neither
does the `Load` that follows it, which is the half a comment used to assert and
nothing held. A running projection that forgets its own name finds no row at its
next advance, is refused `ErrConflict` and halts; one that re-reads first is
refused the absence at the door, because zero is below its floor. Both are the
correct and visible outcome (§UC-102), and either of them re-seating the fence
would turn it into a silent restart at the origin.

### `event/bounds.go` and `event/reader.go` — the ceiling and where it is checked  *(kernel, modified)*

```go
const MaxCursorBytes = 4096   // the ceiling on what a Log or a Checkpoints may mint
```

Not configurable, unlike `MaxPayload` and `MaxKey`: a bound a deployment could
lower is one that refuses a cursor its own store mints. `eventpg` mints 58 bytes
and `eventmemory` about 30.

```go
func (this *Reader) Next(ctx context.Context) (bool, error) {
	page, cursor, err := this.log.ReadAll(ctx, this.cursor)
	if err != nil {
		return false, refuseRead(err)
	}
	if err := this.checkPage(page, cursor); err != nil {
		return false, err
	}
	this.events, this.cursor = page, cursor
	return len(page) > 0, nil
}

func (this *Reader) checkPage(page []Envelope, cursor Cursor) error
```

`checkPage` gains **two** arms, both in the same shape as the over-long page and
both `ErrBackend`, and the cursor is bounded above **and below**:

- a cursor over `MaxCursorBytes`, naming the length and the ceiling and never the
  cursor;
- an **empty cursor beside a non-empty page**, naming the page's length and the
  fact that the empty cursor is the origin. Without this arm a `Log` that minted
  `""` beside a real page would make the reader read the head of the log forever
  while the projection's checkpoint advanced once a pass — no error, no halt, no
  observer transition, and a read model written twice (D12).

An empty page answered with an empty cursor is **not** refused: that is a fresh
log read from the origin, and nothing was delivered to resume past. The reader's
own cursor is not advanced on a refusal — the assignment already happens after
the check, and a test pins it.

### `event/fact.go` — the typed reading seam  *(kernel, modified)*

```go
func (this *Fact[S, ID, E]) Family() string { return this.aggregate.Family() }

// The recorded bytes through this fact's own reader chain and every declared
// upcaster, in the current reader type. The four history-class refusals a replay
// produces, and no other. The value it answers may alias the envelope's payload,
// exactly as a replay's does.
func (this *Fact[S, ID, E]) Read(envelope Envelope) (E, error)
```

`Read` runs the same store-data check `Repo.apply` runs, before it renders
anything, and in this order: `checkName(envelope.Type)` → `ErrUnknownType`;
`envelope.Stream.Family != this.aggregate.family` → `ErrUnknownType`;
`envelope.Type != this.name` → `ErrUnknownType`; the payload over
`MaxPayloadBytes` → `ErrPayload`; the revision outside `1..len(chain.links)` →
`ErrRevision`; then `chain.links[revision-1].decode(payload)`, which carries
`ErrPayload` and `ErrUpcast` from the chain.

**The payload bound is the kernel's ceiling and not a store's `MaxPayload`, and
that is stated rather than left to be discovered.** A `Fact` holds no store's
limits — they arrive at `Bind`, and `Read` is reached from a projection that
bound none. The store's own read door already refuses a row over *its*
`MaxPayload` (`eventpg.promised`), so the weaker bound here is the second of two
rather than the only one. `Family()` seals the aggregate, exactly as
`Aggregate.Family()` does.

### `event/eventmemory/checkpoints.go` — the checkpoint store with no database  *(new)*

```go
package eventmemory

type CheckpointSpec struct{ Log *Log }

type Checkpoints struct {
	log    *Log
	closed atomic.Bool
}

var _ event.Checkpoints = (*Checkpoints)(nil)

func NewCheckpoints(spec CheckpointSpec) (*Checkpoints, error)
func (this *Checkpoints) Begin(ctx context.Context) (*Tx, error)
```

`Capabilities` is `{Transactions: Supported, Persistence: Unsupported}`.
`Backing()` is the log's own, so two values over one container are one checkpoint
store. It joins the ambient `*eventmemory.Tx` for its log: a `Save` inside a
transaction is staged and published at `Commit`, discarded at `Rollback`, which is
what lets `event/projection` prove the `InUnit` path with no database at all.

**The rows live on the `*Log`, not on the `*Checkpoints` value** — the struct
above holds no map, and that is the change from the plan's first draft — for the
reason the envelopes do: two store values over one log are one store, and a
restart resumes through a value that did not exist when the row was written.
`NewCheckpoints` performs no I/O and starts nothing.

**What the ambient join costs, in three existing files, is D13** and is on
[the path list](#the-complete-set-of-paths-phase-3-may-add-to-the-manifest) with a
sentence each: `Tx` gains a second staging area that `Rollback`'s position
arithmetic does not read; `ambient` lifts to the `*Log`; the rows go beside the
envelopes. `Begin` mirrors `Store.Begin` so a checkpoint store needs no `*Store`
to open a unit, and `WithTransaction` is untouched, so a unit begun through either
peer is found by both. `Save` refuses an empty cursor before it stages anything,
like every `Checkpoints`.

### `event/eventpg/checkpoints.go` and schema version 2  *(satellite)*

```go
package eventpg

const SchemaVersion = 2   // was 1

type CheckpointSpec struct {
	DB     *sql.DB
	Source crud.Source

	Schema           Schema
	SchemaManagement SchemaManagement
}

type Checkpoints struct { /* db, source, schema, management, backing, closed, state */ }

var _ event.Checkpoints = (*Checkpoints)(nil)

func NewCheckpoints(spec CheckpointSpec) (*Checkpoints, error)

func (this *Checkpoints) Prepare(ctx context.Context) error
func (this *Checkpoints) Check(ctx context.Context) error
```

The table, authored in the form PostgreSQL keeps it (§What was measured, 2):

```
<schema>.checkpoints   projection  text        NOT NULL PRIMARY KEY
                                   CHECK (octet_length(projection) >= 1 AND octet_length(projection) <= 128)
                       cursor      bytea       NOT NULL
                                   CHECK (octet_length(cursor) >= 1 AND octet_length(cursor) <= 4096)
                       advance     bigint      NOT NULL CHECK (advance > 0)
                       highest     bigint      NOT NULL CHECK (highest >= 0)
                       applied     bigint      NOT NULL CHECK (applied >= 0)
                       quarantined bigint      NOT NULL CHECK (quarantined >= 0)
                       updated_at  timestamptz NOT NULL
```

**Two departures from [SPEC] §3.1's table, both decided and both measured.**
`cursor` is `bytea` and not `text`, because a cursor is opaque bytes and `text`
cannot hold them (D11) — the column is `byteaType` in the expectation model, the
same constant `payload` already uses, and the store binds `[]byte`. And the
cursor is bounded **below** as well as above, in the `bytesBetween` shape the
other text columns already use, because the empty cursor is the origin (D12);
without the `>= 1` the empty row inserts, measured. Both move the v2 fingerprint
and the three goldens, which is what D1 already provides for.

The save is the one statement measured above, with `updated_at` bound as `$7`
rather than taken from `statement_timestamp()`. **That is a deliberate departure
from the append and it removes a defect rather than adding one:** the append's
instant is a property of the write and belongs to the database, while
`Progress.At` is a property of the *projection's* progress and must round-trip —
a column stamped by the database and a field set by the process are two clocks in
one value, which is exactly what backlog `## P3` §5 reports. One clock, one
value, and `Load` answers back what `Save` was given. `time.Time` is one of the six
types the driver rule admits.

`Prepare` reuses the store's own `Migrate` under `ManageSchema` and its own
`Verify` after: the advisory lock is **per schema**, so a `Store` and a
`Checkpoints` both managing one schema in one process serialise on it and the
second finds the list idempotent and the assertion satisfied. That is the whole of
what the in-process pair needs; the coverage half stays backlog `## P3` §8.

`Checkpoints` reuses `onExecutor`, `bound`, `opened`, `outcomeOf` and `causeOf`
unchanged — it opens, commits and rolls back nothing (§INV-051), and issues every
statement on a `*sql.Conn` it checked out or on the caller's `*sql.Tx`, never on
the pool (§INV-053). `RowsAffected() == 0` is `event.Conflict`; any count other
than 0 or 1 is `Unclassified`. An empty cursor is `event.Refused` **before the
statement is issued**, so the constraint is the second line and not the first: a
check-constraint violation reaches `outcomeOf` with the backend alive, is
classified `NotWritten`, becomes `ErrBackend`, and `pass.go` retries `ErrBackend`
on a save without limit — a refusal the store owes an answer to must not arrive
as a retryable one.

### `event/projection` — nine files  *(kernel, new package, root module)*

**`errors.go`**

```go
var (
	ErrSpec      = errors.New("projection: this projection cannot be assembled from this spec")
	ErrHalted    = errors.New("projection: this projection stopped advancing and is not applying events")
	ErrOvertaken = errors.New("projection: a second live writer at this projection name holds the checkpoint")
	ErrUnrouted  = errors.New("projection: this envelope's type is of a family this router routes and no route claims it")
)
```

**Changed in S3 — three became four ([[D-133]]): a lost fence no longer halts, so
the contention a deployment must act on needs a sentinel `errors.Is` reaches
rather than a message a consumer would have to match by string.** None crosses a
store seam: construction, lifecycle, contention and routing. `ErrHalted` is never
what `Run` returns — `Run` returns `ctx.Err()` — it is what `Ready` reports and
what `State.Err` carries; `ErrOvertaken` reaches `State.Err` on every lost fence
and `Ready` once the losing streak outlasts `Tolerate`.

**`spec.go`** — `Spec` exactly as [SPEC] §9.2 fixes it, plus `Advance`, `Failure`,
`Backoff`, `Unchecked` and `New`.

**Two fields carry an obligation the type cannot, and both were written down
after S3's review because neither is discoverable from the signature.**

- **`Unit` runs the work exactly once.** A unit that retries its transaction —
  the ordinary 40001 loop [[D-126]] and [[D-040]] tell a caller to own — answers
  the failure instead, and the projection re-delivers the page after a backoff. A
  second run of the work inside one pass is refused before it writes anything,
  because one pass presents one advance and a second save would present one above
  a number the row may never have taken. The refusal is not a halt: the
  settlement reads the row against the first run's advance and finds either that
  advance, which lands, or the one below it, which re-delivers.
- **`Wake` is a hint and never the delivery mechanism** (R13/R16 of the reference
  adjudication). `Idle` polls whether or not anything sends, so a producer that
  stops sending costs latency and nothing else. **Closing the channel says there
  will be no more hints**: the loop stops waiting on it and follows on `Idle`
  alone, because a closed channel is permanently ready and receiving from one is
  an unbounded read of the log — measured at 112 000 reads in 200 ms before the
  guard existed, which against `eventpg` is that many statements per projection.

`New`'s refusal set is enumerated here because
the plan may not leave it to be discovered (backlog `## P3` §7 stays open for the
doc and case work; this is what the code does):

| Refused at `New` | Why |
|---|---|
| a `Name` `checkName` refuses, or empty | it is the checkpoint row's primary key and the runner's name |
| a nil `Log`, `Handler` or `Checkpoints` | nothing to read, apply or record |
| a `Log` that is also an `event.Store` | §UC-115, naming `event.ReadOnly` |
| `Advance` outside the enum (`Valid()` is false) | an unknown mode is not `AfterApply` |
| `InUnit` with a nil `Unit` | §UC-107(a) |
| `InUnit` with `Checkpoints.Capabilities().Transactions != Supported` | §UC-107(b) |
| `InUnit` with `Destination == nil` | §UC-107(c) — `Unchecked` is the way to say it cannot be checked, and it is not reachable by leaving a field zero. **S3 spells it `var Unchecked = unchecked{}` rather than `var Unchecked any = unchecked{}`**: a package-level `any` is a value anything may be written through, and `TestNoPackageLevelStateIsEverMutated` reads it as state every copy shares |
| a `Unit` beside `AfterApply` | a unit the framework opens nothing inside is a wiring the caller did not get. **The plan keeps [SPEC] §9.2's refusal and records the argument backlog `## P3` §7 asks for:** the legitimate shape it seems to block — a handler whose writes want one transaction while the checkpoint stays outside it — is written by the *handler* opening its own transaction, which needs no framework field |
| `OnPermanentFailure: Quarantine` with a nil `Quarantine` | §UC-110's Must-not — a policy with nowhere to record is a skip with extra words |
| `Backoff.First < 0`, `Backoff.Max < 0`, or `First > Max` when both are set | a backoff that shrinks is not one |
| a negative `Idle`, `Attempts` or `Tolerate` | |

`New` performs no I/O, starts nothing, reads no environment. It calls
`event.Track(spec.Checkpoints, spec.Name)` and `event.Read` is **not** called here
— the first read of the checkpoint and the first `Read` both happen on the first
pass of `Run` (§INV-013, §INV-074).

Defaults, each the one that promises less: `Advance` → `AfterApply`; `Idle` → 1 s;
`Backoff` → `{250ms, 30s}`, doubling, **without jitter** (a projection is a
singleton per name, so there is no herd); `Attempts` → 10, bounding handler
failures only; `Tolerate` → 1 min; `Classifier` → `Classify`; `Ticks` →
`runtime.SystemTicks`; `OnPermanentFailure` → `Halt`. `Destination` has no default
under `InUnit`.

**`page.go`**

```go
type Batch struct {
	Projection string
	Envelopes  []event.Envelope
	Attempt    int
}

type Handler interface{ Apply(ctx context.Context, batch Batch) error }
type HandlerFunc func(ctx context.Context, batch Batch) error
```

And the one function that earns a comment in this file:

```go
// Each attempt is handed its own page. INV-021 grants the handler the slice, its
// capacity and every payload in it — keep it, read it from any goroutine, write
// into it — and this projection is the first sender in the enumeration that
// re-reads what it handed over, because a retry re-applies the page it holds. So
// the sender pays: a fresh slice and a fresh copy of every payload per attempt,
// the first included, with the log's own page kept untouched beside them.
func copyOf(page []event.Envelope) []event.Envelope
```

The price is one allocation and one copy of the page's payload bytes per attempt.
Against the measured 1.4 µs per event, a 256-envelope page of 120-byte payloads is
a single ~30 KB copy beside ~360 µs of work.

**`classify.go`**

```go
type Verdict uint8
const (Retryable Verdict = iota; Permanent)

type Classifier func(err error) Verdict

func Classify(err error) Verdict   // history class + ErrUnrouted -> Permanent, else Retryable

type Failure uint8
const (Halt Failure = iota; Quarantine)

type Quarantined struct {
	Projection string
	Envelope   event.Envelope
	Cause      error
}

type Quarantines interface {
	Quarantine(ctx context.Context, quarantined Quarantined) error
}
```

**Changed in S3 — the four arguments became one value.** A call that returns an
error and takes a bare `string` is a *message* to
`TestNoRefusalRendersAnIdentityAPayloadAKeyOrACursor`, and the envelope beside it
then renders a version into what that walk reads as a refusal. The walk is kernel
and frozen; the seam is this section's, so the seam moved.

`Classify` compares with `errors.Is` against `event.ErrUnknownType`,
`event.ErrRevision`, `event.ErrPayload`, `event.ErrUpcast` and
`projection.ErrUnrouted`, never by string.

**`state.go`** — `Phase`, `State`, `Observer`, `ObserverFunc`, and the publish
helper that **recovers a panicking observer**, matching `runtime.observing`
verbatim. A blocking observer blocks the loop, and the module page says so.

**`router.go`**

```go
type Foreign uint8
const (SkipForeign Foreign = iota; RefuseForeign)

type Router struct {
	foreign  Foreign
	routes   map[routeKey]func(context.Context, event.Envelope) error
	ignored  map[routeKey]struct{}
	families map[string]struct{}
	sealed   atomic.Bool
	skipped  atomic.Uint64
}

type routeKey struct{ family, name string }

var _ Handler = (*Router)(nil)

func NewRouter(foreign Foreign) *Router
func On[S, ID, E any](router *Router, fact *event.Fact[S, ID, E], apply func(context.Context, E, event.Envelope) error)
func TryOn[S, ID, E any](router *Router, fact *event.Fact[S, ID, E], apply func(context.Context, E, event.Envelope) error) error
func Ignore(router *Router, family string, types ...string)
func TryIgnore(router *Router, family string, types ...string) error
func (this *Router) Apply(ctx context.Context, batch Batch) error
func (this *Router) Skipped() uint64
```

`TryOn` closes over `fact.Read` and the caller's typed applier, and records
`fact.Family()` as covered. `Apply` seals on entry (D7), then per envelope:

```
route present            -> call it; an error is the handler's and reaches Classifier
ignored                  -> nothing, and not an error
family covered, no route -> ErrUnrouted, naming the family and the type and nothing else
family not covered       -> SkipForeign: skipped++    RefuseForeign: ErrUnrouted
```

`ErrUnrouted` is one sentinel for both refusals, so a consumer reads one refusal
rather than two, and `Classify` calls it `Permanent` by its own rule rather than
by membership of a history class it is not in.

**`projection.go`** — `Projection`, `New`'s product, and the loop.

```go
type Projection struct { /* spec, tracker, state, drain, done, halted, ... */ }

var _ runtime.Runner = (*Projection)(nil)

func (this *Projection) Name() string { return "vv.event.projection." + this.spec.Name }
func (this *Projection) Declaration() runtime.Declaration // {Singleton, Durable}
func (this *Projection) Run(ctx context.Context) error    // blocks; returns ctx.Err()
func (this *Projection) Drain(ctx context.Context) error
func (this *Projection) Ready(ctx context.Context) error
func (this *Projection) State() State
```

The loop, and it is the one function in the package that earns a written-out
argument, because six of its properties are load-bearing and none is visible from
its shape:

```
resume   Tracker.Load once, on the first pass    -> a cursor, or the origin
         event.Read(log, cursor)                 -> the walk, held for the loop's life

loop     halted?          -> wait on ctx and the drain signal ALONE: no read, no
                             save, no handler call, no ticker, ever again
         drained?         -> wait on ctx alone
         read one page from the reader, OUTSIDE every unit of work
           empty          -> Following: keep the reader's cursor in memory, save
                             NOTHING, wait on the ticker / Wake / ctx, read again
           non-empty      -> Draining: apply, then advance
```

- **The read is outside every unit** (§INV-071). Not a style preference: an
  `eventpg` walk mints no settlement bound while a transaction of its backing is
  bound, so a walk inside the projection's own write transaction stops at the
  first burnt gap and stays there for the life of that transaction. A projection
  that opened the unit first would deadlock its own progress on the first
  rolled-back append in the log, in production, with every test over a gapless
  log green.
- **The framework opens no transaction** (§INV-027). `Spec.Unit` is the
  application's; `crud.InNewTx(ctx, source, work)` is the one-line spelling.
- **`InUnit` is checked per pass, inside the unit and before the handler runs**
  (§INV-070): `Tracker.Transaction(ctx)` must answer a valid authority, and —
  unless `Destination` is `Unchecked` — `crud.ExecutorFor(ctx, destination)` must
  find an executor `crud.IsTransaction` accepts. Either failing returns from the
  unit body, so the unit rolls back, and the projection halts without calling
  `Classifier` (D10c).
- **The checkpoint advances only for a page the projection finished with**
  (§INV-067). An empty page's cursor is kept in the reader and never saved, so an
  idle projection issues no writes at all.
- **Inside a unit the advance is claimed before the handler runs** — added in S3,
  [[D-133]]. The two writes commit together, so their order is the lock manager's
  business alone, and a fenced save takes the checkpoint row's lock: presenting it
  first is this framework's spelling of the reference's claim-before-read, and a
  second live instance of the name is refused there before it applies anything.
  Outside a unit the order is the delivery guarantee itself — handle first,
  advance last — and does not move. The isolation pass is the one applier that
  still saves last, because what it quarantined is what its own run discovered.
- **A retry re-applies the page it holds and does not re-read.** `Reader.Next`
  advances the reader, so the page and its cursor are captured together at the
  read and the retry loop runs over those two values, handing each attempt its own
  copy (`page.go`).
- **A halted projection keeps running and does exactly nothing.** It publishes the
  transition **once**, records the failure in `State`, and then waits. `Drain`
  returns at once because there is no pass in flight. `State()` and `Ready(ctx)`
  keep answering. `Run` returns `ctx.Err()` and never `ErrHalted`. **A halt is
  terminal for that value's life** — there is no `Resume`, `Retry` or `Clear`, and
  the exit is a new value in a new process after the operator has fixed what
  halted it.

`Drain(ctx)` sets the drain flag and then returns as soon as any of four things is
true: the loop acknowledges between passes, the projection is halted, `Run` has
returned, or `ctx` is done. **It also returns `nil` at once when no pass has ever
begun** — `Supervisor.Start` hands `Run` a goroutine and `Stop` can drain before
that goroutine reaches its first pass, and a `Drain` that waited out its deadline
there would report a runner that had done nothing wrong. It starts no goroutine:
one channel closed by the loop and one atomic are the whole mechanism.

`Ready(ctx)` answers the halt error when halted; the streak error when the
accumulated backoff of the current streak exceeds `Tolerate` (D4); nil otherwise.
It names no importance and no code ([[D-091]]).

**`pass.go`** — one pass, the two modes, the retry, the isolation pass and the
uncertainty resolution (D10b). This is where the failure table lives:

| What answered | Where | What the pass does |
|---|---|---|
| `context.Canceled` / `DeadlineExceeded` | anywhere | return from `Run` |
| `ErrBackend` | read or save | retryable, without limit; the streak feeds `Ready` |
| `ErrClosed`, `ErrRefused` | read or save | halt — retrying a closed store is a busy loop that never clears |
| `ErrCursor` | read | halt, naming the projection; **never restart from the origin** |
| `ErrWrongStore` | `event.Read` at start-up, or `Tracker.Load` | halt; terminal wiring refusal |
| `ErrConflict` | save | the bounded resolution of D10b, once — **changed in S3 ([[D-133]]): a lost fence takes the row the winner left rather than halting, and only an absent row or one behind the fence halts** |
| `ErrUncertain` | save | the bounded resolution of D10b, once |
| the handler's own error | `Apply` | `Classifier` decides |
| a handler panic | `Apply` | recovered, reported permanent, the value rendered by the observer and never by a line of the framework's |
| `Retryable`, `Attempts` exhausted | `Apply` | becomes permanent |
| `Permanent` | `Apply` | `Halt`, or the isolation pass when a sink is supplied |
| anything else — `ErrTooLarge` from the door, a sentinel this table does not name | read or save | **halt**, naming the door and the sentinel. The table is total by this row rather than by hope: a pass that cannot classify its own failure must not fall through to the retryable arm, which is how a structurally impossible write becomes an infinite loop |

The isolation pass re-delivers the failed page **one envelope to a `Batch`, in
position order**: an envelope that applies is applied; one the classifier calls
permanent goes to the sink with its cause and is passed; a **retryable** failure
ends the isolation pass and returns the whole page to `Retrying` under the same
`Attempts` budget, because a database that went away is not a corrupt payload.
`Attempt` keeps rising across it. When every envelope has applied or been
quarantined, the checkpoint advances over the page **once**, carrying
`Progress.Quarantined` raised by what the sink took. Under `InUnit` the whole
isolation pass is one unit, the sink included (D5).

`Progress` is built at the save and nowhere else:
`{Highest: page[len-1].Position, Applied: loaded.Applied + appliedThisPage,
Quarantined: loaded.Quarantined + quarantinedThisPage, At: time.Now()}`. **Both
counts are cumulative across restarts**, seeded from the checkpoint the first
`Load` answered, because the columns are persisted and a dashboard that resets on
every deploy is one nobody can read. `Applied` counts **envelopes**, not pages.
(Backlog `## P3` §5 stays open for the doc and case work; this is what the code
does, and the plan states it because a persisted column whose meaning is undefined
is a column two implementers fill differently.)

### `event/eventtest` — the conformance extension  *(kernel)*

```go
type CheckpointFactory struct {
	New     func(t *testing.T) event.Checkpoints
	Begin   func(t *testing.T, ctx context.Context, c event.Checkpoints) (context.Context, Tx) // required by Transactions
	Sibling func(t *testing.T, c event.Checkpoints) event.Checkpoints                          // required by Persistence
	Cursor  func(t *testing.T) event.Cursor                                                    // required, always
	Window  time.Duration
}

func RunCheckpoints(t *testing.T, factory CheckpointFactory)
```

Twelve sections, in this order: `binding`, `absence`, `round trip`, `fence`,
`forget`, `names`, `bounds`, `refusal classes`, `lifecycle`, `concurrency`,
`transactions` (needs `Transactions`), `durability` (needs `Persistence`).

The same three anti-vacuity rules as `Run`, sharing its machinery: a claimed
capability with no hook is **fatal before any section starts**; an unstated
capability is **fatal**; a run in which nothing was certified **fails**. The
`probe`, `verdict`, `word` and `sweep` machinery is reused unchanged — a second
verdict type would be a second account of one rule.

**`Sibling` is required when `Persistence` is claimed, and its absence is fatal
before any section runs.** That is [SPEC] §9.4's own sentence ("Required when the
store claims Persistence") and it is the first anti-vacuity rule — §UC-123's
Must-not is "**No capability may be claimed without its hook**". `missing()`
already implements exactly this for the store suite (`event/eventtest/suite.go:187`),
and `RunCheckpoints` walks its own two claims the same way and in the same words:

| Claim | Hook | Section it gates |
|---|---|---|
| `Transactions` | `Factory.Begin` | `transactions` |
| `Persistence` | `Factory.Sibling` | `durability` |

Either stated `Unstated` is fatal; either stated `Supported` with its hook absent
is fatal, before a section runs. `Cursor` is fatal when absent for the reason
`runIdentity`'s narrow key is fatal — it is the suite's own requirement rather
than the store's defect, and it is required by every section that saves because a
cursor literal the suite invented is nonsense to one store and a legal position
to the next.

**An earlier draft of this plan made `Sibling` optional and gating nothing, and
that was wrong in both directions**, which is why the rule is spelled out rather
than assumed. A third-party store claiming `Persistence` with no `Sibling` would
have got `durability: not certified` in a list of eleven `passed` lines and an
exit code of 0, where the store suite `t.Fatal`s before a section runs — "a store
that only the door catches is a store whose implementer never learns". And
`eventmemory.Checkpoints`, which declares `Persistence: Unsupported` and supplies
a working `Sibling`, would have had the section named **`durability` report
`passed`** for a store nothing of which survives its process — the opposite of
`report.go`'s contract that a section a store did not claim and a section it could
not demonstrate are reported alike and neither is a pass.

So **`durability` is gated on `Persistence == Supported`**: `eventpg.Checkpoints`
reports `passed`, `eventmemory.Checkpoints` reports `not certified` with its own
reason, and the memory store's cross-value behaviour is exercised where it
already was — by every section that takes a second value, which is what `Sibling`
is supplied for. The reason text says that, so a reader of the verdict is not
left thinking nothing was asked.

Backlog `## P3` §9 **stays open**, and its text records that the gate **moved
rather than went**: the entry's real question — that `Persistence` and
"two values over one backing" are not the same claim, and
`CheckpointCapabilities` carries only the first — is untouched by any of this,
and [SPEC] §9.4 fixes the two-field shape so the plan may not add the third.

### What is deliberately absent from the surface

| Not exported | Why |
|---|---|
| `Reset`, `SetCheckpoint`, `Rewind` | a rebuild is a second name; an API that clears a live checkpoint has a typo's blast radius and is indistinguishable afterwards from corruption |
| `Resume`, `Retry`, `Clear` on a halt | a halt is terminal for that value's life; the classifier said the failure was permanent |
| `WaitUntilCaughtUp`, `Await` | the sound predicate exists and the ergonomics around it do not; the application's own read-model column is exact |
| `Head`, `Tail`, `Lag` | there is no head; a lag in events is a subtraction of positions wearing a dashboard's clothes |
| a `Position` → `Cursor` function, anywhere | §INV-066, and a surface test asserts it |
| a parallel or sharded projector, a snapshot of any kind | no consumer; [the snapshot decision](#the-snapshot-decision-measured-first) |
| a retry, backoff or circuit-breaker option on either store | [[D-040]] |

---

## Sections

Statuses: `[ ]` not started · `[~]` partial (must carry `MISSING:`) · `[x]` done,
checkpoint executed · `[!]` blocked (must carry `BLOCKED BY:`).

**Ordering rule: no section leaves the tree red.** `go build ./...` and
`go vet ./...` pass after every one, and `make unit` stays green throughout because
every live test is behind `//go:build integration`.

**Every checkpoint names its tests and counts them before running them**, on the
phase-2 pattern: each `-run` clause is preceded by
`test "$(go test -list '<the same anchored pattern>' … | grep -c '^Test')" = <N>`,
patterns written `'^(A|B)$'` because `-run` and `-list` match unanchored. Every
counting arm over a tagged package is itself preceded by an arm proving the binary
lists at all (`FROSTGROVE_EVENTPG_TEST_DSN=x go test -tags=integration -list '.' … | grep -q '^Test'`),
because `TestMain` runs before `m.Run()` handles the flag.

**Every section that touches the kernel opens by recording its predecessor and
ends with the manifest fence.** The opening act, run **before the section's first
file is written**:

```sh
./scripts/checks.sh event-kernel-baseline && cp scripts/event_kernel.sha256 .git/event_kernel_before_s<N>
```

and the closing arm, written once here and referenced by each of S1, S2 and S3 as
`<the manifest fence, with that section's own allowed and required sets>`:

```sh
./scripts/checks.sh event-kernel-baseline \
&& ./scripts/checks.sh event-kernel \
&& ./scripts/checks.sh event-kernel-moved .git/event_kernel_before_s<N> \
     '<the section'"'"'s allowed set, anchored>' <the section's required paths>
```

It reads the **manifest's own diff against a predecessor that survives a commit**
— [why it is a command](#the-fence-is-a-command-and-not-a-shell-pipeline) — so it
says the same thing before `git add`, after it, and after `git commit`, and it
fails on an empty diff as loudly as on an unexpected path. No path outside
[the plan's list](#the-complete-set-of-paths-phase-3-may-add-to-the-manifest) may
appear, and one that does is an unplanned kernel edit **reported out loud rather
than absorbed**; every path the section promised must appear, and one that does
not is a section that did not deliver.

**Two things about running these arms, both of which cost time to find.** First,
some agent harnesses replace `grep` with a wrapper around `ugrep`, whose `-qv`
exit status is **inverted** relative to GNU grep's — measured here: `printf
'a\nb\n' | grep -qv '^a$'` answered 1 through the wrapper and 0 through
`/usr/bin/grep`. That is why the fence is a command matching with bash's own
`[[ =~ ]]` and why no checkpoint below turns on `grep -qv`; the `grep` the
checkpoints do use is `grep -c` inside `$( )` and `grep -q` without `-v`, neither
of which the wrapper inverts. An agent that cannot be sure of its own shell still
spells `/usr/bin/grep`. Second, `scripts/checks.sh` is `set -euo pipefail`, so an
arm added there fails the whole run on a `grep` that matches nothing; the existing
arms end `|| true` where that is intended, and a new one must decide which it is.

---

### S1 — the kernel additions, the door, and the baseline mechanism  `[x]`   *(no database · touches kernel)*

**Delivers** the checkpoint contract, the fenced door, the cursor ceiling and the
typed reading seam — every kernel addition phase 3 makes except the suite's — plus
the rewritten `check-event-kernel` arm and its self-test. Nothing implements
`Checkpoints` yet; `Track` is real and is proved against test doubles in
`event/checkpoint_test.go`, which is where the three defective stores of §UC-129
live.

**Files** `event/checkpoint.go` (new), `event/bounds.go`, `event/reader.go`,
`event/fact.go`; `scripts/checks.sh` (the comparison arm, the regeneration arm
and the fence), `scripts/checks_test.go` (ten cases — five for the comparison and
five for the fence), `scripts/vv`, `Makefile`, `scripts/event_kernel.sha256`
(new).

**Realises** `Progress`, `Checkpoint`, `Checkpoint.Fresh`,
`CheckpointCapabilities`, `Checkpoints`, `Track`, `Tracker` and its seven methods,
`MaxCursorBytes`, `checkPage`'s cursor arm, `Fact.Family`, `Fact.Read`.

**Covers** UC-129, UC-099 (the tracker half), UC-101 (the empty-cursor route),
UC-111 (`Fact.Read`'s half), INV-068 (D12's two doors), INV-069 (the door's
half), INV-076, INV-078, INV-082.

**Tests** — all untagged, in `event/checkpoint_test.go`, `event/tracker_test.go`,
`event/reader_test.go`, `event/fact_test.go` (new — the fact's tests live in
`fold_test.go`, `declaration_test.go` and `upcast_test.go` today, and `Read` gets
its own file so the manifest diff stays readable):

- `TestTheDoorRefusesAnAnswerAboutAnotherName` — §UC-129's three defective
  `Checkpoints`: one whose `Load` ignores its argument and answers another name's
  well-formed row hundreds of thousands of positions ahead; one answering
  `Advance == 0` beside a set cursor; one answering a cursor of
  `MaxCursorBytes + 1`. Each must be `ErrWrongStore`, **no read may be issued from
  the answered cursor and no save at all**, and the store's own row must be
  byte-identical afterwards. **Control:** the honest store resumes through the same
  door, so the check is discriminating rather than universal.
- `TestAbsenceIsTotalAndProgressIsComparedFieldByField` — a store answering
  `Advance == 0` with a zero `time.Time` in another `*Location` must be accepted,
  and one answering it with a non-zero `Applied` must be refused. Without this the
  obvious `Progress{} == held.Progress` ships and refuses a correct store.
- `TestPresenceIsTotalAndAnEmptyCursorIsNeverAResumePoint` — D12's door half, and
  the three cases it owes. A `Checkpoints` answering `{Advance: 7, Cursor: ""}`
  is `ErrWrongStore` at `Tracker.Load`, with **no read issued from it and nothing
  written over the row**; a `Save("")` after a legitimate load is refused
  `ErrWrongStore` **before the store is called at all**, asserted by a recording
  store whose call count stays zero; and the same tracker at a real cursor saves.
  **Control:** the honest pair — a non-empty cursor at `Advance > 0` loads, and
  the zero checkpoint at `Advance == 0` with an empty cursor is accepted, because
  absence is still the origin and this arm must not have swallowed it.
- `TestAFreshTrackerAnswersTheZeroCheckpointAndSavesAtAdvanceOne` — §UC-099's
  tracker half: `Load` over a store with no row answers `Fresh()`, the first
  `Save` presents advance 1, and the second presents 2 only after the first
  landed.
- `TestASaveBeforeALoadIsRefused` and `TestTheFenceIsTheDoorsAndNoCallerComputesIt`
  — the advance presented is always the loaded one plus one; a failed save does not
  move it; `Forget` does not reset it.
- `TestALoadNeverReSeatsAFenceThisTrackerEstablished` — item 6's window, both
  directions and both legitimate exceptions. A tracker that loads, saves five
  times and forgets its own name is refused `ErrWrongStore` at the next `Load`,
  and the save after it is the store's `ErrConflict` with **no row re-created**
  (§UC-102's Must-not). The loser of a fenced save that re-reads is refused the
  winner's advance, and its second save does not land (§INV-069). An unconfirmed
  save is settled by one `Load` **either way** — the row at the presented advance
  and the row at the one below are both admitted (D10b) — while a second save
  over it never reaches the store. A save staged in the caller's transaction may
  be rolled back under the tracker and re-loaded; the same sequence against a
  store that commits its own save is refused, which is what keeps the window from
  being always open or always shut. **Control:** a tracker that has established
  nothing takes a row at advance 900, a `Load` between two saves is admitted, and
  a fresh tracker over an empty store still answers `Fresh()` and still saves at
  advance 1.
- `TestTrackRefusesWhatItCannotHold` — a nil `Checkpoints` by every route
  (untyped nil, a typed nil pointer, a nil interface in a wrapper) → `ErrWrongStore`;
  a name over `MaxNameBytes`, a name with a control rune, a name with a bracket →
  `ErrDeclaration`. **Control:** a legal pair is accepted.
- `TestEachCheckpointDoorMapsItsUnclassifiedFailureItsOwnWay` — an unclassified
  error from `Load` is `ErrBackend` and from `Save` and `Forget` is `ErrUncertain`
  (D10a), and each of the seven `Outcome` values maps as it does at the log's doors.
- `TestAReaderRefusesACursorOverTheCeiling` — a `Log` decorator minting
  `MaxCursorBytes + 1` → `ErrBackend`; the reader's own cursor is unchanged; a
  cursor of exactly `MaxCursorBytes` is accepted. **Control:** the ordinary store's
  cursor passes.
- `TestAReaderRefusesAnEmptyCursorBesideANonEmptyPage` — D12's reader half: a
  `Log` decorator answering a real page with `""` → `ErrBackend`, and the
  reader's own cursor unchanged, so the second `Next` does not read the head
  again. **Controls, and the second is the one that would otherwise make this
  arm too strong:** the ordinary store passes; and an **empty page** answered
  with an empty cursor — a fresh log read from the origin — is accepted, because
  nothing was delivered to resume past.
- `TestFactReadDecodesThroughItsOwnChainAndRefusesTheFourHistoryClasses` — a
  declared upcaster runs; an envelope of another family, of another type name, at a
  revision the chain does not retain, with a payload the codec refuses, and with an
  upcaster that says no, each answering its own sentinel and **naming neither the
  key nor the payload**. **Control:** the same envelope through `Repo.Load` folds
  to the same value, so `Read` and the replay path are one decoder.
- `TestTheValueFactReadAnswersMayAliasThePayload` — D6, pinned so the ownership
  rule is a case rather than a sentence.
- `TestCheckEventKernelReportsADifferenceAndOtherwiseOk` — **five** cases now,
  and the plan says which is the control for which:

  | Case | Expected | Control for |
  |---|---|---|
  | a file under `event/` differs from the manifest | exit 1, **naming the file** | the comparison. A gutted `echo ok` fails here |
  | a **new** file under `event/` not in the manifest | exit 1, naming it | that the manifest is a set and not a whole-tree digest — the failure must name the file |
  | a file **removed** from `event/` | exit 1, naming it | the removal half. A walk that looked each recorded file up and stopped would pass; only a `diff` of two listings fails |
  | **only** `event/eventpg` differs | exit 0 | the pathspec. An arm comparing all of `event/` fails here — and this is the one case a gutted arm passes, which is why it is never run alone |
  | `scripts/event_kernel.sha256` absent | exit 1, naming the file and the regeneration command, **not** exit 0 | the refuse-when-it-cannot-run branch |

- `TestTheKernelFenceRefusesAMoveItWasNotToldAbout` — the fence's own five cases,
  and the first is the one the pipeline this replaces could not have:

  | Case | Expected | Control for |
  |---|---|---|
  | the predecessor and the manifest are **identical** | exit 1, saying a section that moved no kernel file did not deliver | the vacuity. The committed-diff spelling passed here, which is why the fence is a command |
  | a path in the moved set outside the allowed set | exit 1, **naming that path** | the unplanned kernel edit |
  | a **required** path absent from the moved set | exit 1, naming it | the section that did not deliver. Nothing before this arm could see it |
  | the predecessor file missing | exit 1, naming it and the command that records one, **not** exit 0 | refuse-when-it-cannot-ask |
  | every required path present, nothing else moved | exit 0, printing the moved set | the honest case. Run alone it is what a gutted arm also passes, which is why it never is |

- `TestTheEventKernelOfThisRepositoryIsWhereThisPhaseLeftIt` — the real arm against
  the real repository, replacing the phase-2 test of the same shape.

**Opening act.** S1 is the one section whose opening act cannot come first,
because the command that records a manifest is one of the things S1 builds. So
S1's order is fixed: write `checks.sh`'s three arms and their ten self-tests;
record the manifest of the **untouched** kernel and copy it aside; and only then
write `event/checkpoint.go` and the three kernel files it modifies.

```sh
./scripts/checks.sh event-kernel-baseline && cp scripts/event_kernel.sha256 .git/event_kernel_before_s1
```

The manifest recorded there is phase 3's zero point, and it is what makes S1's
own fence say something: run after the kernel files are written, the diff against
it is exactly the four plus their tests.

**Checkpoint**

```sh
cd <repo> \
&& go build ./... && go vet ./event/... ./scripts/... \
&& [ -z "$(gofmt -l event scripts)" ] \
&& test "$(go test -list '^(TestTheDoorRefusesAnAnswerAboutAnotherName|TestAbsenceIsTotalAndProgressIsComparedFieldByField|TestPresenceIsTotalAndAnEmptyCursorIsNeverAResumePoint|TestAFreshTrackerAnswersTheZeroCheckpointAndSavesAtAdvanceOne|TestASaveBeforeALoadIsRefused|TestTheFenceIsTheDoorsAndNoCallerComputesIt|TestALoadNeverReSeatsAFenceThisTrackerEstablished|TestTrackRefusesWhatItCannotHold|TestEachCheckpointDoorMapsItsUnclassifiedFailureItsOwnWay|TestAReaderRefusesACursorOverTheCeiling|TestAReaderRefusesAnEmptyCursorBesideANonEmptyPage|TestFactReadDecodesThroughItsOwnChainAndRefusesTheFourHistoryClasses|TestTheValueFactReadAnswersMayAliasThePayload)$' ./event/ | grep -c '^Test')" = 13 \
&& test "$(go test -list '^(TestCheckEventKernelReportsADifferenceAndOtherwiseOk|TestTheKernelFenceRefusesAMoveItWasNotToldAbout|TestTheEventKernelOfThisRepositoryIsWhereThisPhaseLeftIt)$' ./scripts/ | grep -c '^Test')" = 3 \
&& go test -race -count=1 ./event/... \
&& go test -race -count=1 -run '^(TestCheckEventKernelReportsADifferenceAndOtherwiseOk|TestTheKernelFenceRefusesAMoveItWasNotToldAbout|TestTheEventKernelOfThisRepositoryIsWhereThisPhaseLeftIt)$' ./scripts/ \
&& ./scripts/checks.sh event-kernel-baseline \
&& ./scripts/checks.sh event-kernel \
&& ./scripts/checks.sh event-kernel-moved .git/event_kernel_before_s1 \
     '^event/(checkpoint\.go|checkpoint_test\.go|tracker_test\.go|bounds\.go|bounds_test\.go|reader\.go|reader_test\.go|fact\.go|fact_test\.go)$' \
     event/checkpoint.go event/bounds.go event/reader.go event/fact.go
```

The last arm is [the manifest fence](#sections) with S1's own allowed set and its
four **required** paths: the three kernel files this section modifies
(`bounds.go`, `reader.go`, `fact.go`) and the one it adds (`checkpoint.go`) must
all appear in the moved set, and the four test files they need may.
`event/eventtest/` and `event/projection/` are deliberately **not** in the allowed
set — they belong to S2 and S3, so one of their files appearing in S1's manifest
diff means the sections were interleaved and the arm says so. The two count arms
are the phase's own standard applied to `./scripts/` as well as to `./event/`: a
section is reported against tests that exist, and `go test` cannot fail for one
nobody wrote.

**One ordering constraint the arms make, found by running them.**
`TestTheEventKernelOfThisRepositoryIsWhereThisPhaseLeftIt` runs *before*
`event-kernel-baseline` in the checkpoint above, so the manifest has to be
re-recorded as the **last act of writing code**, not as the first act of running
the checkpoint. That is the arm working — it refused the run in which two test
files had moved since the last baseline — and it is what "regenerated beside the
code" means in practice.

**Executed 2026-09-08, green — re-run after GAP-1's fix, with the window and its
`TestALoadNeverReSeatsAFenceThisTrackerEstablished` in the counted set (12 → 13).**
Real output of the last five arms:

```
ok  	github.com/frostgrove/vv/event	5.906s
ok  	github.com/frostgrove/vv/event/eventmemory	1.249s
ok  	github.com/frostgrove/vv/event/eventtest	2.527s
ok  	github.com/frostgrove/vv/scripts	1.171s
event-kernel-baseline: 108 files recorded in scripts/event_kernel.sha256
check-event-kernel: ok
the files under event/ this section moved:
  event/bounds.go
  event/checkpoint.go
  event/checkpoint_test.go
  event/fact.go
  event/fact_test.go
  event/reader.go
  event/reader_test.go
  event/tracker_test.go
event-kernel-moved: ok
PLAN S1 CHECKPOINT EXIT=0
```

`make unit`, `make vet`, `make check` (`check-event-kernel: ok` at the new
manifest) and `gofmt -l .` are all green, and the live `eventpg` suite was run
unchanged against PostgreSQL 17.9 — `ok github.com/frostgrove/vv/event/eventpg
78.728s` — because the reader's two new arms bound what a real store mints and a
green untagged suite cannot say that.

**Three departures from this section as written, each recorded rather than left
in a diff.**

1. **`TestPresenceIsTotalAndAnEmptyCursorIsNeverAResumePoint` gained a fourth
   subtest**, for `Tracker.Save`'s over-ceiling `ErrTooLarge` arm. The contract
   above names that arm and D12 argues for its class, and no test in the section
   list reached it: removing the arm left every S1 test green, measured.
2. **The fence's fifth self-test case removes a file as well as changing one.**
   With only a changed file it did not catch a moved set computed in one
   direction — manifest minus predecessor — which loses every removal, and the
   fence's own step 3 is written "in either direction". Measured: the
   one-directional spelling passed all five cases before the change and fails the
   fifth after it.
3. **`FL-037` and `docs/roadmaps/Roadmap.md` were corrected in the same change**,
   because both named `EVENT_KERNEL_BASELINE` and `check_event_kernel` and the
   git spelling they belong to. `TestEveryTestNameTheDocsCiteExists` caught the
   flow's stale test name; nothing would have caught the two prose paragraphs.
   The roadmap's phase-2 debt entry now records that both halves of backlog
   `## P2` §5 are closed — the arm runs from a tarball, and re-baselining is a
   command.

---

### S2 — the two checkpoint stores, schema version 2, and the conformance runner  `[x]`   *(live database · touches kernel)*

**Delivers** both `Checkpoints` implementations, the v2 schema and its migration,
`eventtest.RunCheckpoints` with twelve sections, the five-defect mutation harness,
and the `resumption` in-flight case that closes backlog `## P2` §59.

**Files** `event/eventmemory/checkpoints.go` (new) and the two files D13 names
that really move — `transaction.go` (the second staging area, the lifted
`ambient` and the two delegations) and `log.go` (the rows, beside the envelopes)
— plus `transaction_test.go`; `event/eventpg/checkpoints.go`, `schema.go` (the
version-parameterised model and the fourth table), `migration.go` (the guarded
`UPDATE`), `verify.go`/`catalog.go` (level 3 over the fourth table),
`census_integration_test.go` (the checkpoint census), `mutation_integration_test.go`
(the seventh mutation), `MIGRATIONS.md`, the three `testdata` goldens;
`event/eventtest/checkpoints.go`, `sections_checkpoints.go`,
`defects_checkpoints.go`, `inventory.go`, `sections_resumption.go`,
`sections_read.go`, `suite.go`, `probe.go`, `report.go`, `export_test.go`.

**Covers** UC-100, UC-102, UC-103, UC-122, UC-123, UC-124, UC-129 (the suite's
half), INV-069, INV-076, INV-078, INV-080, INV-082 (the suite's half).

**The `resumption` change, precisely.** `probe.lateWriter` currently opens a
transaction, appends inside it, appends beside it, and **commits before the walk
resumes** — so the section never walks the log while a lower position is
uncommitted, which is why a store answering `max(position)` passed all twenty
sections (measured, `## P2` §59). The case is restructured: the late writer's
transaction is **held open across the resumed walk**, the section asserts the walk
delivers nothing at or beyond the held position and that the persisted cursor has
not passed it, the transaction commits, and a walk resumed from that cursor
delivers it. A store with no transactions reports the clause `not certified` as it
does today.

**Tests**

- `event/eventmemory/checkpoints_test.go` — `TestTheCheckpointStoreSatisfiesTheContract`
  (`RunCheckpoints` against `eventmemory.Checkpoints`);
  `TestForgetTouchesOneNameAndAnAbsentOneIsNotARefusal` (§UC-122 — two names
  saved, one forgotten, the other's row byte-identical, and forgetting an absent
  name is nil rather than a refusal); and
  `TestTwoCheckpointValuesOverOneLogAreOneStore`, which is the store's own
  ownership case and the reason the rows live on the `*Log`.
- `event/eventmemory/transaction_test.go` — `TestARolledBackUnitStagedBothAndBurntOnlyTheAppends`,
  which is the case D13's shape change owes: a unit that staged **two appends and
  a checkpoint save** and then rolled back leaves the log's position advanced by
  **two** — the append count alone — and leaves the checkpoint row exactly where
  it was. **Control:** the same unit committed publishes both, and the position
  advances by two for the same reason. Against an implementation that put the
  save in `staged`, the rollback burns three and the next append lands a position
  ahead of where it should, which is an event the log will never hold and a gap
  no reader can explain.
- `event/eventtest/defects_checkpoints.go` + `defects_test.go` —
  `TestEveryCheckpointDefectIsReportedByItsOwnSection`, the mutation harness
  §UC-123 asks for, five defective stores, **each of which must be reported
  by a named section, and the section is named in the table so a defect that moves
  to another section is a change a person reads**:

  | Defect | Must be reported by | Self-falsifying because |
  |---|---|---|
  | ignores the fence — `Save` always lands | `fence` | the honest store's second save at a stale advance is `Conflict`, and this one's is nil |
  | `Load` answers a stale cursor — the one before the last `Save` | `round trip` | a cursor saved and loaded back must be the one that was saved |
  | reports absence for a row that exists | `absence` | `Fresh()` after a `Save` must be false |
  | **ignores the `projection` argument of `Load`** | `names` | two names saved, each loaded, each must answer its own row |
  | saves outside the caller's transaction | `transactions` | a save inside a rolled-back transaction must leave the row where it was |

  The fourth is the one `event.Track`'s door refuses in front of every consumer
  (§UC-129); **the suite runs against the raw `Checkpoints` rather than through the
  door**, precisely so an implementer is told about it instead of having the kernel
  quietly cover for them.

  **Two arms the round-1 remediation added carry no row of this table, and that
  is stated here rather than left to be noticed.** The removal racing a save
  lands in **`forget`** (`forgetRacingASave`) and the load inside a unit that
  staged a removal lands in **`transactions`** (`forgetsInAUnit`). Neither gets a
  sixth defect decorator: the first detects an interleaving, and every decorator
  that expresses one deterministically also trips `fence` sequentially, which
  would make this table's evidence about the wrong section; the second is a
  defect of the fixture store this table's controls are built from, so a
  decorator over it would be proving itself. Both are falsified directly instead
  — reverting each fix turns its section red and restoring it turns it green,
  measured — and `checkpointDefects` stays at five.
- `event/eventtest/suite_test.go` — the two admission self-tests §UC-123's
  Must-not owes, one each way:
  `TestACheckpointFactoryClaimingPersistenceWithNoSiblingFailsBeforeASectionRuns`
  (the run is `t.Fatal` before `binding`, in `missing()`'s own words, and **no
  section reported a verdict at all**) and
  `TestAStoreThatDoesNotPersistDeclinesDurabilityAndCertifiesTheOtherEleven`
  (`Persistence: Unsupported` with a `Sibling` supplied → `durability` is `not
  certified` with its reason pinned, the other eleven `passed`, and the run does
  not fail). Both read verdicts through `Certify`'s checkpoint twin rather than a
  subprocess, the way `event/eventtest`'s existing self-tests do. **Control:** the
  honest factory claiming `Persistence` *with* a `Sibling` certifies twelve —
  otherwise the first test passes against a suite that fatally refuses everything.
- `event/eventpg/checkpoints_integration_test.go` —
  `TestTheCheckpointStoreSatisfiesTheContract` against live `eventpg`;
  `TestEightSaversAtOneAdvanceLeaveOneWinnerAndSevenConflicts` (§10(3)) with the
  single-saver control; `TestASaveOfACursorAtExactlyTheCeilingLands`;
  `TestACursorOfArbitraryBytesRoundTripsThroughTheColumn` — D11's case: a cursor
  carrying a NUL and one carrying `0xff` save and load back **byte for byte**,
  read out of the database with `encode(cursor,'hex')` rather than from the
  code's account of itself. **Control:** the same two bytes through a `text`
  column are the two server errors measured above, so the case is about the
  column type and not about the store's own copying;
  `TestAnEmptyCursorIsRefusedByTheDoorAndByTheColumn` — D12's live half: `Save`
  refuses it as `event.Refused` before issuing anything, and an `INSERT` of `''`
  issued directly at the table violates `checkpoints_cursor_check`, so the second
  line exists as well as the first;
  `TestEveryFailureTheCheckpointStoresAnswerCarriesOneOfTheSeven` — §INV-076:
  every error reachable out of `eventmemory.Checkpoints` and `eventpg.Checkpoints`
  is nil, a bare context error, or an `event.Failure` carrying one of the seven
  outcomes, and the vocabulary is still twenty-four.
- `event/eventpg/migration_integration_test.go` extended —
  `TestAVersionOneSchemaWithRowsMigratesToVersionTwoAndReadsBackUnchanged`;
  `TestAVersionOneSchemaAtOtherBoundsIsNotRestamped` (D1's whole point: the
  `EVPG1` raise, the meta row unmoved, and **no `checkpoints` table left behind**);
  `TestAFreshDatabaseTakesTheSameVersionTwoList`;
  `TestTwoConcurrentPreparesTakeOneLockAtTheNewVersion`;
  `TestAStoreAndACheckpointsManagingOneSchemaInOneProcessSerialiseOnTheLock`.
- `event/eventpg/schema_integration_test.go` extended — level 3 over the fourth
  table on §UC-072's own matrix: the primary key dropped, a check constraint
  altered, **a trigger added** (D2), row-level security enabled — each refused,
  with the intact schema as the exempt control.
- `event/eventtest/run_test.go` + `event/eventpg/conformance_integration_test.go` —
  `TestTheNewestPositionCursorNowFailsResumption`: a decorator over the **real**
  `eventpg` store answering `max(position)` of the page as its cursor must now fail
  `resumption`, paired with the unmodified store, which must pass it. This is
  §INV-080 and it is the case that makes every other claim about checkpoints mean
  something.
- `event/eventpg/census_integration_test.go` **extended**, and this is the arm
  the plan's first draft claimed in prose and did not have. The file already
  carries the mechanism: `TestBothConformanceRunsCertifyEverySectionButTheOneThisStoreDeclines`
  reads the verdict lines out of a subprocess run and compares them with a written
  `census()`, and `TestADowngradedSectionIsCaughtByTheCensusAndByNothingElse`
  drives three withdrawn hooks, each of which must leave `go test` **green** and
  the census **red**. Both keep their names and both grow a checkpoint half:

  - `census()` gains `checkpointCensus()` — the twelve `RunCheckpoints` sections
    for live `eventpg`, all `passed`, with the reason text pinned wherever a
    verdict carries one, and a third subprocess run at
    `^TestTheCheckpointStoreSatisfiesTheContract$`. Without it, a run whose
    `transactions` section reported `not certified` — the section that certifies
    the `InUnit` half of the entire phase — prints `ok` and passes the section,
    and `RunCheckpoints`'s own third anti-vacuity rule does not fire because
    eleven others certified.
  - `downgrades()` gains two rows, and `withdrawals` moves from 3 to **5** (which
    `TestTheDefectInventoryIsTheSizeItSaysItIs`'s own falsification arm already
    pins in both directions). A hook cannot be withdrawn here without being fatal
    — that is the point of the `Sibling` gate — so the rows withdraw a *claim*:
    a `Checkpoints` wrapper answering `Persistence: Unsupported` downgrades
    `durability`, and one answering `Transactions: Unsupported` downgrades
    `transactions`. Each leaves the run green and the census red, which is the
    property the file's own comment names — caught "by the census and by nothing
    else".
  - The memory store's half — eleven `passed` and `durability: not certified` —
    is **not** here, because a subprocess of the `eventpg` binary cannot run a
    test of another package. It is
    `TestAStoreThatDoesNotPersistDeclinesDurabilityAndCertifiesTheOtherEleven`
    above, which reads the verdicts directly and pins the same reason text.

**Opening act**, before the first file of this section is written:

```sh
./scripts/checks.sh event-kernel-baseline && cp scripts/event_kernel.sha256 .git/event_kernel_before_s2
```

**Checkpoint (untagged)**

```sh
cd <repo> \
&& go build ./... && go vet ./event/... \
&& [ -z "$(gofmt -l event)" ] \
&& test "$(go test -list '^(TestTheCheckpointStoreSatisfiesTheContract|TestForgetTouchesOneNameAndAnAbsentOneIsNotARefusal|TestTwoCheckpointValuesOverOneLogAreOneStore|TestARolledBackUnitStagedBothAndBurntOnlyTheAppends|TestEveryCheckpointDefectIsReportedByItsOwnSection|TestACheckpointFactoryClaimingPersistenceWithNoSiblingFailsBeforeASectionRuns|TestAStoreThatDoesNotPersistDeclinesDurabilityAndCertifiesTheOtherEleven)$' ./event/eventmemory/ ./event/eventtest/ | grep -c '^Test')" = 7 \
&& go test -race -count=1 ./event/... \
&& ./scripts/checks.sh event-kernel-baseline \
&& ./scripts/checks.sh event-kernel \
&& ./scripts/checks.sh event-kernel-moved .git/event_kernel_before_s2 \
     '^event/(checkpoint\.go|eventmemory/(checkpoints(_test)?|transaction(_test)?|log|store)\.go|eventtest/(checkpoints|sections_checkpoints|defects_checkpoints|inventory|sections_resumption|sections_read|suite|probe|report)\.go|eventtest/[a-z_]+_test\.go)$' \
     event/eventmemory/checkpoints.go event/eventmemory/transaction.go event/eventmemory/log.go \
     event/eventtest/checkpoints.go event/eventtest/sections_checkpoints.go event/eventtest/defects_checkpoints.go event/eventtest/inventory.go event/eventtest/sections_resumption.go
```

The untagged count arm is the phase's standard applied to what this section
delivers with no database: seven names across `eventmemory` and `eventtest`, and
a missing one is a red line rather than a test `go test ./event/...` silently
never ran.

The last arm is [the manifest fence](#sections) with S2's allowed and required
sets. Nothing under `event/projection/` and none of S1's four files may appear:
S1's are already in the recorded manifest and must not move again, and a
projection file here is S3 leaking into S2. The three `eventmemory` files D13
names are **required** rather than merely allowed, because the `InUnit` proof S3
rests on does not exist without them. `suite.go`, `probe.go` and `report.go` are
allowed for the shared-mechanism reason argued above, and for no other — a
*feature* landing in one of them is what the "stop and report" rule is about.

**Executed 2026-09-08, green; re-executed after the round-1 remediation below,
green.** Real output of the checkpoint's last five arms, on the re-run:

```
ok  	github.com/frostgrove/vv/event/eventpg	96.821s
ok  	github.com/frostgrove/vv/event/eventpg	97.175s
ok  	github.com/frostgrove/vv/event	5.975s
ok  	github.com/frostgrove/vv/event/eventmemory	1.501s
ok  	github.com/frostgrove/vv/event/eventtest	4.081s
event-kernel-baseline: 114 files recorded in scripts/event_kernel.sha256
check-event-kernel: ok
PLAN S2 CHECKPOINT EXIT=0
```

The manifest still records 114 files — the remediation added no file to the
kernel and moved five of them.

and the manifest fence, with the corrected sets recorded below:

```
the files under event/ this section moved:
  event/checkpoint.go
  event/eventmemory/checkpoints.go
  event/eventmemory/checkpoints_test.go
  event/eventmemory/log.go
  event/eventmemory/transaction.go
  event/eventmemory/transaction_test.go
  event/eventtest/checkpoints.go
  event/eventtest/checkpoints_test.go
  event/eventtest/defects_checkpoints.go
  event/eventtest/export_test.go
  event/eventtest/inventory.go
  event/eventtest/probe.go
  event/eventtest/report.go
  event/eventtest/sections_checkpoints.go
  event/eventtest/sections_read.go
  event/eventtest/sections_resumption.go
  event/eventtest/suite.go
event-kernel-moved: ok
```

**The allowed set grew by exactly one path, and here is the sentence for it.**
`event/checkpoint.go` is S1's file and S2 did not touch it; the round-1
remediation does, to state on `event.Progress.At` the round-trip obligation
GAP-2 found nowhere in the contract — a comment, no symbol, no signature. It is
an accepted extension of this section's set rather than a relaxed check: the
pathspec names the one file, the required set is unchanged, and nothing under
`event/projection/` and none of S1's other three files appear. Seventeen paths
where the first run had sixteen.

`make unit`, `make vet`, `make tidy`, `make check` (`check-event-kernel: ok` at the
new manifest), `make api` (additions only — no line disappeared) and `gofmt -l .`
are all green, and the live suite ran twice in a row.

**Seven departures from this section as written, each recorded rather than left
in a diff.**

1. **`event/eventmemory/store.go` does not move, and it is not a required path.**
   D13 says `Store.ambient` and `Store.Transaction` become one-line delegations
   in `store.go`; both have always lived in `transaction.go`, so the delegations
   landed there and `store.go` is byte-identical. The fence's required set drops
   it; the allowed set keeps it, unused.
2. **`event/eventtest/sections_read.go` joins the allowed set**, for the same
   shared-mechanism reason `suite.go`, `probe.go` and `report.go` are on it:
   `from` is rewritten in terms of a new `reading`, which answers the events
   **and the cursor the walk reached**. The in-flight case needs that cursor and
   `from` cannot answer it; two walk bodies would be two accounts of one rule.
3. **`TestTheNewestPositionCursorNowFailsResumption` lives in `event/eventpg`
   only.** The plan named `event/eventtest/run_test.go` too, and the case cannot
   exist there: every memory fixture assigns positions at commit, so a cursor at
   the newest position of a page is not a defect in one. The eventpg half is a
   seventh row of the mutation harness (`in-flight-newest-position`,
   `mutations` 6 → 7) so the harness itself is the standing guard.
4. **The admission self-test is a subprocess.** The plan asked for `Certify`'s
   checkpoint twin; the two admission rules are a `t.Fatal` at the door, which
   cannot be observed in-process — a failure under a `T` fails the `T` that
   started it. `run_test.go`'s two admission cases are subprocesses for exactly
   this reason, and `TestACheckpointFactoryClaimingPersistenceWithNoSiblingFailsBeforeASectionRuns`
   follows them. It carries four cases rather than one (no `Sibling`, an unstated
   claim, no `Cursor`, a `Cursor` that repeats), and asserts **no section
   reported a verdict at all**. Its control — the honest factory certifying
   twelve — is in-process.
5. **`TestEveryFailureTheCheckpointStoresAnswerCarriesOneOfTheSeven` does not
   assert "the vocabulary is still twenty-four".** No runtime test can count the
   package's exported sentinels; that is the surface baseline's job and S5's. It
   asserts the seven-outcome half, over both stores, which is the half a store
   can break.
6. **The `transactions` section gained a `Forget` arm**, and it found a real
   defect rather than confirming one: a `Forget` staged inside an
   `eventmemory` unit could never commit, because `revalidateSaves` fenced the
   advance-zero entry a staged removal travels as. The section reported it
   (`committing the transaction this section was inside answered … a checkpoint
   row moved`), the fence now exempts a removal exactly as `Forget` is exempt,
   and the same arm proves `eventpg`'s `DELETE` rides in the caller's
   transaction too. Retiring a projection is a write like any other, so it
   belongs in the section about units of work.
7. **`Save` refuses an unnamed or over-long projection name**, in both stores,
   before anything is issued. The plan names only the cursor's two refusals. A
   name the row's own column cannot key by would otherwise reach the classifier
   as a check-constraint violation with the backend alive — `NotWritten`,
   `ErrBackend`, retried without limit — which is D11's own argument applied to
   the other column.

**Round-1 remediation, 2026-09-08.** Three `[high][immediate]` findings
([`EVENTSOURCE_P3_S2_GAPS.md`](../gaps/EVENTSOURCE_P3_S2_GAPS.md)) closed. Each
was reproduced first — the two interleavings by driving them, not by reasoning
about them — and each fix was reverted afterwards to watch its test go red.

**GAP-1 — the save is now two statements and one is issued.** The single
`INSERT … SELECT … WHERE $3 = 1 OR EXISTS (…) ON CONFLICT DO UPDATE` decided *is
the row there* against the statement's READ COMMITTED snapshot and *which row
does it collide with* against the live index. Reproduced against PostgreSQL 17.9
with a `Forget` held inside an open transaction: a row at advance 5, the DELETE
committed while the save was blocked on its lock, `INSERT 0 1` after 2001 ms, and
a row at advance 6 that no advance-1 save created. Split, an `UPDATE` matches
nothing where the row has gone and `DO NOTHING` never overwrites one. The four
sequential paths the section first measured are now six, remeasured by `psql`
against the DDL `testdata/migration.golden` renders:

| path | statement | tag |
|---|---|---|
| advance 1, no row | `INSERT … ON CONFLICT DO NOTHING` | `INSERT 0 1` |
| advance 1, row at advance 1 | the same | `INSERT 0 0` |
| advance 2, row at advance 1 | `UPDATE … AND advance = $3 - 1` | `UPDATE 1` |
| advance 4, row at advance 2 | the same | `UPDATE 0` |
| advance 3, **no row at all** | the same | `UPDATE 0`, and nothing created |
| the empty cursor at advance 1 | `INSERT …` | `checkpoints_cursor_check` |

and the contention properties are unmoved: a writer holding advance 3 open blocks
the second, which then answers `UPDATE 0` and leaves the winner's cursor (1296 ms),
or `UPDATE 1` where the first rolled back (1299 ms); eight concurrent first saves
at advance 1 against an empty table answer `1 × INSERT 0 1`, `7 × INSERT 0 0`,
one row at advance 1.
`TestEightSaversAtOneAdvanceLeaveOneWinnerAndSevenConflicts` is green.

The case is `TestASaveAboveAdvanceOneCannotResurrectARowAForgetRemoved`
(`event/eventpg/checkpoints_integration_test.go`), which drives that interleaving
through the store's own API and counts the rows with SQL rather than reading the
store's account of itself; its control is the same save with nothing racing it.
The suite's arm is in the **`forget`** section — `forgetRacingASave`, declining
with `unable` for a store that does not claim `Transactions`, because holding a
removal in flight is what makes the meeting exact. **No sixth row joins the
mutation harness**: the defect is an interleaving, and every decorator that
expresses it deterministically also trips `fence` sequentially, which would make
the harness's evidence about the wrong section. Its falsification is direct
instead — with the old statement restored, both the case **and** `RunCheckpoints`'s
`forget` section fail live, and both pass with it back.

**GAP-2 — `CheckpointFactory.Instant` declares the store's own grain.** The suite
minted `time.Now().UTC().Truncate(time.Microsecond)` and compared it with `Equal`
in every section that reads a row back, which is PostgreSQL's `timestamptz` grain
promoted into an obligation no rule states. Measured here: a fixture correct in
every rule §3.1 does state and differing only in the grain of its instant column
failed **ten of twelve** sections at millisecond and at second grain — round trip,
fence, forget, names, bounds, refusal classes, lifecycle, concurrency,
transactions, durability — against a microsecond control that certified twelve.
The obligation is now stated on `event.Progress.At` (a store answers the instant
it was handed; the grain is its backing's and this kernel names none) and the
grain is a factory hook the suite mints through and compares at, absent meaning
exact. It carries `Cursor`'s own anti-vacuity rules, both fatal at the door: it
must round rather than invent (idempotent), and it must keep two instants a
second apart apart. `eventpg`'s factory declares a microsecond and still certifies
twelve; the census is unchanged.

**GAP-3 — a staged removal is told apart from a row.** `eventmemory.Forget`
stages `event.Checkpoint{Projection: projection}` inside a unit, and
`Tx.checkpointHeld` handed that entry back, so a `Load` inside the same unit
answered advance 0 **with the name set** — the half-absent row `Tracker.admit`
refuses as `ErrWrongStore`, and a shape `eventpg` gets right because its `DELETE`
is visible to its own transaction. Reproduced under `-race`. `checkpointHeld`
now answers the zero `Checkpoint` for a name whose last staged entry is a
removal, in both `eventmemory` and the `eventtest` fixture store, which carried
the identical shape and was certified twelve times over. The suite asks in
`forgetsInAUnit` — inside the `transactions` section, gated on `Transactions`
like the rest of it — and the store's own case is
`TestALoadInsideAUnitThatStagedAForgetAnswersTheZeroCheckpoint`, with a load
inside a unit that staged nothing as the control.

**What moved in the kernel, and why.** `event/checkpoint.go` (the `Progress.At`
obligation, a comment), `event/eventmemory/transaction.go` and
`event/eventtest/{checkpoints,sections_checkpoints,export_test,checkpoints_test}.go`.
The one API addition is `CheckpointFactory.Instant`, which `make api` records as
an addition; no line disappeared. The baseline was regenerated once, after the
three fixes, and the arm was neither widened nor excepted.

**Not fixed, deliberately.** Nothing. No `[medium]` or `[low]` was raised by this
round, so `EVENTSOURCE_BACKLOG.md` `## P3` is unchanged.

**Checkpoint (live)**

```sh
cd <repo> \
&& go vet -tags=integration ./event/eventpg/... \
&& FROSTGROVE_EVENTPG_TEST_DSN=x go test -tags=integration -list '.' ./event/eventpg/ | grep -q '^Test' \
&& test "$(FROSTGROVE_EVENTPG_TEST_DSN=x go test -tags=integration -list '^(TestTheCheckpointStoreSatisfiesTheContract|TestEightSaversAtOneAdvanceLeaveOneWinnerAndSevenConflicts|TestASaveOfACursorAtExactlyTheCeilingLands|TestACursorOfArbitraryBytesRoundTripsThroughTheColumn|TestAnEmptyCursorIsRefusedByTheDoorAndByTheColumn|TestEveryFailureTheCheckpointStoresAnswerCarriesOneOfTheSeven|TestAVersionOneSchemaWithRowsMigratesToVersionTwoAndReadsBackUnchanged|TestAVersionOneSchemaAtOtherBoundsIsNotRestamped|TestAFreshDatabaseTakesTheSameVersionTwoList|TestTwoConcurrentPreparesTakeOneLockAtTheNewVersion|TestAStoreAndACheckpointsManagingOneSchemaInOneProcessSerialiseOnTheLock|TestTheNewestPositionCursorNowFailsResumption|TestBothConformanceRunsCertifyEverySectionButTheOneThisStoreDeclines|TestADowngradedSectionIsCaughtByTheCensusAndByNothingElse)$' ./event/eventpg/ | grep -c '^Test')" = 14 \
&& for pass in 1 2; do FROSTGROVE_EVENTPG_TEST_DSN='postgres://vv:vv@localhost:55432/vv?sslmode=disable' \
     go test -race -count=1 -tags=integration ./event/eventpg/... || exit 1; done \
&& ! env -u FROSTGROVE_EVENTPG_TEST_DSN go test -count=1 -tags=integration ./event/eventpg/ 2>&1 | grep -q '^ok'
```

Twice in a row, because a suite that passes once and fails on rerun is a real
defect. **And the section verdicts are read by an arm and not by a reviewer's
eye:** the last two names in the count are the census, which is what makes a run
whose `transactions` or `durability` section reported *not certified* fail instead
of printing `ok`. They are in the count for the same reason every other name is —
a census nobody wrote is a census that never fails.

---

### S3 — `event/projection`: the loop, the router, the lifecycle  `[x]`   *(no database · touches kernel)*

**Delivers** the whole consumer, proved end to end over `eventmemory` and
`eventmemory.Checkpoints` — including the `InUnit` path, which the memory
checkpoint store's ambient transaction makes reachable with no database at all,
at `Destination: projection.Unchecked` because a read model in a Go map has no
`crud` binding to resolve. That is the honest spelling of what such a test is, and
it is why §UC-105 and §UC-128 are S4's live cases and not memory ones.

**Files** the nine `event/projection/*.go` files and their tests;
`scripts/event_test.go` (the `charged` row **and** the doc comment that currently
ends "which is why `health`, `port` and `runtime` stay outside it" — the comment is
what goes stale and it is the thing the next store's author reads).

**Covers** UC-099, UC-101, UC-102, UC-104 (the resolution's logic),
UC-105 (memory half), UC-107(a–d), UC-108, UC-109, UC-110 (memory half), UC-111,
UC-112, UC-114, UC-115, UC-116, UC-117, UC-118, UC-119, UC-130 (memory half),
INV-067, INV-068, INV-070 (construction half), INV-071 (the decorator half),
INV-073, INV-074, INV-075, INV-081.

**Tests** — untagged, over a fake `Ticks` that drives every wait:

- `TestInUnitIsRefusedAndNeverDowngraded` — §UC-107's five halves; (a)(b)(c) at
  `New`, (d) on the first pass **before the handler runs** with the unit rolled
  back, (e) in S4. Each asserts **no page was applied**. **Control:** a correctly
  wired `InUnit` projection passes the same code path, and `Destination:
  Unchecked` is accepted — so (c) is about the field being unstated and not about
  the check being mandatory.
- `TestARetryReAppliesTheLogsOwnPageAfterABackoff` — the delays are `First`,
  `2×First`, capped at `Max`, proved by the ticker rather than slept; `Attempt`
  rises; the phase is `Retrying`; the stored advance is unchanged across two failed
  attempts. **And the ownership control, which is the one that would otherwise pass
  vacuously:** a handler that **truncates, reorders and overwrites the payload
  bytes of** `Batch.Envelopes` — all legal under §INV-021's grant — and then fails
  retryably, asserting the second attempt receives the log's page unchanged and
  that the checkpoint the applied attempt saves covers every position the log
  answered. Against a framework that hands the same slice back, the advance passes
  events nobody ever saw and the control fails loudly.
- `TestNIdlePollsIssueZeroSavesAndNRoundTrips` — a recording `Log` and a recording
  `Checkpoints`; over N idle polls the save count is **zero** and the read count is
  **N**; the reader's in-memory cursor advances when the store answers a different
  one.
- `TestAHaltedProjectionIssuesNothingAndReportsThroughReady` — over a bounded
  window after the halt: **zero** reads, **zero** saves, one published transition,
  `Drain` returning without waiting, `State` and `Ready` still answering, `Run`
  returning `ctx.Err()`. This is the case that refuses the two plausible
  implementations §3.2(6) exists to name — a `for` loop re-applying the page the
  classifier called permanent, and a poll that keeps a dead projection's traffic on
  the database.
- `TestADrainFinishesThePassInFlightAndAHaltedOneReturnsAtOnce` — the pass's
  handler *and* its checkpoint advance both complete; then `Run` returns on the
  cancellation. **Control:** a cancellation with no drain leaves the page unsaved
  and redelivered on the next start, which is the window `Drain` exists to close
  and the proof that it did.
- `TestARouterRoutesDeclaresAndRefusesTheUnclaimed` — §UC-130 over two aggregates
  in memory, five facts, an `Ignore`, an older revision through a declared
  upcaster, and a third aggregate's events skipped and counted. **Two controls,
  and they are what make §INV-081 discriminating:** one `On` removed *inside* a
  covered family halts on the first such event with `ErrUnrouted`; the same removal
  *outside* every covered family is skipped and counted. A router that refused both
  or skipped both fails one half each. Beside them, `RefuseForeign` halting on the
  third aggregate's first event with the same sentinel and the same verdict.
- `TestAPermanentFailureHaltsAndQuarantineIsEnvelopeGranular` — under `Halt` the
  advance is unchanged and the offending envelope's identity is published once;
  under `Quarantine` the sink holds **one** envelope, the applicable ones are in
  the read model, the advance moved **once**, and `Progress.Quarantined` rose by
  **one rather than by the page's length**. **Controls:** a sink that itself fails
  halts instead of skipping; a retryable failure inside the isolation pass returns
  the whole page to `Retrying` and quarantines nothing.
- `TestASingleFailureFollowedByASuccessNeverReportsUnhealthy` — D4's pinned case,
  beside one that crosses `Tolerate` and does.
- `TestASpecCarryingAStoreIsRefusedAndReadOnlyIsAccepted`,
  `TestNewRefusesEverySpecItCannotAssemble` (the twelve rows of the refusal table,
  each naming the field), `TestTheProjectionStartsNothingAndReadsNoEnvironment`
  (a `go/ast` walk: no `*ast.GoStmt`, no `func init`, no `log.`, no `fmt.Print*`,
  no `os.Getenv`, no package-level mutable `var`).
- `TestEveryReadArrivesOutsideEveryUnit` — a recording `Log` decorator asserting
  every `ReadAll` arrived on a context with no executor of that backing bound
  (§INV-071's memory half; the burnt-gap half is S4's).
- **The nine the coverage matrix names and the plan's first draft did not list**,
  each in `event/projection`, each over the memory pair and the fake `Ticks`:
  - `TestAFirstRunStartsAtTheOriginAndASecondResumes` — §UC-099's loop half: no
    row, the walk starts at the origin, one page applied, one save; a second
    projection value over the same stores resumes at the saved cursor and applies
    nothing twice. **Control:** the second run over a log that grew applies only
    what is new.
  - `TestAnUnreadableCursorHaltsAndNeverRestartsAtTheOrigin` — §UC-101, §INV-068:
    a stored cursor of another log and one of a retired format each halt with
    `ErrCursor`, and the stored row is **byte-identical** afterwards. **Control:**
    absence — the same projection with no row at all — starts at the origin, so
    the refusal is discriminating rather than universal.
  - `TestAForgottenCheckpointRefusesTheNextSaveAndHalts` — §UC-102: `Forget`
    under a running projection, the next save `ErrConflict`, the projection
    halted, and the fence **not** reset.
  - `TestARedeliveryCarriesTheSameIdentitiesInMemoryOfItsOwn` — §UC-108: a page
    delivered twice carries the same `(Stream, Version)` pairs and `Attempt` 1
    both times (D3), in a page whose payload bytes are a fresh copy.
  - `TestAHistoryClassFailureHaltsAndNamesNoData` — §UC-111: a payload this build
    cannot read halts, and the refusal names neither the key nor the payload.
  - `TestADrainingProjectionReachesFollowingAndStaysThere` — §UC-112, §INV-067:
    `PhaseDraining` while pages arrive, `PhaseFollowing` when one comes back
    empty, one save per applied page and none after.
  - `TestAClosedStoreHaltsAndATransientBackendRecovers` — §UC-116: `ErrClosed`
    halts; a `Log` failing `ErrBackend` for three passes and then answering
    recovers without a halt and without a duplicate save.
  - `TestTheSupervisorHoldsAProjectionAndNewStartsNothing` — §UC-117: a
    `runtime.Supervisor` starts, drains and stops it; `New` alone issues no read,
    no save and no goroutine.
  - `TestTheAdvanceAndTheHandlersRowsCommitTogetherInMemory` — §UC-105's memory
    half at `Destination: Unchecked`: the handler writes into a map guarded by
    the unit, and a unit that fails after the inner `Save` returned nil leaves
    **neither** the write nor the advance. **Control:** the committed unit leaves
    both. This is the case the memory checkpoint store exists for and D13 makes
    reachable.
- **Three S3 added for the two-instance question, which no section owned** —
  §UC-103, [[D-133]], and §Adopt A2 of the reference adjudication:
  - `TestTwoLiveInstancesOfOneNameTakeTurnsAndNeitherHalts` — a second writer
    presents the advance this projection is about to present, from the same
    fence, on every page: it never halts, it applies the whole log through the
    rows the winner left, and `Ready` answers `ErrOvertaken` once the losing
    streak outlasts `Tolerate` — which is what proves the streak a lost fence
    opens is not cleared by the reads and applies that keep succeeding.
    **Control:** one instance alone drains the same log, stays healthy and
    contends with nothing.
  - `TestTwoLiveInstancesUnderInUnitApplyEachEventOnce` — two `Projection`
    values of one name, both running on their own goroutines under `-race`, over
    one log and one checkpoint row, in both modes. Under `InUnit` against a
    fenced save taken under the row's own lock, no event reaches a handler twice
    and neither instance halts; under `AfterApply` there is no lock to hold
    across a handler call, every event reaches one and neither instance halts.
  - `TestTheAdvanceIsClaimedBeforeTheHandlerInsideAUnitAndAfterItOutside` — the
    order itself, read off a recording checkpoint store and the handler beside
    it: `save` then `apply` under `InUnit`, `apply` then `save` under
    `AfterApply`.
- **Three S3's review added, each closing a `[high]` finding of
  `EVENTSOURCE_P3_S3_GAPS.md`:**
  - `TestAUnitThatRunsTheWorkTwiceIsRefusedAndThePageIsRedelivered` — GAP-1. A
    caller's own retry loop around the transaction runs the work twice; the
    second run is refused by name, the state published names the field rather
    than the checkpoint store, the page is applied **once**, the row is at
    advance 1 and nothing halts. Before the fix this halted permanently with
    `errFenceRefused`, blaming the store for refusing a save over the row its own
    fence admits.
  - `TestACheckpointStoreThatRefusesASaveItsFenceAdmitsHalts` — GAP-1's fourth
    close criterion: `errFenceRefused` is now reachable only from a store that
    did exactly that, and this is the case that pins it. **Control:** the same
    `Conflict` from a store whose row **did** move is contention, publishes
    `ErrOvertaken` and does not halt — so a halt on every conflict fails.
  - `TestAClosedWakeStopsWakingAndAnOpenOneStillDoes` — GAP-2. With `Wake` closed
    and no tick fired, the log is read at most twice over 200 ms; **control:** an
    open channel still releases the poll. Before the fix: 112 082 reads in
    200 ms.
- **And one in the kernel's own suite**, `event/tracker_test.go`:
  `TestTheFenceIsTheDoorsAndNoCallerComputesIt` gains *the door answers the
  advance it presented, landed or refused* — the half a caller could get wrong
  alone, and the reason `Save`'s signature changed.

**Checkpoint**

**Opening act**, before the first file of this section is written:

```sh
./scripts/checks.sh event-kernel-baseline && cp scripts/event_kernel.sha256 .git/event_kernel_before_s3
```

```sh
cd <repo> \
&& go build ./... && go vet ./event/... ./scripts/... \
&& [ -z "$(gofmt -l event scripts)" ] \
&& test "$(go test -list '^(TestInUnitIsRefusedAndNeverDowngraded|TestARetryReAppliesTheLogsOwnPageAfterABackoff|TestNIdlePollsIssueZeroSavesAndNRoundTrips|TestAHaltedProjectionIssuesNothingAndReportsThroughReady|TestADrainFinishesThePassInFlightAndAHaltedOneReturnsAtOnce|TestARouterRoutesDeclaresAndRefusesTheUnclaimed|TestAPermanentFailureHaltsAndQuarantineIsEnvelopeGranular|TestASingleFailureFollowedByASuccessNeverReportsUnhealthy|TestASpecCarryingAStoreIsRefusedAndReadOnlyIsAccepted|TestNewRefusesEverySpecItCannotAssemble|TestTheProjectionStartsNothingAndReadsNoEnvironment|TestEveryReadArrivesOutsideEveryUnit|TestAFirstRunStartsAtTheOriginAndASecondResumes|TestAnUnreadableCursorHaltsAndNeverRestartsAtTheOrigin|TestAForgottenCheckpointRefusesTheNextSaveAndHalts|TestARedeliveryCarriesTheSameIdentitiesInMemoryOfItsOwn|TestAHistoryClassFailureHaltsAndNamesNoData|TestADrainingProjectionReachesFollowingAndStaysThere|TestAClosedStoreHaltsAndATransientBackendRecovers|TestTheSupervisorHoldsAProjectionAndNewStartsNothing|TestTheAdvanceAndTheHandlersRowsCommitTogetherInMemory|TestAUnitThatRunsTheWorkTwiceIsRefusedAndThePageIsRedelivered|TestACheckpointStoreThatRefusesASaveItsFenceAdmitsHalts|TestAClosedWakeStopsWakingAndAnOpenOneStillDoes)$' ./event/projection/ | grep -c '^Test')" = 24 \
&& test "$(go test -list '^(TestNoEventPackageCostsMoreThanTheSeamItNames|TestMerelyImportingTheEventExtensionStartsNothing|TestNoBaseSubsystemDependsOnTheEventExtension)$' ./scripts/ | grep -c '^Test')" = 3 \
&& go test -race -count=1 ./event/... \
&& go test -race -count=1 -run '^(TestNoEventPackageCostsMoreThanTheSeamItNames|TestMerelyImportingTheEventExtensionStartsNothing|TestNoBaseSubsystemDependsOnTheEventExtension)$' ./scripts/ \
&& ./scripts/checks.sh deps \
&& ./scripts/checks.sh event-kernel-baseline \
&& ./scripts/checks.sh event-kernel \
&& ./scripts/checks.sh event-kernel-moved .git/event_kernel_before_s3 \
     '^(event/projection/[a-z_]+\.go|event/checkpoint\.go|event/checkpoint_test\.go|event/tracker_test\.go|event/reader\.go|event/store\.go|event/eventtest/sections_read\.go)$' \
     event/projection/projection.go event/projection/pass.go event/projection/spec.go \
     event/projection/router.go event/projection/page.go event/projection/classify.go \
     event/projection/state.go event/projection/errors.go event/projection/doc.go
```

**The allowed set names six files S3 did not write, and that is the fence
working rather than bending.** `.git/event_kernel_before_s3` was recorded before
the Decide stage settled [[D-128]], which then wrote the ordering contract into
`event/store.go`, `event/reader.go`, `event/eventtest/sections_read.go` and
`event/checkpoint.go` — its own "Where it lives" list, exactly those four. The
other two, `event/checkpoint_test.go` and `event/tracker_test.go`, are the call
sites of `Tracker.Save`, whose signature GAP-1's close moved: the door now
answers the advance it presented. They are named one by one rather than by
widening the pattern to a directory, so the next unplanned kernel edit is still
reported.

**Re-run, 2026-09-09, after the review's three `[high]` findings were closed —
the counted arm is 24 and the moved set is 23:**

```
ok  	github.com/frostgrove/vv/event	6.392s
ok  	github.com/frostgrove/vv/event/eventmemory	1.506s
ok  	github.com/frostgrove/vv/event/eventtest	4.095s
ok  	github.com/frostgrove/vv/event/projection	1.490s
ok  	github.com/frostgrove/vv/scripts	2.637s
check-deps: ok
event-kernel-baseline: 131 files recorded in scripts/event_kernel.sha256
check-event-kernel: ok
the files under event/ this section moved:
  event/checkpoint.go
  event/checkpoint_test.go
  event/eventtest/sections_read.go
  event/projection/classify.go
  event/projection/doc.go
  event/projection/errors.go
  event/projection/facts_test.go
  event/projection/harness_test.go
  event/projection/lifecycle_test.go
  event/projection/loop_test.go
  event/projection/page.go
  event/projection/pass.go
  event/projection/projection.go
  event/projection/retry_test.go
  event/projection/router.go
  event/projection/router_test.go
  event/projection/spec.go
  event/projection/spec_test.go
  event/projection/state.go
  event/projection/unit_test.go
  event/reader.go
  event/store.go
  event/tracker_test.go
event-kernel-moved: ok
CHECKPOINT EXIT=0
```

Beside it, and not part of the checkpoint: `make check` green on every arm,
`make unit` and `make vet` green over every module, `gofmt -l .` silent, and the
live `event/eventpg` suite re-run green **twice in a row** because this section
changed the package S4 will drive it through — and because it now carries S4's
two-instance case, written here for [[D-133]] §3.

`check-deps` is in this section's checkpoint and not only in S5's, because the
whole "root-module package" decision fails here if a single test import brings a
third-party package in. The manifest fence's allowed set is the narrowest of the
three — **only** files under `event/projection/` — because by S3 every other
kernel file is where S1 and S2 left it, and one of them moving here is the phase
editing the kernel twice for one reason; and all nine non-test files are
**required**, which is the arm that turns "eight of the nine were written" from a
green section into a red one.

---

### S4 — the live proof  `[x]`   *(live database)*

**Delivers** every claim [SPEC] §10 makes that needs a database, in
`event/eventpg/projection_integration_test.go` — the satellite, because the root
module may take no third-party dependency and pgx is one.

**Corrected during S4, five times, and each is a line of this plan that was
wrong rather than a scope this section shrank:**

- **The settlement read a matching advance as proof of its own authorship, and
  that lost events** — GAP-1 of the section's review, `[critical]`. D10b's first
  table row said "equal to the one this pass presented → the save landed", which
  is false at N=2: the fence admits one writer at each advance and a rolling
  deploy's second instance takes it the moment this pass's unit rolls back.
  `settle` now compares the **cursor** as well, and a row at the presented
  advance carrying a cursor this pass never presented is `overtaken` like any
  other lost fence (`event/projection/pass.go`, `anothers`). D10b's table,
  §UC-104, §3.4's failure table and [[D-133]]'s table are rewritten together.
  Proved live by `TestASettlementTakesTheRowsCursorWhenAShorterWinnerLeftIt` and
  without a database by
  `TestASettlementResumesFromTheRowsCursorAndNeverFromItsOwn`.
- **§UC-104's Must-not was guarded by an assertion that could not fail** — GAP-2,
  `[high]`. The tally check in
  `TestAnUnconfirmedSaveIsResolvedByOneLoadAndNeverBySaving` sat inside
  `if len(got) != len(payloads)`, so a page applied twice — two distinct payloads
  over a log of two — never entered the body. It is unconditional now, and the
  arm that stands for the landed save commits the cursor that save **presented**
  rather than a string no log ever minted.

- **The eleven landed in five files, not one** —
  `projectioncase_integration_test.go` (the shared harness: one scratch schema
  with its log, checkpoint table and read models, a projection on a goroutine of
  its own, the kill point, and the psql cross-check),
  `projection_integration_test.go` (S3's two-instance case and the loop cases:
  the kill point, the second database, the in-flight watermark, the burnt gap,
  the unconfirmed save, the quarantine and the resume),
  `router_integration_test.go` (UC-130), `rebuild_integration_test.go` (UC-120,
  UC-121) and `replay_integration_test.go` (UC-125 and the benchmark). One file
  of 1 800 lines is not one a reviewer reads, and the checkpoint counts names
  rather than files.
- **The row counts are read out of the database over a pool that is neither the
  projection's nor the handler's, and cross-checked against `psql` when
  `FROSTGROVE_EVENTPG_TEST_PSQL` names a command to ask.** The plan said `docker
  compose exec -T postgres psql`; a Go test cannot depend on that, because
  `docker compose` needs a project directory `go test` does not run in and names
  a container the DSN does not. The checkpoint sets the variable to
  `docker exec -i vv-postgres-1 psql -U vv -d vv`, and when it is set and cannot
  answer the case **fails** rather than quietly not asking — measured: pointed at
  a database that does not exist, `TestTheTwoModesLeaveDifferentStateAtOneKillPoint`
  fails naming the query it could not put. A read model in the second database
  is never asked, because psql reaches the one the DSN names.
- **`TestTheReplayBenchmarkMeasuresTwoOrdersApart` drives 1 000 and 100 000** —
  two orders of magnitude, as its name says and as the body text under it did
  not — and asserts the times are at least an order of magnitude apart **and**
  that the per-event cost stays within a factor of four. An exact tenfold
  assertion over a tenfold step is brittle at the sizes a suite can afford,
  because one round trip is a fixed term; two orders apart makes both arms
  loose and still discriminating. Measured: 1 021 ns/event at 1 000 and
  1 028 ns/event at 100 000.

**Covers** UC-100, UC-104, UC-105, UC-106, UC-107(e), UC-110, UC-113, UC-120,
UC-121, UC-125, UC-128, UC-130, INV-066, INV-070, INV-071, INV-073, INV-081.

**Tests**

- `TestTheTwoModesLeaveDifferentStateAtOneKillPoint` — §UC-105, §UC-106. The same
  projection under `InUnit` and under `AfterApply`, interrupted between the
  handler's commit and the advance, asserting **against what the database actually
  holds** — the read model's rows and the stored advance — in both cases. Row
  counts read on a pool that is neither the projection's nor the handler's and
  cross-checked against `psql`, never from the code's account of itself; the
  spelling is `FROSTGROVE_EVENTPG_TEST_PSQL`, above.
- `TestThreeWiringsToASecondDatabaseAreToldApart` — §UC-128, and the case asserts
  all three because the point is that they are told apart: `AfterApply` to a second
  live database works and its window is §UC-106's; `InUnit` with `Destination`
  naming the second source is **refused on the first pass, before the handler
  runs** (§UC-107(e)); `InUnit` with `Unchecked` is accepted and the crash at the
  same point leaves the second database's rows in place while the advance rolls
  back — `AfterApply` semantics under an `InUnit` spec, measured. **Control:** the
  same handler against a read model in the checkpoint store's own database, with
  `Destination` naming it — the crash at the same point leaves no rows at all.
- `TestAProjectionDoesNotPassAPositionAWriterCouldStillCommit` — §UC-113: writer A
  holds `p` uncommitted, writer B commits `p+1`, the projection must apply nothing
  at or beyond `p` and must not checkpoint past it; after A commits it applies `p`
  then `p+1` in that order. **Control:** the same projection over a quiescent log
  must not stall.
- `TestAProjectionInAUnitPassesABurntGap` — §INV-071's live half: a rolled-back
  append between two committed ones, `InUnit` mode, **under a deadline**, so a read
  issued inside the unit reports as a hang rather than as a timeout nobody reads.
- `TestAnUnconfirmedSaveIsResolvedByOneLoadAndNeverBySaving` — §UC-104, driven with
  `pg_terminate_backend` on an autocommit save. The arm that stands for the landed
  save commits a row carrying **the cursor that save presented**, because that is
  what the settlement reads; the tally afterwards is unconditional, so a page
  applied twice fails rather than passing through a guard shaped for a missing
  payload. **Control:** the same failure inside a bound transaction answers
  `NotWritten` and the pass simply retries, so the two windows are told apart by
  evidence (§INV-052).
- `TestASettlementTakesTheRowsCursorWhenAShorterWinnerLeftIt` — §UC-104's other
  half and [[D-133]]'s third table row, added by the section's review. Two
  `Projection` values of one name whose page sizes differ, in the order every
  rolling deploy produces: the new replica claims advance 1 over the whole log
  and its unit takes the claim back, the old replica takes that advance with its
  own one-event cursor and stops, and the new replica's settling load is held at
  a gate until that row is committed. A position appended afterwards is what puts
  the checkpoint over the gap for good, and the assertion is that every event of
  the log is in exactly one row of the read model. **Controls:** the two cursors
  must differ, the new replica's first page must have been the whole log, and
  exactly one save must have been issued before the settlement, so a run in which
  the two read the same page fails rather than passes; and one instance alone
  over the same log applies every event once.
- `TestARebuiltProjectionDrainsBesideTheLiveOneAndTheRowsAgree` — §UC-120: `orders`
  running, `orders-v2` from zero over the same log to a second destination;
  `orders`'s checkpoint, destination and phase untouched throughout; then, with the
  log quiescent and both in `PhaseFollowing` at one `Highest` and both with
  `Quarantined == 0`, **the two destinations hold the same rows**. The row
  comparison is the assertion; `new.Highest >= old.Highest` is asserted too and the
  case says in its own words which is which. **Controls:** `orders`'s
  `Progress.Applied` rises across the rebuild, proving it never stopped; and a
  deliberately wrong `orders-v2` — one route left unregistered for a type outside
  its covered families, so nothing halts — reaches the same `Highest` and holds
  **different** rows.
- `TestARollbackRestoresNothingBecauseNothingWasTouched` — §UC-121, plus a
  compile-level assertion that comparing two **cursors** is not expressible in the
  API.
- `TestARouterRoutesDeclaresAndRefusesTheUnclaimed` (live) — §UC-130 over a live
  log carrying three aggregates: one `On` removed inside a covered family halts at
  the first such event with `ErrUnrouted` and leaves the checkpoint where it was;
  the same removal outside every covered family is skipped, counted and invisible.
  **Control:** the complete router reaches `PhaseFollowing` with the rows all four
  appliers wrote.
- `TestAQuarantineIsEnvelopeGranular` (live) — §10(13): one corrupt payload in a
  live page, the rest applying; the sink holds one envelope, the read model holds
  the rest, and `quarantined` **in the checkpoint row** is 1 rather than the page's
  length — read out of the database.
- `TestAProjectionResumesThroughASecondValueOverOneBacking` — §UC-100, and the
  live half of what `durability` certifies: a projection runs, saves, and is
  replaced by a **second process's** wiring — new `*sql.DB`, new `Store`, new
  `Checkpoints`, new `Projection` over the same schema — which resumes at the
  saved cursor and re-applies nothing that was already applied, asserted against
  the read model's rows. **Control:** the same second wiring pointed at a
  projection name that was never saved starts at the origin, so the resume is
  the cursor's doing and not the read model's idempotency.
- `TestTwoLiveInstancesOfOneNameOverOneSchema` — §UC-103's live half, [[D-133]]
  §3 and §Adopt A2 of the reference adjudication. **Written in S3, because the
  claim it measures was S3's and the review would not sign the section off on a
  `sync.Mutex` the test supplied over `eventmemory`** — it is listed here because
  it is S4's file and S4's checkpoint counts it. Two `Projection` values of one
  name over one schema, in **both** modes, with each instance's first save held
  at a gate until both have issued one, so the contention is driven rather than
  hoped for. The reference runs its whole acceptance suite at
  `--scale event-sourcing-app=2` for the reason that transfers verbatim: the
  contention branch is never executed at N=1. Under `InUnit` the page both
  instances claimed is in **one** row of the read model and the handlers were
  called for exactly the log's length — the loser is refused before its handler
  runs, and at advance 1 what refuses it is PostgreSQL's speculative insertion
  rather than a row lock, because there is no row yet. Under `AfterApply` that
  page is in **two** rows. **Controls:** the gate must have opened and
  `ErrOvertaken` must have been published, so a run in which one instance drained
  the log before the other woke fails rather than passes; and one instance alone
  drains the same log, applies every event once, contends with nothing and
  reaches `PhaseFollowing`.
- `BenchmarkStreamReplay` and `TestTheReplayBenchmarkMeasuresTwoOrdersApart` —
  §UC-125. The benchmark replays a stream of a caller-chosen size —
  `-eventpg.replay.events`, defaulting to 10 000 — and reports ns/event beside
  ns/op; the test drives two sizes **two** orders of magnitude apart and asserts
  the times are at least an order of magnitude apart and the per-event cost
  within a factor of four, so a benchmark measuring nothing is visible.
  **The benchmark does not skip when the DSN is unset** — `TestMain` fails the
  whole binary, as everywhere else in this module.

**Checkpoint (live)**

```sh
cd <repo> \
&& go vet -tags=integration ./event/eventpg/... \
&& [ -z "$(gofmt -l event)" ] \
&& FROSTGROVE_EVENTPG_TEST_DSN=x go test -tags=integration -list '.' ./event/eventpg/ | grep -q '^Test' \
&& test "$(FROSTGROVE_EVENTPG_TEST_DSN=x go test -tags=integration -list '^(TestTheTwoModesLeaveDifferentStateAtOneKillPoint|TestThreeWiringsToASecondDatabaseAreToldApart|TestAProjectionDoesNotPassAPositionAWriterCouldStillCommit|TestAProjectionInAUnitPassesABurntGap|TestAnUnconfirmedSaveIsResolvedByOneLoadAndNeverBySaving|TestASettlementTakesTheRowsCursorWhenAShorterWinnerLeftIt|TestARebuiltProjectionDrainsBesideTheLiveOneAndTheRowsAgree|TestARollbackRestoresNothingBecauseNothingWasTouched|TestARouterRoutesDeclaresAndRefusesTheUnclaimed|TestAQuarantineIsEnvelopeGranular|TestTheReplayBenchmarkMeasuresTwoOrdersApart|TestAProjectionResumesThroughASecondValueOverOneBacking|TestTwoLiveInstancesOfOneNameOverOneSchema)$' ./event/eventpg/ | grep -c '^Test')" = 13 \
&& test "$(FROSTGROVE_EVENTPG_TEST_DSN=x go test -tags=integration -list '^BenchmarkStreamReplay$' ./event/eventpg/ | grep -c '^Benchmark')" = 1 \
&& for pass in 1 2; do FROSTGROVE_EVENTPG_TEST_DSN='postgres://vv:vv@localhost:55432/vv?sslmode=disable' \
     FROSTGROVE_EVENTPG_TEST_PSQL='docker exec -i vv-postgres-1 psql -U vv -d vv' \
     go test -race -count=1 -tags=integration ./event/eventpg/... || exit 1; done \
&& FROSTGROVE_EVENTPG_TEST_DSN='postgres://vv:vv@localhost:55432/vv?sslmode=disable' \
     go test -count=1 -tags=integration -run '^$' -bench '^BenchmarkStreamReplay$' -benchtime 1x ./event/eventpg/ \
&& ! env -u FROSTGROVE_EVENTPG_TEST_DSN go test -count=1 -tags=integration ./event/eventpg/ 2>&1 | grep -q '^ok' \
&& ! env -u FROSTGROVE_EVENTPG_TEST_DSN go test -count=1 -tags=integration -run '^$' -bench '^BenchmarkStreamReplay$' -benchtime 1x ./event/eventpg/ 2>&1 | grep -q '^ok' \
&& go test -race -count=1 ./event/... \
&& ./scripts/checks.sh event-kernel-baseline \
&& ./scripts/checks.sh event-kernel \
&& ./scripts/checks.sh event-kernel-moved .git/event_kernel_before_s4 \
     '^event/projection/(pass\.go|unit_test\.go)$' \
     event/projection/pass.go event/projection/unit_test.go
```

It is S2's shape with S4's own list, and four things about it are deliberate.
**The count is thirteen tests and one benchmark, counted separately**, because
`-list` prints a benchmark under `^Benchmark` and an arm anchored on `^Test`
would never have noticed `BenchmarkStreamReplay` missing — and §UC-125's whole
deliverable is the instrument. **The full package runs twice in a row**, so a
suite that passes once and fails on rerun is a defect and not a flake; the whole
existing conformance run at the defaults and at the narrow limits is inside those
two passes, unchanged, which is §10(1). **The benchmark is executed at
`-benchtime 1x`**, because a benchmark that only compiles is a benchmark nobody
has run. **The unset-DSN arm is doubled**: `go test` and the benchmark invocation
must each fail rather than skip, since a `Benchmark` that skipped on a missing DSN
would be the one place in this module where an absent gate reads as a pass.

**One of the thirteen is already on disk.** S3's review would not sign the section
off with [[D-133]] §3 pinned only by a `sync.Mutex` the test supplied over
`eventmemory`, so `TestTwoLiveInstancesOfOneNameOverOneSchema` and the
`projectionStand` it drives were written there and are green live. S4 adds the
remaining twelve and a second harness beside that one, `projectionCase`, which
they share: `projectionStand` is built around a gate, one name and a
one-envelope page, and generalising it would have rewritten a case the previous
section's review had already signed off.

**The manifest fence moved once after all, and it is GAP-1's fix.** S4 was
planned as integration test files in `event/eventpg`, the one directory
`check-event-kernel` does not look at — and it was that, until the section's
review found a `[critical]` in the settlement itself. Closing it changed
`event/projection/pass.go` and added a case to `event/projection/unit_test.go`,
both inside the fence, so the baseline was re-recorded in the same change with
`make check-event-kernel-baseline` and those two paths are what moved.

**Run, 2026-09-09, after the review's two blocking findings were closed — the
counted arms are 13 and 1, both live passes green, the benchmark executed, both
unset-DSN arms refusing, and the kernel fence recording exactly the two files
GAP-1's fix moved:**

```
ok  	github.com/frostgrove/vv/event/eventpg	106.148s
ok  	github.com/frostgrove/vv/event/eventpg	105.851s
goos: linux
goarch: amd64
pkg: github.com/frostgrove/vv/event/eventpg
cpu: Intel(R) Core(TM) i9-10900K CPU @ 3.70GHz
BenchmarkStreamReplay-20    	       1	  10878441 ns/op	      1088 ns/event
PASS
ok  	github.com/frostgrove/vv/event/eventpg	0.293s
ok  	github.com/frostgrove/vv/event	6.299s
ok  	github.com/frostgrove/vv/event/eventmemory	1.502s
ok  	github.com/frostgrove/vv/event/eventtest	4.058s
ok  	github.com/frostgrove/vv/event/projection	1.491s
event-kernel-baseline: 131 files recorded in scripts/event_kernel.sha256
check-event-kernel: ok
the files under event/ this section moved:
  event/projection/pass.go
  event/projection/unit_test.go
event-kernel-moved: ok
CHECKPOINT EXIT=0
```

Beside it, and not part of the checkpoint: `gofmt -l .` silent, `go build ./...`
clean, `go vet ./event/...` clean, `go test -race -count=1 ./event/...` green
over all four packages, `make unit` green over 97 packages, `make vet` green, and
`make check` green on all ten arms including `check-event-kernel`.

**§UC-125's own numbers, reproduced on the gate's hardware** and reported by
`TestTheReplayBenchmarkMeasuresTwoOrdersApart`, which logs them:

```
full replay: 1000 events in 1.089378ms (1089 ns/event), 100000 events in 107.634597ms (1076 ns/event)
```

That is §5.1's table within an order of magnitude on this host: the plan measured
10.19 – 10.93 ms for 10 000 events through a no-op codec, i.e. ~1.02 µs an event,
and this reproduces 1.02 µs at both ends of a hundredfold range.

**Mutation evidence — the implementation was broken thirteen times and restored,
and each break was caught by the case it belongs to.** Twelve are in the table
below; the thirteenth found a redundancy rather than a hole and is written out
after it, because that is worth keeping too. **Two of the twelve were caught by
nothing until the section's review** — the last two rows, which are GAP-1 and
GAP-2, and which are the reason the ten above them are not by themselves evidence
that a suite is complete.

| What was broken | What caught it |
|---|---|
| `pass.go` `held.quarantined++` → `+= len(this.page)` | `TestAQuarantineIsEnvelopeGranular` — "records 5 quarantined where one envelope of a page of 5 was passed" |
| `pass.go` an `ErrUncertain` save is `redeliver`ed rather than settled | `TestAnUnconfirmedSaveIsResolvedByOneLoadAndNeverBySaving`, both arms — "no second load was ever issued" |
| `pass.go` `outsideAUnit` saves before it applies | `TestTheTwoModesLeaveDifferentStateAtOneKillPoint` — "a checkpoint row at advance 1 outlived a kill" |
| `router.go` `foreignTo` refuses every unrouted type | `TestARouterRoutesDeclaresAndRefusesTheUnclaimed`, the out-of-family arm — halted on `payments.authorised` |
| `eventpg/read.go` `deliverable` returns the whole page | `TestAProjectionDoesNotPassAPositionAWriterCouldStillCommit` — "applied 1 rows while position 1 was still uncommitted" |
| `pass.go` `resume` reads from `""` rather than the stored cursor | `TestAProjectionResumesThroughASecondValueOverOneBacking` — "holds [r-1 r-2 r-3 r-1 r-2 r-3 r-4 r-5 r-6]" |
| `eventpg/checkpoints.go` `Forget` issues nothing | `TestARollbackRestoresNothingBecauseNothingWasTouched` — "still there after Forget" |
| `eventpg/read.go` never mints a settlement bound | `TestAProjectionInAUnitPassesABurntGap` — the deadline fired, which is the shape a read inside the unit has |
| `pass.go` `checkUnit` stops resolving `Destination` | `TestThreeWiringsToASecondDatabaseAreToldApart` — the second-source arm never halted |
| `pass.go` `Progress.Applied` stops being cumulative | `TestARebuiltProjectionDrainsBesideTheLiveOneAndTheRowsAgree` — "at 5 applied where it was at 6" |
| `pass.go` `settle` reads a matching advance alone as its own save — GAP-1's defect, restored | live: `TestASettlementTakesTheRowsCursorWhenAShorterWinnerLeftIt` — *"the read model holds map[s-1:1 s-4:1] while the checkpoint stands at advance 2, highest 4 and quarantined 0"*; and without a database `TestASettlementResumesFromTheRowsCursorAndNeverFromItsOwn` — *"applied [four] where the row it settled against carries the cursor after [one]"* |
| `pass.go` `settle` re-applies the page on the confirmed arm — §UC-104's Must-not, implemented verbatim | `TestAnUnconfirmedSaveIsResolvedByOneLoadAndNeverBySaving`, the landed-save arm — *"holds map[u-1:2 u-2:2] where the page was applied once"*; and `TestASettlementResumesFromTheRowsCursorAndNeverFromItsOwn`'s control arm in `make unit` |

The last two are the section's review, driven twice each: the defect restored to
watch the case fail, and the case re-run against the fix. Before the review they
were caught by **nothing** — the first because the plan's own contract said a
matching advance is proof, the second because the tally assertion sat inside a
guard that never fired.

**And the cross-check itself was broken twice, because a check that cannot fail
is not one.** Pointed at a database that does not exist,
`TestTheTwoModesLeaveDifferentStateAtOneKillPoint` and
`TestAQuarantineIsEnvelopeGranular` fail naming the query psql could not put —
so the command being unusable is a failure and not a measurement quietly skipped.
And with psql asked for `ORDER BY id DESC` while the pool is asked for `ORDER BY
id`, the same case fails with `psql prints [k-4 k-3 k-2 k-1] … where this process
read [k-1 k-2 k-3 k-4] out of the same table` — so the two answers are really
compared rather than merely both fetched.

**The thirteenth, kept because it is a finding.** Disabling only `checkUnit`'s
`!bound` arm changed nothing: the `crud.IsTransaction(executor)` arm below it
refuses a nil executor too, so the two arms cover one another and the whole
check has to be removed before UC-107(e) fails. That is a redundancy rather than
a defect, and it is why the mutation recorded above removes the whole
`Destination` resolution instead.

---

### S5 — documentation, decisions, the surface and `make check`  `[x]`   *(no database, re-runs the gate)*

**Delivers** everything that is not code and the final green.

**Files** `scripts/projection_test.go` (new — the five surface, AST and
source-comment walks four invariants name and no section owned before);
`docs/modules/{en,ru}/projection.md` and their `Index.md` rows;
`docs/modules/{en,ru}/eventpg.md`, `event.md`, `eventmemory.md`, `eventtest.md`
extended; `event/eventpg/MIGRATIONS.md`;
`docs/ai/flows/FL-038-a-settled-cursor-becomes-a-durable-checkpoint.md` and its
three kinds of row in `docs/ai/flows/Index.md`; FL-036 and FL-037 extended;
`docs/ai/decisions/D-129…D-132` and their `Index.md` rows (D-128 was
settled ahead of this section and is already on disk, and so is **D-133**, which
S3 wrote with its `Index.md` row — what S5 still owes it is the module pages'
delivery row: a lost fence is a turn rather than a halt, `ErrOvertaken` is what
says so, under `AfterApply` two live instances both apply the overlapping page,
and — added by S4's review — **the settlement of an unconfirmed save is decided
by the cursor the row carries and never by the advance alone**, which is the
obligation `roundTripSection`'s byte-faithful cursor round trip already puts on
a third-party `Checkpoints`);
`docs/ai/usecases/Index.md` (`UC-032`); `docs/roadmaps/Roadmap.md` (E2 rewritten,
not annotated done); `_examples/event-checkpoints-elsewhere/`;
`.agents/artifacts/gaps/EVENTSOURCE_BACKLOG.md` (`## P3`, appended);
`docs/api/surface.md` regenerated.

**The ordering contract this section freezes is [[D-128]], and it is already on
disk.** It was settled ahead of S3 sign-off, because S5's fence is what would have
made it expensive: `check-event-kernel`'s manifest covers everything under
`event/` outside `event/eventpg`, so `reader.go`, `store.go`, `checkpoint.go` and
`eventtest/sections_read.go` are all inside it. What S5 freezes is therefore the
**decided** contract and not the one that happened to be in the tree:

- `Log.ReadAll` answers ascending positions **and** one stream's events in that
  stream's own order; there is no capability for either, and adding one is the
  decision being reversed by a line.
- `Reader.checkPage`'s ascending arm is a refusal for every store, and its comment
  names which half of the law a single page can be checked against.
- `probe.ascending` and `probe.subsequence` stay ungated — neither section that
  runs them takes a `needs` function.
- `Progress.Highest` is the completeness watermark, stated as the one testable
  sentence D-128 carries, not as "the highest position of the page".

**Two rows are therefore added to the module pages** (both languages), beside the
four contract rows below: **the ordering row** — position order and stream
subsequence are laws, `Capabilities` has no member for them, and a store that
cannot hold both is not a conformant store; and **the watermark row** — what
`Highest` promises, and that `Quarantined` is what keeps it from reading as a
claim about the read model.

**The four decision docs**, at the next free numbers after `D-128`:

- **D-129 — a checkpoint is a store-minted cursor, never a position.** [SPEC] §1 is
  the argument; the decision is what stops the next maintainer adding
  `Checkpoint.Position` for a dashboard. Links [[D-121]], [[D-128]], §UC-037,
  §INV-035, §INV-066.
- **D-130 — a projection is a supervised runner and the checkpoint advance is the
  caller's unit of work.** Why the framework opens no transaction, why `InUnit` is
  refused rather than downgraded **and what its precondition is**, why the read is
  outside the unit, why a halted projection does nothing at all and never clears,
  why each attempt is handed its own page, and — the paragraph backlog `## P3` §1
  asks for — why phase 3 **re-spells** `jobs.BackoffPolicy`, `jobs.RetryLimit` and
  `jobs.Permanent` rather than importing them (§11.3 forbids this package the
  `jobs` closure, and a projection that dragged the job runtime into every consumer
  of the root module would be worse). Links [[D-092]], [[D-118]], [[D-126]],
  §INV-021, §INV-070, §INV-071.
- **D-131 — a router's coverage is inferred from its routes, and an unclaimed type
  inside it is a refusal.** Why a missing `projection.On` must not be a skip, why
  the skip that remains is not on `State`, why `Ignore` is by name rather than by
  fact, and why the router seals (D7). Links [[D-123]], §INV-081.
- **D-132 — a snapshot is added from a measured need, not a measured cost, and
  this package writes no log line.** The measured numbers, the trigger, the
  contract §5.4 fixes, **and D9 in as many words** — "this package deliberately
  emits nothing" is exactly the kind of claim that gets quietly reversed by the
  first person who wants to debug a halt. Links §INV-008, §INV-079, [[D-062]],
  [[D-091]].

**The four contract rows** the module pages carry as contracts rather than prose:

1. **the checkpoint row** — a checkpoint is a cursor; `Progress` resumes nothing;
   there is no function from a `Position` to a `Cursor`.
2. **the delivery row** — at-least-once in **both** modes, what `InUnit` does and
   does not make atomic, **and its precondition in the same breath**: the handler's
   writes are inside the unit's transaction or `InUnit` is `AfterApply` with a
   different name (§UC-128).
3. **the page row** — each attempt gets its own page and the handler may do what it
   likes with the one it holds.
4. **the routing row** — an unclaimed type of a routed family halts, and a foreign
   family is skipped and counted.

**Three sentences S3's review owes this section, each closing a `[high]`
finding of `EVENTSOURCE_P3_S3_GAPS.md` and each measured rather than reasoned:**

- **beside `Advance`, the two-instance row** — under `AfterApply` two live
  instances of one name both apply the overlapping page before the fence fires,
  so the handler must be idempotent; under `InUnit` against a store that
  evaluates the fenced save against a tuple it holds — PostgreSQL, at advance 1
  by speculative insertion and above it by the row lock — the loser is refused
  before its handler runs; **the loser takes its turn and does not halt**
  ([[D-133]]); and `Placement: Singleton` is a promise to the deployment rather
  than an enforcement.
- **beside `Idle`, the wake row** — a `Wake` signal is a hint layered on a poll
  that always runs and never the delivery mechanism, and **closing the channel
  says there will be no more hints**: the loop stops waiting on it and follows on
  `Idle` alone.
- **beside `Unit`, the arity row** — a unit runs the work exactly once; a unit
  that retries its transaction answers the failure and the page is re-delivered.

**And D-130 states the arity as a decision**, because it is the one obligation on
`Spec.Unit` a caller cannot read off the signature and the shape that breaks it
is the ordinary 40001 retry [[D-126]] tells callers to own.

**The five walks in `scripts/projection_test.go`**, which are where four
invariants' named proofs live. Before this section they were named by the
coverage matrix and owned by no file, which is the shape a use case gets reported
closed in:

| Test | Invariant | What it walks |
|---|---|---|
| `TestNoExportedFunctionTakesAPositionAndAnswersACursor` | INV-066 | a `go/types` walk over the packages the regenerated `docs/api/surface.md` lists: no exported function, method or constructor anywhere in `event/…` takes an `event.Position` and answers an `event.Cursor` |
| `TestCursorIsNeverCompared` | INV-066 | an AST walk for a binary `<`, `<=`, `>`, `>=` over an `event.Cursor`, outside the store package that parses its own — a cursor is opaque and ordering two is the position arithmetic this phase exists to refuse |
| `TestNoConstructorTakesAProgressAndAnswersACursor` | INV-077 | the same surface, for `Progress` — the field that must never become a resume point |
| `TestNoCommentInTheProjectionPackagePromisesExactlyOnce` | INV-072 | `event/projection`'s **own source comments**, which `TestNoDocPromisesExactlyOnceDelivery` does not read: that test reads `docs/` only (`scripts/docs_test.go:801`), so a package comment promising exactly-once would ship unread |
| `TestNoSnapshotAuthorityIsDeclaredOrPromised` | INV-079 | §INV-008's existing grep extended to `event/projection`, plus the absence of any snapshot symbol from the surface baseline |

Each carries the control its kind needs: a fixture declaring the forbidden shape
in the walker's own testdata must be **reported**, so a walk that found nothing
because it looked in the wrong place fails.

**§INV-021's hand-off enumeration gains its eighth row** — framework → handler,
`Batch.Envelopes` and its payloads, the first hand-off whose sender re-reads what
it handed over, made safe by the sender with a copy per attempt. **Appended, never
inserted**, which is what that invariant provides for, and the ordinal is cited
from both module pages and from FL-038.

**Checkpoint**

```sh
cd <repo> \
&& make unit && make vet && [ -z "$(gofmt -l .)" ] && make tidy && git diff --quiet -- '**/go.mod' '**/go.sum' \
&& make examples \
&& make api \
&& test "$(go test -list '^(TestNoDocPromisesExactlyOnceDelivery|TestEverySymbolTheDocsCiteIsDeclaredWhereTheDocSaysItIs|TestAPromiseOfExactlyOnceDeliveryIsReportedAndARefusalOfOneIsNot|TestNoExportedFunctionTakesAPositionAndAnswersACursor|TestCursorIsNeverCompared|TestNoConstructorTakesAProgressAndAnswersACursor|TestNoCommentInTheProjectionPackagePromisesExactlyOnce|TestNoSnapshotAuthorityIsDeclaredOrPromised)$' ./scripts/ | grep -c '^Test')" = 8 \
&& go test -race -count=1 -run '^(TestNoDocPromisesExactlyOnceDelivery|TestEverySymbolTheDocsCiteIsDeclaredWhereTheDocSaysItIs|TestAPromiseOfExactlyOnceDeliveryIsReportedAndARefusalOfOneIsNot|TestNoExportedFunctionTakesAPositionAndAnswersACursor|TestCursorIsNeverCompared|TestNoConstructorTakesAProgressAndAnswersACursor|TestNoCommentInTheProjectionPackagePromisesExactlyOnce|TestNoSnapshotAuthorityIsDeclaredOrPromised)$' ./scripts/ \
&& ./scripts/checks.sh event-kernel-baseline && make check \
&& git diff --stat -- docs/api/surface.md
```

`make api` runs **before** the count arm and not after it, because three of the
eight walks read the regenerated `docs/api/surface.md`: a surface check run
against yesterday's baseline is a check of the wrong artefact.

The `make api` diff is **read by a person and asserted by nothing**, which is the
rule: a line that appears there is a new promise and a line that disappears is a
breaking change.

**Ran green, 2026-09-09 — and again after the review's `[high]` GAP-1 was closed.**
The whole command as one `&&` chain, exit 0. The transcript is 310 lines; what it
turns on, from the re-run:

```
ok  	github.com/frostgrove/vv/event	6.740s
ok  	github.com/frostgrove/vv/event/eventmemory	(cached)
ok  	github.com/frostgrove/vv/event/eventtest	(cached)
ok  	github.com/frostgrove/vv/event/projection	(cached)
ok  	github.com/frostgrove/vv/scripts	25.375s
ok  	github.com/frostgrove/vv/event/eventpg	36.798s
…
?   	github.com/frostgrove/vv/_examples/event-checkpoints-elsewhere	[no test files]
api: docs/api/surface.md regenerated — read the diff
ok  	github.com/frostgrove/vv/scripts	17.184s
event-kernel-baseline: 131 files recorded in scripts/event_kernel.sha256
check-deps: ok
check-tiers: ok
check-utils: ok
check-triplets: ok
check-todo: ok
check-replaces: ok
check-tidy: ok
check-otel-schema: ok
check-workspace: ok
check-event-kernel: ok
 docs/api/surface.md | 50 ++++++++++++++++++++++++++++++++++++++++++++++++++
 1 file changed, 50 insertions(+)
```

The count arm passed at 8 and the eight walks ran under `-race` (the second
`scripts` line, 17.184s). `event-kernel-baseline` recorded 131 files, the same
count S1–S4 left. GAP-1's fix moved exactly one of them — a comment in
`event/projection/state.go`, a path this plan's own fence already lists — so the
predecessor went to `.git/event_kernel_before_s5_gap1` first and
`event-kernel-moved` read the move back out loud:

```
the files under event/ this section moved:
  event/projection/state.go
event-kernel-moved: ok
```

The frozen contract is [[D-128]]'s as decided rather than as it stood before the
Decide stage.

**The `make api` diff, read by a person:** 50 lines, **all additions and no
removals**. `event` gains `Checkpoint`, `CheckpointCapabilities`, `Checkpoints`,
`Progress`, `Tracker` and `Track`; `eventmemory` and `eventpg` each gain
`CheckpointSpec`, `Checkpoints` and `NewCheckpoints`; `eventtest` gains
`RunCheckpoints` and `CheckpointFactory`; and `event/projection` appears as a new
section of 30 lines. Nothing disappeared, so nothing here is a breaking change
before the first tag.

**Live, twice in a row:**

```
FROSTGROVE_EVENTPG_TEST_DSN='postgres://vv:vv@localhost:55432/vv?sslmode=disable' \
  go test -race -count=1 -tags=integration ./event/eventpg/...
ok  	github.com/frostgrove/vv/event/eventpg	104.908s
ok  	github.com/frostgrove/vv/event/eventpg	104.289s
```

**And the worked example ran against a live database rather than only compiling**
(`cd _examples && GOWORK=off go run ./event-checkpoints-elsewhere`), which is the
half `make examples` cannot reach:

```
balances   draining   applied=0 quarantined=0
balances   draining   applied=6 quarantined=0
balances   following  applied=6 quarantined=0
acme/1               100
acme/2               100
```

read back out of the database rather than off the program's own stdout:

```
 projection | advance | highest | applied | quarantined | octet_length
------------+---------+---------+---------+-------------+--------------
 balances   |       1 |       6 |       6 |           0 |           28

 account | balance | version
---------+---------+---------
 acme/1  |     100 |       3
 acme/2  |     100 |       3
```

**Mutation evidence for the five new walks.** One file, `event/projection/zzmutation.go`,
declaring `type Snapshot`, `func ResumeAt(event.Position) event.Cursor`,
`func CursorFor(event.Progress) event.Cursor`, `func Later(held, taken event.Cursor) bool`
and a comment promising exactly-once delivery. All five went red, each by its own
arm, and green again when the file was removed:

```
--- FAIL: TestNoExportedFunctionTakesAPositionAndAnswersACursor
    projection_test.go:47: …projection.ResumeAt takes event.Position and answers event.Cursor
--- FAIL: TestNoConstructorTakesAProgressAndAnswersACursor
    projection_test.go:65: …projection.CursorFor takes event.Progress and answers event.Cursor
--- FAIL: TestCursorIsNeverCompared
    projection_test.go:104: ../event/projection/zzmutation.go:13:52 orders two cursors …
--- FAIL: TestNoCommentInTheProjectionPackagePromisesExactlyOnce
    projection_test.go:148: ../event/projection/zzmutation.go:6 promises exactly-once delivery …
--- FAIL: TestNoSnapshotAuthorityIsDeclaredOrPromised
    projection_test.go:175: ../event/projection/zzmutation.go:7:6 declares Snapshot …
    projection_test.go:187: ../docs/api/surface.md:1132 publishes "type Snapshot struct{ ... }" …
```

The last line is the second arm of the snapshot walk, driven by regenerating
`docs/api/surface.md` with the mutation in the tree — so the baseline arm is
proved against the real artefact and not only against its fixture.

### GAP-1, closed: §INV-072's window was one sentence thick, and a live sentence was already behind it

The S5 review's one `[high]`. `wording.refuses` exempted a promise whenever the
negation regex matched anywhere in the preceding sentence, and this repository's
prose is negative by habit. **Reproduced before it was fixed**: the delivery row
of `docs/modules/en/projection.md:77` was replaced with *"The framework
deduplicates nothing. Every event reaches the handler exactly once, so a read
model needs no idempotency of its own."* and `TestNoDocPromisesExactlyOnceDelivery`
stayed **green**; deleting only the four-word first sentence turned it red at that
line. The Russian page took the same treatment — *"Фреймворк ничего не
дедуплицирует. Каждое событие доходит до обработчика ровно один раз…"* — and was
green too, so both rows of the table had the hole, not just the English one.

**The model now asks whether the negation governs the claim.** A negation inside
the claim's own clause refuses it. Further away — an earlier clause, or the
sentence before — a negation refuses only when that span also names the claim, by
saying *promise*, *guarantee* or *claim* (`wording.denying`, one regex per
language) or by spelling the frequency itself. The clause is delimited by
`block.clauseFrom`, the mirror of the `clauseTo` that was already there. With the
narrowing in place both driven sentences are reported and both pages are red:

```
--- FAIL: TestNoDocPromisesExactlyOnceDelivery
    docs_test.go:827: ../docs/modules/en/projection.md:77 promises exactly-once delivery, …
    docs_test.go:827: ../docs/modules/ru/projection.md:80 promises exactly-once delivery, …
```

`TestAPromiseOfExactlyOnceDeliveryIsReportedAndARefusalOfOneIsNot` carries the
driven sentence verbatim as `window.md:13`, asserted **reported**, next to
`window.md:5` — *"We never promise it. The broker delivers each event exactly
once."* — asserted **not** reported. The two differ only in whether the negation
is about the claim, which is the whole of the repair, and each has its own failure
message saying so. The same pair is written in Russian as `обход.md:9` and `:11`,
because the driven reproduction showed the hole was in both rows of the table and
only the English row would otherwise have a regression test. Reverting `refuses`
alone turns all three assertions red; the fuzz target ran 1 088 649 executions
over the new offset arithmetic without a crash.

**Five sentences the narrowed window then reported, and every one was corrected
rather than exempted:**

| Where | Was | Is |
|---|---|---|
| `D-128:74` | *"as long as both are delivered **exactly once** and the cursor never advances past an undelivered event"* — an exactly-once **delivery** claim in the decision this section freezes, and false: the tuple read gives gap-free **at least once** | the exactly-once clause is gone; the sentence now says what the tuple order buys — no committed event is skipped — and states that delivery stays at least once (§INV-072) under either order, which is what `D-128:119` already said twelve lines down |
| `D-118:88` | *"Conflating the two is how an exactly-once claim gets made by accident"* | *"…how a claim this repository never makes — exactly-once delivery — gets made by accident"*: the refusal is now in the span that carries the phrase |
| `D-130:7` and `docs/ai/decisions/Index.md:185` | `Spec.Unit` *"must run the work it is given **exactly once**"* — bold, two lines under the word `projection`, and the phrase this invariant exists to keep away from delivery | *"once and no more"*, which is what the section heading two paragraphs down already said |
| `event/projection/state.go:52` | *"a halted one publishes its halt **exactly once**"* — about publishing a `State`, in the package a consumer reads with `go doc` | *"once and no more"* |

`event/projection/doc.go:24` and `spec.go:60` keep *"runs the work exactly once"*
deliberately: the comment walk's narrower window exists for exactly that sentence
and says so, and backlog `## P3` §66 is where the residual is recorded.

[[FL-036]]'s paragraph on the walker was rewritten in the same change, because it
is where the model is described in prose and it stated the old window.

---

## The deliverable checklist

A phase is not finished when it compiles. Phase 1 left thirteen of fourteen files
under `event/` with no flow row; this table is what stops that repeating.

| # | Deliverable | Section | What catches it if it is missing |
|---|---|---|---|
| 1 | `scripts/event_kernel.sha256`, regenerated in every kernel-touching section, and its predecessor copied to `.git/event_kernel_before_s<N>` before that section's first file | S1–S4 (S4 only for its review's `[critical]` fix, which reached `event/projection`) | `check-event-kernel` itself; and `event-kernel-moved`, which refuses when the predecessor is missing |
| 2 | `scripts/checks.sh`'s comment naming **what moved and why**, listing the five kernel entries, the three `eventmemory` files (D13) and `event/projection` | S1 | this checklist |
| 3 | `scripts/checks_test.go`'s **ten** self-test cases — five for `event-kernel`, five for `event-kernel-moved`, the first of which is the unchanged manifest that must **fail** | S1 | `make unit` |
| 4 | `scripts/vv` dispatch + usage and the `Makefile` `COMMANDS` rows for `check-event-kernel-baseline` and `check-event-kernel-moved` | S1 | nothing — this checklist |
| 5 | **no** `go.work` line and **no** `replace` in `test/go.mod` or `_examples/go.mod` — `event/projection` is a root-module package | S3 | `check-workspace` refuses a stray line; `check-tidy` refuses a `require` nothing imports |
| 6 | `scripts/event_test.go` — `charged[eventExtension + "/projection"] = "./runtime"` **and** the stale doc comment | S3 | `TestNoEventPackageCostsMoreThanTheSeamItNames` fails on `len(packages) != len(charged)+1` the moment the package exists |
| 7 | `docs/modules/{en,ru}/projection.md` + both `Index.md` rows | S5 | nothing — this checklist |
| 8 | the four contract rows in both pages, **plus the two [[D-128]] rows** — the ordering row and the watermark row | S5 | this checklist; the delivery row is also read by `TestNoDocPromisesExactlyOnceDelivery` |
| 9 | `docs/modules/{en,ru}/eventpg.md` — the checkpoints table, schema version 2, the migration, and **the corrected paging number** (17.6 ms over 391 pages, not 6 ms) | S5 | this checklist |
| 10 | `docs/modules/{en,ru}/event.md`, `eventmemory.md`, `eventtest.md` — the new surface, and **the tracker's window**: what a `Load` after a save is allowed to answer, that a save the store never confirmed is settled by one `Load` and not by a second save, and that a tracker whose row moved under it is finished rather than re-seated | S5 | this checklist |
| 11 | `event/eventpg/MIGRATIONS.md` — the v1→v2 row, that v2 is safe in one transaction, and the fingerprint guard | S5 | this checklist |
| 12 | `FL-038` with a source-file row for **every** non-test file in `event/projection` (nine), the new files in `eventmemory`, `eventpg` and `eventtest`, each naming its symbols | S5 | `TestEverySymbolTheDocsCiteIsDeclaredWhereTheDocSaysItIs` |
| 13 | FL-038's row in the flow table, its question row in "what am I looking for", and **a row per file in the reverse `By file` index** | S5 | nothing — this checklist. This is the row phase 1 missed thirteen times |
| 14 | FL-036 and FL-037 rows for the kernel and store files that changed | S5 | this checklist |
| 15 | §INV-021's **eighth** hand-off, appended | S5 | this checklist |
| 16 | `docs/ai/decisions/D-129…D-132` + four `Index.md` rows; **D-128 is already on disk** and what S5 owes it is the two module-page rows and the frozen manifest | S5 | this checklist |
| 17 | `docs/ai/usecases/Index.md` — the `UC-032` row extended | S5 | this checklist |
| 18 | `docs/roadmaps/Roadmap.md` E2 **rewritten**, carrying the snapshot deferral, the measured number and the re-entry trigger | S5 | `scripts/roadmap_test.go` reads activations, not this |
| 19 | `_examples/event-checkpoints-elsewhere/` (D8) | S5 | `make examples` |
| 20 | `make api` regenerated and its diff read | S5 | nothing, and nothing should |
| 21 | `## P3` backlog appended with what this plan raises, and §9's text amended to record that the `Sibling` gate **moved rather than went** | S5 | this checklist |
| 22 | `make check` green including `check-event-kernel` at the new manifest | S5 | itself |
| 23 | `event/eventpg/census_integration_test.go` extended to the twelve checkpoint sections, and `withdrawals` moved from 3 to 5 | S2 | `TestTheDefectInventoryIsTheSizeItSaysItIs`, which fails in both directions; and S2's live count arm, which names both census tests |
| 24 | `scripts/projection_test.go` — the five walks INV-066, INV-072, INV-077 and INV-079 name | S5 | S5's count arm, which is the only thing that turns an unwritten walk into a red line |

---

## The conformance extension, and the defect that falsifies each section

A section nobody can break is a section that certifies nothing. Every new section
lists the defect that must fail it, on the pattern
`event/eventtest/defects_*.go` already uses, and the harness drives all of them
against both shipped stores in S2's checkpoint.

| Section | Certifies | Self-falsifying defect |
|---|---|---|
| `binding` | the three pure answers are constant for the store's life | a `Checkpoints` whose `Capabilities` or `Backing` differs between two calls |
| `absence` | `Load` of an unknown name answers the zero checkpoint, `Fresh()` is true, and it is not an error | one that reports absence for a row that exists; and one that answers an *error* for an absent name |
| `round trip` | a saved cursor and progress load back identically, through a second value | one answering the cursor saved before the last one |
| `fence` | a save lands iff the stored advance is one below; a loser is `Conflict` | one that ignores the fence and always lands |
| `forget` | `Forget` removes one name, answers nil for an absent one, and touches no other name's row | one that truncates the table |
| `names` | two names saved, each `Load`ed, each answering **its own** row | one whose `Load` ignores its `projection` argument |
| `bounds` | a cursor of exactly `MaxCursorBytes` saves and loads; a projection name of exactly `MaxNameBytes` does; **an empty cursor is refused and no row moves** (D12) | one that silently truncates either; and one that accepts the empty cursor and reports it saved |
| `refusal classes` | every failure is nil, a bare context error, or an `event.Failure` carrying one of the seven | one that returns a bare sentinel of its own |
| `lifecycle` | `Close` is idempotent, closes nothing it did not open, and every later call refuses | one whose `Close` panics on the second call |
| `concurrency` | N goroutines saving at one advance leave exactly one winner | one that serialises by reading then writing, which leaves two |
| `transactions` | a save inside the caller's transaction is invisible outside it and vanishes on rollback | one that saves outside the caller's transaction |
| `durability` *(needs `Persistence`, and `Factory.Sibling` with it)* | a row written through one value is read through a second over one backing | one whose rows live on the value rather than the backing |

The five [SPEC] §UC-123 names are the fence, the stale cursor, the false absence,
the ignored `projection` argument and the escaped transaction; the other seven are
the plan's, and each is written the same way — the defect is applied, the run must
report **that** section failed, and a control run with the honest store must report
it passed.

**Two of the twelve are gated, and a gate is not a pass.** `transactions` runs
only for a store claiming `Transactions` and `durability` only for one claiming
`Persistence`; a store claiming either and supplying no hook fails the run before
a section starts. So `eventmemory.Checkpoints` is certified on eleven and reports
`durability: not certified`, and that pair of facts — eleven passed, one declined,
for the memory store; twelve passed for live `eventpg` — is what the census arm
in S2 asserts rather than leaves to a reader of `t.Log` lines.

---

## The snapshot decision, measured first

**A snapshot does not ship in phase 3.** The measurement was taken before this
paragraph was written (§What was measured, 1) and is the reason rather than the
decoration:

1. **The measured cost does not justify it at the sizes this framework's own rules
   encourage.** 10 – 18 ms for a 10 000-event aggregate is inside the budget of a
   single request. An aggregate that reaches 100 000 events has a modelling problem
   the roadmap already names — a consistency boundary drawn too wide — and a
   snapshot would hide it rather than fix it.
2. **What it costs is correctness, not code.** A snapshot is a second answer to the
   question §INV-008 says has one, and every part of it is a way to serve a wrong
   state silently: a stale one, a corrupt one, one whose revision this build cannot
   read, and — the one the framework structurally cannot detect — one taken with a
   fold that has since changed.
3. **The roadmap's gate is a measured *need*, not a measured *cost*.** There is no
   consumer, and phase 3 has four required mechanisms competing for the same review
   rounds.

**What ships instead is the instrument**, so the decision is reversible from
evidence: `BenchmarkStreamReplay` in `event/eventpg` (§UC-125), which replays a
stream of a caller-chosen size against a live database and reports ns/event.

**The recorded re-entry trigger, and its owner** (§12.8): a snapshot is built when
**a deployment** measures its own p99 aggregate replay above **~50 ms in its own
environment** — at these rates somewhere above 30 000 – 50 000 events, or an order
of magnitude fewer over a 1 ms link, because the 391-page term measured here at
17.6 ms grows to roughly 390 ms there. The measurement is the deployment's, the
instrument is the benchmark, and the trigger is written into
`docs/roadmaps/Roadmap.md` and D-132 so the deferral has an owner rather than a
hope.

**No memo and no cache is added instead.** §INV-042 already forbids the kernel
retaining an application value across a call boundary, and a per-request cache is a
snapshot with no version, no invalidation rule and no corruption story — the same
second authority with none of the four things that make one safe.

§5.4's contract — what is versioned, what invalidates one, what a corrupt one does,
and how it is proved — is recorded and unchanged, so the later phase implements a
decision rather than re-deriving one. UC-126 and UC-127 stay **deferred**.

---

## Debt

Deferred, never dropped. Referenced by backlog entry rather than restated, per the
delivery policy.

**Carried into phase 3** — [`EVENTSOURCE_BACKLOG.md`](../gaps/EVENTSOURCE_BACKLOG.md):

- **`## P2` §5**, `[medium]` — `check-event-kernel` outside a git checkout, and no
  stated re-baselining rule. **Closed by this plan**, both halves: the manifest arm
  needs no git, and the re-baselining rule is a command the failure message prints
  and a file list this plan fences every section against.
- **`## P2` §25**, `[high]`, owner *the phase that unfreezes the kernel* — a factory
  whose `New` answers a store over a backing an earlier `New` wrote to cannot pass
  `store failure classification`. **Not closed by this plan and said so out loud.**
  It is a defect in `event/eventtest`'s `Factory.New` contract, phase 3 unfreezes
  the kernel and therefore *could* fix it, and it is left because closing it means
  restructuring `storeFailureSection`'s two `through` variants — which is a change
  to the section that certifies the write path, in a phase whose evidence rests on
  that section still being the one phase 2 ran. It stays open with its owner
  changed to **phase 4**, and this paragraph is the record that it was considered
  and declined rather than missed.
- **`## P2` §24**, `[medium]` — the `Log` read door carries no wiring class, so
  §UC-086's "`ErrAmbientNotTransaction` from every door" holds at three of four.
  Reachable now that the kernel is open; **left alone** under the policy.
- **`## P2` §26**, `[medium]` — no decode-side depth bound on a stored payload.
  Belongs to the codec seam in `event/`, reachable now; **left alone**.
- **GAP-T5**, `[medium]`, assigned to phase 2 and blocked by its zero-diff rule —
  §INV-011's two disclosed one-hop escapes, whose fixture pair lives in
  `event/refusalmessages_test.go`. Reachable now; **left alone**, and the new
  refusals this phase writes are held to the same rule by hand: **no refusal in
  `event/projection`, `event/checkpoint.go`, `eventmemory/checkpoints.go` or
  `eventpg/checkpoints.go` renders a key, a payload, a position or a cursor.**

**Raised by the phase-3 spec gate and left alone** — `## P3` §1 through §15, each in
that file. Four are touched in passing by a decision above and **none is worked**:

| Entry | Severity | Touched by | Still open because |
|---|---|---|---|
| §4 | medium | D10b/D4 name the pass context and resolve §UC-118's two must-nots into an order | no `Spec.Timeout` is added, and the doc work is the entry |
| §5 | medium | the `updated_at` bound parameter removes the two-clock defect; `Applied` and `Quarantined` are defined as cumulative envelope counts | the doc and case work is the entry |
| §12 | low | the observer panic is recovered, matching `runtime.observing` | the doc sentence is the entry |
| §13 | low | `checkPage` takes the cursor, named in the baseline table | nothing else is owed |

The other eleven — §1, §2, §3, §6, §7, §8, §9, §10, §11, §14, §15 — are untouched.
Of those, §7's argument about refusing a `Unit` beside `AfterApply` and §9's
`Sibling` gate are the two the plan **states its code's answer to** without closing
the entry, because a plan that shipped either without saying what the code does
would have the implementer invent it. **§9's text is amended in S5 to record that
the gate moved rather than went**: `Sibling` is required by `Persistence` and its
absence is fatal, `durability` is gated on the capability, and the entry's real
question — that `Persistence` and "two values over one backing" are two claims
and `CheckpointCapabilities` carries one — is exactly as open as it was. Amending
an entry to say where its subject now lives is recording it, not working it.

**Raised by this plan**:

- **`## P3` §16**, `[medium]`, owner **phase 4** — [SPEC] §5.1's claim that the
  single-statement scan is 92–94 % of the paged replay is 83 % on this host, so
  "391 pages add about 6 ms" is really about 17.6 ms. The conclusion is unchanged
  and the module page carries the measured number, but the spec's table is now the
  one place the old figure survives.
- **`## P3` §17**, `[low]` — `Fact.Read` bounds the payload against
  `MaxPayloadBytes` rather than against the store's own `MaxPayload`, because a
  `Fact` holds no store's limits. A projection over a store with a narrower payload
  bound is therefore protected by the store's read door and not by the kernel's
  second check. No shape closes this without threading a store's limits through a
  declaration.
- **`## P3` §35**, `[low]`, owner **phase 4** — [SPEC] §3.1's table is now the one
  place `cursor text` survives, and its `CHECK (octet_length(cursor) <= 4096)` the
  one place the cursor is bounded only above. D11 and D12 override both, with the
  measurements; the spec is frozen semantics and is not edited, so the drift is
  recorded rather than repaired.

**What the plan overrides in [SPEC], listed once so it is not found in a diff.**
Two things, both in §3.1's DDL table, both decided with a live measurement above:
the `cursor` column is `bytea` and not `text` (D11), and it is bounded below as
well as above (D12). Nothing else in the frozen semantics moves — no use case, no
invariant, no signature in §9, and no section of §10.

**Three passages of [SPEC] §3.1 and §9.4 were amended in place** by S2's round-1
remediation, and that is a departure from the rule above rather than an exception
to it: GAP-1, GAP-2 and GAP-3 are findings that the *contract* was wrong, not the
code, and leaving the defective save statement standing as the published contract
would have a third-party implementer copy it. Each amendment says so where it
sits — the save's two statements and the interleaving that forced them, the
`Instant` hook and the ten sections a correct store failed without it, and the
sentence that absence is total inside a unit that staged a removal. No use case,
no invariant and no signature moved with them.

**What this plan deliberately does not do.** It writes no snapshot, no outbox, no
broker delivery, no `eventfx` and no `projectionfx`. It publishes no importance and
imports no `health` ([[D-091]]). It writes to no logger (D9). It starts no goroutine
outside `runtime.Runner`'s own contract, and no goroutine at all in
`event/projection`'s non-test files. It adds no sentinel and no class. It moves the
kernel baseline exactly once per kernel-touching section, by regenerating a manifest
whose diff names every file that moved — and it never widens the arm's pathspec,
never adds an exclusion for its own new files, and never turns the arm into a
warning.
