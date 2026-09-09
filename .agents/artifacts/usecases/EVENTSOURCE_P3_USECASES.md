# EVENTSOURCE PHASE 3 — PROJECTIONS, CHECKPOINTS, CATCH-UP AND SNAPSHOTS

**Status:** specification, phase 3 of the PostgreSQL event-sourcing roadmap.
**Written against:** `event`, `event/eventmemory`, `event/eventtest` and
`event/eventpg` as they stand, `runtime` and `jobs` as the accepted precedents,
PostgreSQL 17.9.
**Numbering:** continues phase 2. Use cases start at UC-099, invariants at
INV-066, so a bare `UC-0nn` or `INV-0nn` is unambiguous across all three
documents.

## 0. What this document is, and what it does not repeat

Phases 1 and 2 are frozen and are the input to this one:
[`EVENTSOURCE_P1_USECASES.md`](EVENTSOURCE_P1_USECASES.md) for the semantics,
[`EVENTSOURCE_P2_USECASES.md`](EVENTSOURCE_P2_USECASES.md) for what a PostgreSQL
store adds, the plans beside them for the contracts as built, and the code for
what they actually are. **Nothing already written there is restated here.** Where
a rule exists it is referenced by number and the reference is the whole of what
this document says about it.

**Delivery policy in force (set 2026-09-08).** Only `[critical]` and `[high]`
findings block. `[medium]` and `[low]` are appended to
[`EVENTSOURCE_BACKLOG.md`](../gaps/EVENTSOURCE_BACKLOG.md) under `## P3` and left
alone. A gate never proceeds silently red, and a section is reported by a
reviewer's verdict rather than by `go test` being green.

**Phase 3 moves the frozen kernel, deliberately.** §9.1 enumerates every file
under `event/` outside `event/eventpg` that changes and why, and `make check`'s
`check-event-kernel` arm is re-baselined in the same change rather than
loosened.

---

## 1. What a checkpoint means when visibility is not monotone

Everything else in this document is downstream of this section, so it is first.

### 1.1 The shape that is wrong, and why it is the tempting one

`eventpg` declares `MonotoneVisibility: event.Unsupported` (§2.6, §INV-054). A
PostgreSQL identity column draws a position when a row is inserted and not when
it commits, so at the instant a reader holds position 7, position 5 may belong to
a transaction that is still running and will commit later. The store answers this
with a settled watermark: it delivers past a gap only once it has proved every
transaction that could still fill that gap has finished, and its cursor carries
the three numbers `(from, bound, reach)` that record the proof
(`event/eventpg/read.go`, `event/eventpg/cursor.go`).

A checkpoint spelled **"I have consumed up to position N"** is therefore a naive
contract on the store this framework actually ships, in three separate ways, and
each of them loses events silently:

1. **There is no N below which everything is delivered and above which nothing
   is.** A walk that stopped at a gap has delivered everything at or below
   `from`, and it knows the gap at `from+1` is *not yet* settled. A single number
   cannot carry "and I proved nothing about the gap".
2. **A number cannot be turned back into a resume point.** Resuming needs the
   bound and the reach; without them the store has to re-derive settlement from
   scratch, and the only rule that could re-derive it cheaply —
   `pg_snapshot_xmin = pg_snapshot_xmax` — was *measured false* on PostgreSQL
   17.9 while phase 2 was being written (§2.6). A consumer that stored `N` and
   resumed at `position > N` skips every position that was in flight when it
   stopped.
3. **A number invites arithmetic.** `N - safetyMargin`, `N + 1`, `max(N, M)`,
   "projection A is ahead of projection B". The lag a watermark carries is
   bounded by the oldest open write transaction, not by a count or a duration, so
   no margin is safe and no two consumers' numbers are comparable as progress.

Phase 1 already refused the general form of this: a cursor is opaque, no
arithmetic, ordering or derivation is defined on it, and **no caller derives one
from a position** (§UC-037, §INV-035, §UC-053). A checkpoint that stored a
position would need a conversion from a position back to a cursor, and there is
deliberately no such conversion anywhere in this framework.

### 1.2 The honest shape

> **A checkpoint is the store's own cursor, held under a name, with a fence.**
>
> ```go
> type Checkpoint struct {
>     Projection string     // whose progress this is
>     Cursor     Cursor     // the store's own resume point — the authority
>     Advance    uint64     // 1 for the first save, +1 per save; the fence
>     Progress   Progress   // observations, and never a resume point
> }
> ```

Four fields, and each earns its place:

- **`Cursor`** is the whole of the resume authority. It is opaque, it was minted
  by the store, it carries whatever that store needs — for `eventpg`, the log
  id and `(from, bound, reach)`; for `eventmemory`, a log fingerprint and a
  position. The checkpoint store persists it as bytes and reads nothing in it.
- **`Projection`** is the name the checkpoint belongs to. It is held to the
  kernel's identifier rule (`checkName`, ≤ `MaxNameBytes`) — the same rule as a
  family and a wire type name — so one rule covers every declared identifier in
  this subsystem and a projection name renders safely in a refusal (§INV-025).
- **`Advance`** is a monotone counter, not a position and not a version of
  anything. It exists so a save is a **conditional row update** rather than a
  blind overwrite: a save carries the advance it was decided at, and lands if
  and only if the stored row is still one below it. That is the same admission
  shape as an expected-version append (§INV-046), so a checkpoint row is a
  one-row stream and needs no second vocabulary — a losing save is
  `event.Conflict` and the kernel maps it to `ErrConflict`.
- **`Progress`** is `{Highest Position, Applied uint64, Quarantined uint64,
  At time.Time}` and is **an observation**. It is what an operator's dashboard
  reads and what a rebuild is compared by (§6). It is written down beside the
  cursor because the demand for a number is real and refusing it entirely is how
  a number gets invented somewhere worse. Nothing in this framework accepts a
  `Progress` where a `Cursor` is wanted, and there is no function anywhere from a
  `Position` to a `Cursor`.

  **A `Progress` is computed at exactly one point: the save.** It describes the
  page the checkpoint beside it names, and it is not touched anywhere else —
  not at the read, not after `Apply`, not on a retry, not on an empty page.
  `Highest` is therefore the highest position of a page that was **applied or
  quarantined**, never of one that was merely delivered, and `State().Progress`
  is the last saved value rather than a running one. A projection that is
  retrying or halted on the page it just read publishes the progress it had
  before that page, which is the only number that is true of its destination.

### 1.3 What a consumer may assume

Exactly this list, and it is short because most of it is inherited rather than
new:

1. Resuming a walk from `Checkpoint.Cursor` returns **every event that walk had
   not delivered**, in ascending position, none skipped — §INV-035, which is
   mandatory for every store the contract admits, including one that cannot
   promise monotone visibility.
2. Re-reading from one cursor is **stable in its prefix**: the events delivered
   are the same ones in the same order, and only the *number* of them may grow
   as settlement advances. A retry after a crash therefore replays the same work
   and never a different subset.
3. Once a walk has delivered position `H`, **every event at a position ≤ `H`
   that is or ever becomes visible has already been delivered by that walk.**
   This is a theorem of the contract rather than a new promise: under monotone
   visibility it follows from visibility being in position order, and under a
   watermark it follows from the walk never passing a gap it has not proved
   burnt. It is stated over **delivery**, which is all a walk knows about.
   `Progress.Highest` is written only at a save and a save happens only for a
   page that was applied or quarantined (§INV-067), so `Highest ≤ H` always and
   the theorem carries: every committed event at or below a *saved* `Highest`
   was delivered, and every one of them was applied or was recorded in a
   quarantine sink. That is what makes `Highest` a sound *consumption*
   comparison between two projections over one log (§6) even though it is not a
   resume point — and §6 is careful that consumption is not content.
4. A checkpoint is safe to persist and to resume from **in another process**, and
   a store over another log refuses it (`ErrCursor`).

### 1.4 What a consumer must never assume

- That `Cursor` orders. `event.Cursor` is a defined string, so `<` compiles;
  phase 1 recorded that cost (§UC-053) and phase 3 adds nothing that makes it
  meaningful. No API takes two cursors.
- That a checkpoint names a position, a time, or a count of events. `Progress` is
  the only thing that names any of those and it resumes nothing.
- That "all events at or below the checkpoint have been applied". The sound form
  is (3) above, and it is stated over `Progress.Highest`, not over the cursor.
- That two projections' checkpoints can be compared. Two cursors over one log
  may carry different bounds for the same delivered prefix.
- That an unreadable checkpoint means "start from the beginning". It means the
  projection does not know where it is, and a projection that restarted from the
  origin would re-apply the whole log (§UC-091, §UC-101).
- That a checkpoint the store returned on an **empty** page is worth persisting.
  It is legal to persist and phase 3 deliberately does not (§INV-067).

### 1.5 The alternative that was rejected, and why it is recorded

The other shape a reviewer proposes is the **gap-tracking checkpoint**: a low
watermark plus the set of positions above it that have been delivered — a
position and a bitmap, advanced as gaps fill. It is what several event-sourcing
libraries do and it is rejected for four reasons, in ascending order of how
final they are:

1. It re-implements, in every consumer's storage, the watermark the store
   already computes — and `eventpg`'s costs one transaction id, only when a gap
   is seen (§2.6).
2. The outstanding set is unbounded in exactly the case that matters: its size is
   bounded by the oldest open write transaction, which an application can hold
   open for an hour.
3. It is store-specific in disguise. Over a store with monotone visibility the
   set is always empty, and the shape still teaches every consumer that a
   checkpoint is made of positions.
4. **A consumer cannot build one.** Distinguishing "burnt by a rollback" from
   "in flight" requires `pg_current_xact_id()` and `pg_snapshot_xmin`, which is
   precisely the reading only the store can make. A consumer-side gap set would
   have to guess, and the safe guess — never settle — stalls forever while the
   unsafe guess skips committed events silently.

Also rejected, for the record, because they are the two lines somebody will
write: **`position - safetyMargin`**, which converts a correctness property into
a tunable that no value makes correct; and **"start from now"**, which has no
sound O(1) reading (§2.6, phase 2 non-goal 5) and which, offered to a
first-time consumer, silently skips its whole backlog.

---

## 2. Scope

### In scope

1. **The checkpoint contract in the kernel** — `event.Checkpoint`,
   `event.Progress`, `event.Checkpoints`, `event.Track` as the door that
   re-checks what a checkpoint store answers, and the ceiling a persisted cursor
   is held to.
2. **Two implementations and a conformance suite for them** —
   `eventmemory.Checkpoints`, `eventpg.Checkpoints` (schema version 2), and
   `eventtest.RunCheckpoints`, so a third is provable.
3. **`event/projection`** — the idempotent, checkpointed, at-least-once consumer:
   one loop that resumes, drains, follows, retries, halts and drains again on
   shutdown, as a `runtime.Runner` the host supervises ([[D-092]]).
4. **The typed reading seam** — `Fact.Read`, so a projection decodes a stored
   envelope through the declaration that wrote it without reflection, and
   `projection.Router`, so an event type nobody handled is a decision rather
   than a missing `case` — and, inside a family the router does route, a
   **refusal** rather than a decision, because there it is a forgotten
   registration and not a choice (§3.3, §INV-081).
5. **Rebuild and cutover** as a documented composition of what 1–4 already give,
   plus the one comparison that is sound between two projections.
6. **The replay-cost measurement** that the roadmap makes the precondition for a
   snapshot — run, reported in §5, and shipped as a benchmark a deployment runs
   against its own aggregate.
7. **Closing `## P2` §59** in `event/eventtest`: the `resumption` section never
   walks the log while a lower position is uncommitted, so the one defect that
   separates a settled watermark from the newest position is invisible to the
   suite that exists to certify stores. That entry names "the phase that
   unfreezes the kernel" as its owner and a checkpoint is a promise about
   exactly that read, so it is phase 3's.

### Non-goals — named, not specified

Phases 1 and 2's non-goal lists carry over whole. These are phase 3's own:

1. **No snapshot implementation.** The contract is specified (§5.4) and the
   decision not to ship it is made from a measurement (§5.1–5.3), not from
   silence. Full replay stays the only authority (§INV-008, §INV-079).
2. **No outbox and no broker delivery.** Phase 4, and [[D-118]] already decided
   the shape.
3. **No codegen and no container binding.** No `eventfx`, no `eventpgfx`,
   no `projectionfx`. Phase 5.
4. **No OpenTelemetry, no tenancy, no audit.** A projection reports through an
   `Observer` the composition root supplies, and imports none of the three.
5. **No `LISTEN`/`NOTIFY` and no long-poll.** Following is a poll with a
   process-local wake channel; a notification path needs a dedicated connection
   and a second failure mode, and has no consumer yet.
6. **No exactly-once, anywhere, in any wording.** §INV-072, [ES]'s ninth
   non-negotiable, [[D-118]]'s words for jobs, and the repository's existing test
   that fails a doc promising it.
7. **No `WaitUntilCaughtUp`, and no read-your-writes helper.** §4.3 argues it.
8. **No parallel or partitioned projection.** One projection applies one page at
   a time in position order; sharding a projection by stream is a real feature
   with its own ordering questions and it is not this phase's.
9. **No retention, archival or checkpoint sweeper.** A checkpoint row is named
   and tiny; `Forget` is the whole of its lifecycle.
10. **No new module.** `event/projection` takes no third-party dependency, so it
    is a package of the root module: no `go.work` line, no `replace` lines in
    `test/go.mod` or `_examples/go.mod`, and nothing for `make check-replaces`
    to find. What it does need is a row in `scripts/event_test.go`'s `charged`
    map (§11.3).

---

## 3. The five mechanisms

### 3.1 The checkpoint store

```go
type Checkpoints interface {
	Capabilities() CheckpointCapabilities
	Backing() Backing
	Transaction(ctx context.Context) (Authority, error)

	Load(ctx context.Context, projection string) (Checkpoint, error)
	Save(ctx context.Context, checkpoint Checkpoint) error
	Forget(ctx context.Context, projection string) error
	Close() error
}
```

Seven methods, and the shape is `event.Store`'s on purpose — a party that has
implemented one has implemented most of the other.

- **`Load`** answers the zero `Checkpoint` when there is none. `Advance == 0` is
  the absence, unambiguously, because `Save` refuses a zero advance; `Fresh()`
  is the predicate and there is no second return value to misread. **Absent and
  unreadable are different answers**: absent is a first run and starts at the
  origin; a cursor the log's store cannot read is `ErrCursor` at the first read
  and the projection halts (§UC-101). **Inside a unit of work that has staged a
  `Forget`, the answer is the zero `Checkpoint` too** — absence is total there
  exactly as it is outside one. S2's round-1 review (GAP-3) found `eventmemory`
  answering the name beside no advance, which is the half-absent row
  `Tracker.admit` refuses as `ErrWrongStore`; the removal travels as an
  advance-zero staged entry, and handing that entry to a caller is not an
  answer to the question.
- **`Save`** is a conditional row update: it lands if and only if the stored
  advance is `checkpoint.Advance - 1`, or there is no row and
  `checkpoint.Advance == 1`. Zero rows is `event.Conflict`. There is no
  read-then-write anywhere.
- **`Forget`** removes a name. It is not fenced and it does not need to be: a
  running projection's next save finds no row at its advance, is refused, and
  halts — which is the correct and visible outcome of retiring a live
  projection, rather than a silent reset to the origin.
- **`Transaction`** answers the same three answers and no fourth as
  `Store.Transaction` (§2.4). It is what makes the checkpoint half of the
  `InUnit` claim checkable rather than assumed: the projection asks it — through
  the tracker — *inside* the unit and refuses the pass if what comes back is not
  a valid authority. It says nothing about the handler's half, and §3.4 is where
  that is stated rather than left to be inferred from this one.
- **`Capabilities`** carries two supports and not four. `Transactions` gates
  `InUnit`. `Persistence` is what tells a projection over a memory checkpoint
  store that a restart re-applies the whole log — an honest answer that a
  fourth field would not improve.

`Backing()` is the checkpoint store's own, not the log's. The two may be one
resource (`eventpg`: the same database and schema) or two (a checkpoint table in
a read model's own database), and **the log's and the checkpoint store's are
never compared** — the projection reads the log outside every transaction
(§INV-071), so the two authorities never meet. The comparison that does matter
is the other one, between the checkpoint store and the **destination the handler
writes to**, and §3.4 is where it is stated and §3.2(3) where it is checked.

#### The door: `event.Track`

Nothing calls a `Checkpoints` raw. `event.Track(checkpoints, name)` answers a
`*Tracker`, which is to a `Checkpoints` exactly what `*Reader` is to a `Log`:
the one door, holding the name, owning the fence, and re-checking every answer
the store gives. Phase 1's rule is unchanged and the reason is unchanged with it
— *a store is trusted exactly as far as its numbers are, which is not at all*
(`event/store.go`) — and a checkpoint store is a second store interface with an
invitation for third-party implementations, so it needs the same door rather
than the same sentence.

What `Track` refuses at construction: a nil `Checkpoints`, and a name the
kernel's identifier rule refuses (`checkName`, ≤ `MaxNameBytes`).

What `Tracker.Load` re-checks on the answer, before any of it reaches a walk:

- **Absence is total.** `Advance == 0` if and only if the cursor is empty, the
  progress is zero and no projection is named. A row that is half-absent is not
  a fresh start with some extra fields; it is a store that did not answer the
  question.
- **The answer is about the name that was asked for.** `Checkpoint.Projection`
  equals the tracker's name. This is the check the whole door exists for: a
  `Load` that ignores its argument, or whose statement is mis-parameterised,
  answers another projection's *well-formed* cursor — of the right log, in the
  right format, refused by nothing — and the walk resumes hundreds of thousands
  of positions ahead and skips everything in between, forever, with no error on
  any path (§INV-082).
- **The cursor is inside the published ceiling** (`MaxCursorBytes`), so a row
  written by something that is not this kernel is caught at the door rather than
  at the store that has to size a column.

Any of the three is `ErrWrongStore`, which is the wiring class and is terminal
(§3.4): a page that does not ascend is a bad answer to a good question and can
be retried, while a row that is not this projection's means the store and the
name do not belong together, and no retry changes that.

`Tracker.Save(ctx, cursor, progress)` takes no advance. **The fence is the
door's**: the tracker holds the advance it loaded, presents one above it, and
refuses a save before a load — so no caller can present a wrong advance, and
`Checkpoint.Advance` is a field the *store* reads rather than one a caller
computes. `Tracker.Transaction(ctx)` is the per-pass re-check §3.2(3) names, and
`Forget`, `Capabilities` and `Backing` pass straight through, so the projection
never holds the raw `Checkpoints` at all.

**A checkpoint store classifies its own failure and adds no sentinel** — the
same rule as a store, the same `event.Failure(Outcome, cause)` channel, the same
seven outcomes, mapped by the same kernel function ([[D-122]], §INV-045). Phase
3 therefore adds no sentinel and no class to the twenty-four (§INV-076).

#### `eventpg.Checkpoints` — schema version 2

One new table in the existing schema, and therefore a schema version bump with
everything §2.2 already requires of one: a new fingerprint, a `MIGRATIONS.md`
row, a v1→v2 statement list whose last statement asserts on the version it
migrated from, and level-3 verification extended to the fourth table with the
same set-equality classes (§INV-057).

```
<schema>.checkpoints   projection  text        NOT NULL PRIMARY KEY
                                               CHECK (octet_length(projection) BETWEEN 1 AND 128)
                       cursor      text        NOT NULL CHECK (octet_length(cursor) <= 4096)
                       advance     bigint      NOT NULL CHECK (advance > 0)
                       highest     bigint      NOT NULL CHECK (highest >= 0)
                       applied     bigint      NOT NULL CHECK (applied >= 0)
                       quarantined bigint      NOT NULL CHECK (quarantined >= 0)
                       updated_at  timestamptz NOT NULL
```

`quarantined` is persisted rather than kept in memory for the same reason
`applied` is: it is what tells an operator, after a restart, that the read model
this checkpoint accounts for has known holes in it, and a cutover decided
without it is decided on half the number (§6).

The save is one statement, on the append's own shape — but **which** statement is
the advance's, because a save is one decision and not two. The create path and
the move path are separate:

```sql
-- advance = 1: create, and never overwrite
INSERT INTO <schema>.checkpoints (projection, cursor, advance, highest, applied, quarantined, updated_at)
VALUES ($1, $2, $3::bigint, $4::bigint, $5::bigint, $6::bigint, $7::timestamptz)
ON CONFLICT (projection) DO NOTHING

-- advance > 1: move, and never create
UPDATE <schema>.checkpoints
   SET cursor = $2, advance = $3::bigint, highest = $4::bigint,
       applied = $5::bigint, quarantined = $6::bigint, updated_at = $7::timestamptz
 WHERE projection = $1 AND advance = $3::bigint - 1
```

**Amended after S2's round-1 review (GAP-1), and the reason is a measured
defect, not a preference.** The single `INSERT … SELECT … WHERE $3 = 1 OR
EXISTS (…) ON CONFLICT DO UPDATE` this section first specified asks whether the
row is there against the statement's own READ COMMITTED snapshot and asks which
row it collides with against the live index. A `DELETE` committing between those
two moments makes the `EXISTS` true and the conflict absent, and PostgreSQL
performs a plain insert: driven live against PostgreSQL 17.9 with a `Forget`
held in an open transaction, a row at advance 5 was removed, the projector's
save at advance 6 answered `INSERT 0 1` and `nil`, and a row existed at advance
6 that no advance-1 save created. That defeats the very property this section
gives for leaving `Forget` unfenced, and in the §6 cutover it is a retired
projector still writing into a read model an operator watched it be cut over
from. Split, neither half can do it: an `UPDATE` matches nothing where the row
has gone, and `DO NOTHING` never overwrites one.

Every property §2.3 argues for the append holds here for the same reasons and is
not re-argued: one statement so atomicity is not borrowed from a transaction;
admission is a predicate PostgreSQL evaluates against a row it has locked;
`RowsAffected() == 0` is the conflict and any other count is unclassified; the
store selects no isolation level; it opens, commits and rolls back nothing
(§INV-051); and it issues every statement on a `*sql.Conn` it checked out or on
the caller's `*sql.Tx`, never on the pool (§INV-053).

**Three tables were append-only and the fourth is not**, deliberately: a
checkpoint is not history. The append-only trigger stays on `events` alone, and
level 3 compares the new table's expected trigger set as empty — so a trigger
added to it is caught by the comparison that already exists (§UC-072).

`event.MaxCursorBytes` (4096) is the kernel's ceiling and the `cursor` column's
operand. It exists because a checkpoint store has to size a column and nothing
bounded a cursor before. It is enforced where store honesty is already checked —
`Reader.Next` refuses a log that mints a cursor over it, the same shape as
refusing a page longer than `MaxRead`, and `Tracker.Load` refuses a checkpoint
store that answers one — so a checkpoint store never has to refuse a legitimate
cursor. It is **not** configurable, unlike `MaxPayload` and
`MaxKey`: a bound a deployment could lower is one that refuses a cursor its own
store mints. `eventpg` mints 58 bytes and `eventmemory` about 30.

#### `eventmemory.Checkpoints`

`Persistence: Unsupported`, `Transactions: Supported`. It joins the ambient
`*eventmemory.Tx` for the log it was built over, which is what lets the
projection package prove the `InUnit` path with no database at all. Its
`Backing` is minted over its own container, so two values over one container are
one checkpoint store — which is what `RunCheckpoints`'s `Sibling` hook needs —
and a cursor stored by a projection over another log is refused where every
foreign cursor is refused: by the log's store, at the first read.

A projection over it proves the `InUnit` path with `Destination:
projection.Unchecked`, because a read model in a Go map has no `crud` binding to
resolve. That is the honest spelling of what such a test is, and it is worth
noticing that the framework's own in-memory proof takes the escape rather than
pretending to a check — which is why §UC-105 and §UC-128 are live cases against
a real database and not memory ones.

### 3.2 The projection

One type, one loop, and it is the catch-up subscription too (§4).

```
resume  ── Tracker.Load ────────────────────────────────────► a cursor, or the origin
  │
loop    ── read one page from the cursor, OUTSIDE every unit of work
  │        │
  │        ├─ page is empty ──► Following: wait on the ticker, the wake channel
  │        │                    or the context; do not save; read again
  │        │
  │        └─ page is not empty ──► Draining
  │             │
  │             ├─ AfterApply:  Apply(a copy)        then  Tracker.Save
  │             └─ InUnit:      Unit(check the destination, Apply(a copy), Tracker.Save)
  │             │
  │             ├─ applied ──► advance in memory, read again immediately
  │             ├─ retryable ──► Retrying: back off, apply the SAME page again
  │             │                          (a fresh copy of it, §3.3)
  │             └─ permanent ──► Halted, and then nothing at all
  │                              (or the isolation pass, if a sink is supplied)
```

Six properties of that loop are load-bearing and each has an invariant:

1. **The read is outside every unit of work** (§INV-071). It is not a style
   preference: `eventpg`'s walk mints no settlement bound while a transaction of
   its backing is bound, and at `REPEATABLE READ` the snapshot is frozen anyway,
   so a walk inside the projection's own write transaction stops at the first
   burnt gap and stays there for the life of the transaction
   (`event/eventpg/read.go` step 7, §UC-092). A projection that opened the unit
   first would deadlock its own progress on the first rolled-back append in the
   log, in production, with every test over a gapless log green.
2. **The framework opens no transaction** (§INV-027). `Spec.Unit` is a
   `func(ctx, work func(ctx) error) error` the application supplies —
   `crud.InNewTx(ctx, source, work)` is the one-line spelling for a `crud`
   application, and an application on anything else supplies its own. The
   projection is the caller here, and the caller's transaction stays the
   caller's.
3. **`InUnit` is never silently downgraded** (§INV-070). Four checks, and the
   fourth is the one the naive version leaves out:
   - at construction, `Unit` is not nil;
   - at construction, `Checkpoints.Capabilities().Transactions == Supported`;
   - at construction, `Spec.Destination` is stated — either the handle the
     handler writes through, or `projection.Unchecked`, which says out loud
     that it cannot be resolved and is not something a caller reaches by
     leaving a field zero;
   - **per pass, inside the unit and before the handler runs**: the tracker
     answers a valid authority, *and* — when `Destination` is a handle rather
     than `Unchecked` — `crud.ExecutorFor(ctx, destination)` finds an executor
     for it and `crud.IsTransaction` says it is a transaction. The second half
     is what catches the wiring §3.4 says `InUnit` is worthless under: the unit
     opens a transaction on the database the checkpoint table is in, and the
     handler writes to a different one.

   A projection that asked for an atomic advance and got a separate one would
   double-apply on every crash and nothing would say so. What the fourth check
   cannot see is stated in §3.4 as an obligation rather than left implied.
4. **The checkpoint advances only for a page the projection finished with**
   (§INV-067). An empty page's cursor is kept in memory — so the walk keeps its
   settling state for the next read — and is not saved. An idle projection
   therefore issues no writes at all, and a restart costs at most one extra
   round trip to re-settle.
5. **A retry re-applies the page it holds, and does not re-read.**
   `Reader.Next` advances the reader's cursor, so the page and the cursor are
   captured together at the read and the retry loop runs over the two values.
   The projection keeps the page it read and hands **each attempt its own copy**
   of it (§3.3), which is what makes "the same page" true of the second attempt
   as well as the first.
6. **A halted projection keeps running and stops advancing.** It does not return
   from `Run` — a returning runner is a failure that by default takes the
   process down ([[D-092]]), and one projection that cannot apply one event is
   not a reason to stop serving. It reports itself through `Ready(ctx)`
   (`runtime.Readier`), which the supervisor aggregates and the composition root
   turns into a `health.Contribution` with the importance it chooses ([[D-091]]).

   **And what it does is exactly nothing.** On entering `Halted` the projection
   publishes the transition **once**, records the failure in `State`, and then
   waits on the context and the drain signal alone. It issues no read, no save
   and no handler call, ever again: no ticker, no wake channel, no poll. A
   halted projection costs the database nothing and the process one blocked
   goroutine. This is stated rather than left to the loop's shape because the
   two plausible implementations — a `for` loop that keeps re-applying the page
   the classifier called permanent, and a poll that keeps a dead projection's
   read traffic on the database — are the same busy loop §3.4 refuses for a
   closed store, and neither is visible to a unit test.

   `Drain(ctx)` returns at once, because there is no pass in flight to finish.
   `State()` and `Ready(ctx)` keep answering the halted state for as long as
   `Run` has not returned. `Run` returns when the context is done, with
   `ctx.Err()` and never with `ErrHalted`.

   **A halt is terminal for that `Projection` value's life.** Nothing clears it:
   there is no `Resume`, no `Retry` and no `Clear`, and the exit is a new value
   in a new process, after the operator has fixed what halted it — §UC-111's
   fixing deploy is the shape, and it is the only one. A halt that could clear
   itself would be a retry loop with a longer period, and the classifier already
   said the failure was permanent.

**The projection holds a `Log`, not a `Store`** (§UC-036, §INV-075), and `New`
refuses a value that is also an `event.Store`, naming `event.ReadOnly`. A
projector that can append is how a replay writes, and phase 1 built
`event.ReadOnly` for exactly this consumer; leaving it advisory is how it stops
being used.

### 3.3 What a page is, and why the handler gets one

`Apply(ctx, Batch)` takes the whole page, not one envelope. The checkpoint
advances per page, so a per-envelope handler would suggest a per-envelope
checkpoint that does not exist, and the atomic unit would be invisible in the
signature. A handler that wants one at a time writes the loop.

The page belongs to the handler from the moment it is handed over, indefinitely,
from any goroutine, capacity included, and the handler may write into it — which
is §INV-021's hand-off rule unnarrowed. `Batch.Attempt` is 1 on the first
delivery of that page and rises on each retry, so a handler that wants to log
loudly on a third attempt can.

**Each attempt receives its own page.** §INV-021 is stated over the hand-off —
*whoever hands mutable memory to another party says whether anybody will write
it again*, and **a sender that is still a reader must make the hand-off safe**.
Across a retry the projection is still a reader: §3.2(5) re-applies the page it
holds. So the projection is the sender that pays, and it pays by handing every
attempt a fresh `[]Envelope` and a fresh copy of every payload in it, keeping
the page the log answered untouched. A handler that sorts, filters, compacts or
redacts the page in place — all legal under the grant — and then fails
retryably is re-applied over what the log answered rather than over what it left
behind; and a handler that fanned attempt 1's page out to workers is writing
memory attempt 2 does not share.

The price is one allocation and one copy of the page's payload bytes per
attempt, the first included. Against §5.1's measured ~1.4 µs per event end to
end, a 256-envelope page of 120-byte payloads is a single ~30 KB copy beside
~360 µs of work.

The alternative — narrowing the grant to "yours for the duration of this call"
— is rejected. It makes a handler that keeps a page, which §INV-038 blesses for
a page a read returns, or one that writes into it, which hand-off 4 blesses for
the same page one level out, lose events silently with no error on any path. A
rule a caller breaks by doing the legal thing is the shape phase 1 refused for
the store's own outbound hand-offs, and the framework does not get to hold it
differently when it is the sender.

This is a hand-off phase 1's enumeration has no row for. It is framework →
handler, and it is the first one whose **sender re-reads what it handed over**;
hand-offs 4 and 7 are about a page the framework will never touch again, so
citing them for this would cite the two rows that do not apply. §INV-021
provides for later rows being appended rather than inserted, and §11.2 records
the appended **hand-off 8** as a documentation deliverable of this phase.

**Typed reading.** `Fact.Read(envelope) (E, error)` decodes a stored envelope
through the declaration that wrote it — the fact's own reader chain, every
declared upcaster applied, the current reader type out. It refuses an envelope
of another family or another type name with `ErrUnknownType`, a revision the
chain does not retain with `ErrRevision`, and carries `ErrPayload` and
`ErrUpcast` from the chain, which are the same four history-class refusals a
replay produces (§2.3). It runs the same store-data check `Repo.apply` does
before rendering anything, so a type name from a shared table cannot forge a log
line (§INV-025).

`projection.Router` keys `(family, type name)` → a typed applier, so a handler
is declared rather than switched:

```go
router := projection.NewRouter(projection.SkipForeign)
projection.On(router, orderPlaced, func(ctx context.Context, placed Placed, of event.Envelope) error { … })
projection.On(router, orderPaid,   func(ctx context.Context, paid Paid, of event.Envelope) error { … })
projection.Ignore(router, "orders", "orderArchived", "orderNoteAdded")
```

`On` panics and `TryOn` returns the error, which is [[D-123]]'s rule for a
declaration, and `Ignore`/`TryIgnore` are the same pair.

**The router's coverage is inferred, and that is what makes a forgotten
registration loud.** Every `On` declares its fact's family (`Fact.Family`), so
the set of families this router covers needs no configuration and cannot drift
from the routes. Then there are exactly two answers for an envelope no route
claims, and the difference between them is the whole point of the type:

- **Its family is one this router covers.** It is `ErrUnrouted`, the default
  classifier calls it `Permanent`, and the projection halts. This is not
  configurable and no policy relaxes it, because this is the forgotten
  `projection.On`, the renamed wire type and the event type a newer deploy
  started writing — three ways to get a projection that compiles, runs, returns
  no error, reaches `PhaseFollowing`, advances its checkpoint and silently drops
  a whole event type for the life of the deployment. `Ignore` is how a type in a
  covered family is declared as deliberately not this projection's: it costs one
  name, it is a decision written down, and a rename makes it stale in the safe
  direction — the old name stays ignored and the new one halts.
- **Its family is one this router covers no route of.** That is the ordinary
  case — a global log carries every aggregate's events and a projection is over
  a few — and the `Foreign` policy decides it. `SkipForeign` is the zero value
  and the default, because a projection over a subset of the log is the normal
  case and refusing every other family would make the router useless.
  `RefuseForeign` is for the projection that must see every fact.

`Router.Skipped()` counts the envelopes skipped as foreign, and it is the only
count there is, because inside a covered family the router skips nothing. A
skip of a foreign family is deliberately **not** on `State`: it is not a
projection-level fact, it is unbounded in a way that says nothing about this
projection, and the mistake a counter there would have been guarding against is
now a halt. §INV-081 is the invariant that pins the pair.

§3.4's rule — *`Halt` is the default and skipping is never the default* — reads
literally here, and the zero values are what say so.

### 3.4 What happens when something goes wrong

The whole table, because a partial one leaves a default branch to guess.

| What answered | Where | What the projection does |
|---|---|---|
| `context.Canceled` / `DeadlineExceeded` | anywhere | Return from `Run`; the supervisor reads it as an expected stop |
| `ErrBackend` | read or save | Retryable. Back off and try again, without limit: the database coming back is the normal case and nothing is lost while the checkpoint stands still. `Ready` reports unhealthy once the streak passes `Tolerate` |
| `ErrClosed`, `ErrRefused` | read or save | Terminal. Halt. Retrying a closed or unprepared store is a busy loop that never clears |
| `ErrCursor` | read | Terminal. Halt, naming the projection. **It never restarts from the origin** (§UC-101) |
| `ErrWrongStore` | `event.Read` at start-up, or `Tracker.Load` on any pass | Terminal wiring refusal; halt. From the tracker it means the checkpoint store answered a row that is not this projection's, and no retry changes that (§UC-129) |
| `ErrConflict` | save | Bounded resolution, once, as below. A row that moved is a second live instance and the loop **takes its turn** — the row is adopted, the reader rebuilt from its cursor, the page dropped, the loop backs off ([[D-133]], §UC-103). A row that did **not** move is a store refusing a save its own fence admits, and that halts |
| `ErrUncertain` | save | Bounded resolution, once: `Tracker.Load` again. If the stored advance is the one this pass tried to write **and the row carries the cursor it presented**, it landed — continue from the in-memory cursor. The same advance beside another cursor is another instance's row and takes its turn. Otherwise halt. Never a blind re-save |
| the handler's own error | `Apply` | `Classifier` decides. Default: the **history class** (`ErrUnknownType`, `ErrRevision`, `ErrPayload`, `ErrUpcast`) and `projection.ErrUnrouted` are `Permanent` — a payload this build cannot read does not become readable by trying again, and neither does a type nothing routes — and everything else is `Retryable` |
| a handler panic | `Apply` | Recovered, reported as a permanent failure of that page, with the panic value rendered by the observer and never by a log line of the framework's |
| `Retryable`, `Attempts` exhausted | `Apply` | Becomes permanent |
| `Permanent` | `Apply` | `Halt` (default), or `Quarantine` when a `Quarantines` sink is supplied |

**`Halt` is the default and skipping is never the default.** A projection's whole
value is that its state is a function of the log; skipping one event makes it a
function of "the log minus whatever failed", silently and forever. `Quarantine`
exists because some projections genuinely prefer it — a search index over twelve
event types where one payload is corrupt — and it requires a sink, because a
policy with nowhere to record is a skip with extra words.

**Quarantine is envelope-granular, and the framework buys the granularity with
re-delivery rather than with a second handler signature.** A page-granular
quarantine would drop up to `MaxRead` applicable events for one corrupt payload,
which is not what the search-index case is asking for. So a page whose failure
is permanent under `OnPermanentFailure: Quarantine` is delivered again, one
envelope to a `Batch`, in position order:

- an envelope that applies is applied;
- an envelope whose failure the classifier calls `Permanent` goes to the sink
  with its cause and is passed;
- a **retryable** failure in the isolation pass ends the isolation pass and
  returns the whole page to `Retrying` under the same `Attempts` budget, because
  a database that went away is not a corrupt payload.

When every envelope has applied or been quarantined, the checkpoint advances
over the page once, carrying `Progress.Quarantined` raised by what the sink
took. `Attempt` keeps rising across the isolation pass, so a handler that logs
on a third attempt still does. The price is that an envelope which applied
inside the failed attempt is applied again in isolation — the same
at-least-once redelivery §UC-108 already names, requiring the same idempotency
it already requires, so quarantine adds no obligation a handler did not have.
Under `InUnit` the isolation pass is one unit: the applies and the advance
commit together, or none of them do.

**What `InUnit` requires before it means anything.** The atomicity below is a
property of **one transaction**, so it holds exactly as far as the handler's
writes are in that transaction: the same resource the `Unit` opens and the
checkpoint store joins, reached through the context the handler is given. A
handler that writes to a second database, or through a pool of its own, gets
`AfterApply` semantics with `InUnit` spelled on the spec — its rows survive the
rollback of the advance, the page is redelivered, and a non-idempotent handler
double-applies against a consumer who was told they did not need idempotency
here. §3.2(3)'s fourth check catches the common shape of this, and it is a real
check and not a formality: it resolves `Spec.Destination` inside the unit and
refuses the pass when what is bound for it is not a transaction.

What no check can see is a `Destination` that names one resource while the
handler writes to another, and a destination that is not reachable through
`crud`'s binding at all, which is what `projection.Unchecked` says out loud.
**So the alignment is a load-bearing obligation the framework cannot enforce,
and it is written here because this is where the developer reads it** — the same
kind of promise, in the same words, as key injectivity (§UC-050) and the changed
fold a snapshot revision must declare (§5.4): unenforceable, load-bearing, and
therefore stated rather than assumed. §UC-128 is the case that measures what a
consumer gets when it is not kept.

**Duplicates.** Delivery is at least once, in both modes, and the framework
deduplicates nothing. What it gives a handler instead is the identity that makes
idempotency cheap and that it was already carrying: `(Stream, Version)` is unique
and stable for every event ever written (§INV-034, §INV-009), so an upsert keyed
on it is the whole of what most handlers need. `Position` is stable too and is
the natural key for a handler that stores a single "last applied" row.

In `InUnit` mode, **and under the precondition above**, a crash before the
commit rolls back the handler's writes *and* the advance together, so the
redelivered page finds the read model exactly as it was. That is the property
that makes a projection restartable, and it is worth saying precisely what it is
**not**: it is not exactly-once, and it says nothing about an effect that left
the transaction — an email, an HTTP call, a broker publish are at-least-once in
both modes and always will be, and neither is a write to a resource the unit
never opened.

### 3.5 Lifecycle

`projection.Projection` is a `runtime.Runner`: `New` performs no I/O, starts no
goroutine, reads no environment and mutates no global (§INV-013, §INV-074). The
first read of the checkpoint happens on the first pass of `Run`, not in `New`.

- `Name()` is `"vv.event.projection." + spec.Name`, so the supervisor's
  duplicate-name refusal covers two projections of one name in one process and
  an operator sees the name they chose.
- `Declaration()` is `{Placement: Singleton, Durability: Durable}`. It is a
  promise about how a deployment should run it and not an enforcement — the
  fence is what actually holds when a deployment ignores it (§UC-103).
- `Run(ctx)` blocks until the context is done and returns `ctx.Err()`.
- `Drain(ctx)` (`runtime.Drainer`) stops the loop after the pass it is in.
  `Supervisor.Stop` drains before it cancels precisely so a runner can commit
  its last unit of work, so a shutdown between the handler's write and the
  checkpoint advance is a window a clean deploy never enters. A halted
  projection has no pass in flight, so its `Drain` returns at once rather than
  holding the supervisor to its grace (§3.2(6)).
- `Ready(ctx)` (`runtime.Readier`) is the readiness half and names no importance
  and no code ([[D-091]]). It reports an error when the projection is halted, or
  when it has been retrying for longer than `Tolerate`.
- `State()` is the observation: phase, progress, attempt, error, instant. The
  same value goes to the `Observer` on every transition, which is where a
  composition root logs, counts or exports.

**The projection writes to no logger, and that is a choice against a decision
that has to be argued rather than assumed.** [[D-062]]'s invariant is that every
line this library emits goes through `port.Logger(ctx)`, and its worked case is
exactly this one — "the failures nobody can be returned an error for", which a
recovered handler panic and an exhausted retry streak are. Phase 3 emits no line
instead: a halt is already reachable at the readiness door the supervisor
aggregates, a transition is already published to the `Observer`, and adding
`port` to this package's closure buys one more spelling of both. The cost is
stated rather than hidden — a deployment that supplies no `Observer` learns a
handler panicked only from `Ready`, and learns nothing of a retry streak below
`Tolerate`. §12.9 leaves the alternative to the plan. What the package does not
do under any of the three answers is write to a process-wide logger.

It imports neither `port` nor `health` and reaches nothing outside `event`,
`crud`, `errs` and `runtime` (§11.3).

---

## 4. Catching up, and what "the head" is

### 4.1 There is no head

A store that cannot promise monotone visibility has no single number that is
"the end of the log", and `eventpg` will not compute one: phase 2 refused to
export `Tail` because a cursor at the end is sound only if every position below
it is settled, and no O(1) reading proves that (non-goal 5). More sharply,
`Reader.Next` returning false means **"nothing has settled yet"**, not "there is
nothing more" (§2.6): an empty page can be answered while events exist, at
positions a writer still holds.

So "caught up" is defined over the walk and never over the log:

> **A projection is caught up when its last read delivered nothing.** That is a
> statement about this walk at this instant. It is not a claim that the log is
> empty above it, and the very next read may return events.

### 4.2 One loop, not two phases

Draining to the head and following are therefore not two mechanisms. They are
one loop with one branch: a non-empty page is applied and the next read is
issued immediately; an empty page is followed by a wait. `PhaseDraining` and
`PhaseFollowing` are **observations of that branch**, published so an operator
can see a rebuild has caught up, and nothing in the loop reads them.

Any code that computed a head position and compared the cursor to it would be
building §1.1's contract with extra steps, so there is no such code and no API
that would need one.

What the wait is: `Spec.Idle` (default 1s) on a `runtime.Ticks` seam — the same
injectable ticker `runtime.NewPeriodic` uses, so a test drives time rather than
sleeping — or `Spec.Wake`, a `<-chan struct{}` an application signals after its
own append, or the context being done. A missed wake costs latency and never
correctness, because the poll is the floor.

### 4.3 The read-your-writes question, answered by refusing it

The API everybody asks for is `WaitUntilCaughtUp(position)`. It is not offered,
and the reason is worth a paragraph because the refusal will be re-litigated.

`Progress.Highest ≥ P` **is** a sound predicate — §1.3(3) is exactly that
theorem. What is not sound is the ergonomics around it: a caller that appended
has no position, because `event.Commit` carries the stream, the versions and the
authority and deliberately not a position (an envelope's position "means
something only once the append that carries it has committed", `event/store.go`).
So a framework `Await` would have to hand out positions from somewhere, and the
one place it could get them is a read the caller does not otherwise need.

The answer an application should use instead is one column: write the stream
version into the read-model row and check it. It is exact, it needs no framework
state, it survives a rebuild, and it cannot be mistaken for a resume point.
`Progress` is published for the operator; a per-request wait is the
application's.

---

## 5. Snapshots: the measurement, and the decision

The roadmap's E2 says a snapshot is added "only after measured replay cost and
full-replay equivalence proof". So it was measured.

### 5.1 What was measured

PostgreSQL 17.9 in a container on the same host (~0.1 ms round trip), one
connection, `-race` off, the deployed `eventpg` schema at its defaults
(`StreamPage` 256, `MaxRead` 256), a fresh stream per row of the table, five
passes each for the replay and the tail read and three for the raw scan, after a
warm-up:

| Aggregate | Full replay, no-op codec | Full replay, `event.JSON` | Read past the end | Raw one-statement scan |
|---|---|---|---|---|
| 10 000 events (40 pages) | 10.3 – 11.5 ms | 16.0 – 16.9 ms | 0.1 ms | 9.5 – 9.9 ms |
| 100 000 events (391 pages) | 101.7 – 107.9 ms | 157.4 – 164.2 ms | 0.1 ms | 96.6 – 100.1 ms |

The first three columns are 120-byte payloads through a codec that returns the
bytes it was handed; the `event.JSON` column is a separate aggregate with a
three-field payload, so the two are the same order of magnitude rather than a
strict A/B — what it bounds is how much decoding and folding add, which is
~55 %.

Two more numbers from the same run, because they bound the two things a reader
will assume the table is measuring:

- **A global walk of 110 000 events at `MaxRead` 256 costs 155 – 157 ms**, three
  passes, i.e. ~1.4 µs per event end to end. A projection rebuild is that cost
  plus the handler's.
- **Writing** 10 000 events took 238 ms and 100 000 took 2.33 s, in batches of
  64 through `Repo.Append`.

What the table says when read carefully: the single-statement scan is 92 – 94 %
of the paged replay, so **paging is not the cost here** — 391 pages add about
6 ms to the 100 000 case and the rest is row transfer and the fold. That is the
one number a different environment moves by an order of magnitude: the pages are
issued one after another, so on a 1 ms link rather than a 0.1 ms one the same
replay gains roughly 350 ms from the page count alone.

### 5.2 The decision

**A snapshot does not ship in phase 3.** Three reasons, in order:

1. **The measured cost does not justify it at the sizes the framework's own
   rules encourage.** 11–17 ms for a 10 000-event aggregate is inside the budget
   of a single request, and an aggregate that reaches 100 000 events has a
   modelling problem the roadmap already names — a consistency boundary drawn
   too wide — that a snapshot would hide rather than fix.
2. **What it costs is correctness, not code.** A snapshot is a second answer to
   the question §INV-008 says has one, and every part of it is a way to serve a
   wrong state silently: a stale one, a corrupt one, one whose revision this
   build cannot read, and — the one the framework structurally cannot detect —
   one taken with a fold that has since changed.
3. **The roadmap's gate is a measured *need*, not a measured *cost*.** There is
   no consumer. Phase 3 has four required mechanisms competing for the same
   review rounds, and the delivery policy is core mechanics first.

What ships instead is the measurement itself, so the decision is reversible from
evidence rather than from an argument: a `-bench` harness in `event/eventpg`
that replays a stream of a caller-chosen size and reports ns/event, and the
recorded trigger — **a snapshot is built when a deployment measures its own p99
aggregate replay above ~50 ms in its own environment**, which at these rates is
somewhere above 30 000 – 50 000 events, or an order of magnitude fewer over a
1 ms link.

### 5.3 What is deliberately not done instead

No memo, no cache and no "load once per request" is added either. §INV-042
already forbids the kernel retaining an application value across a call boundary,
and a per-request cache is a snapshot with no version, no invalidation rule and
no corruption story — the same second authority, with none of the four things
that make one safe.

### 5.4 The contract a snapshot would satisfy, written now

Recorded so the later phase implements a decision rather than re-derives one.
Its use cases are UC-126 and UC-127 and both are marked **deferred**.

- **What is versioned.** The *state encoding*, by a `SnapshotRevision` declared
  on the aggregate and **independent of every event revision**. A row is
  `(family, key, version, revision, payload, digest)` where `version` is the
  stream version the state is the fold of and `revision` is the encoding's own.
  A snapshot is never upcast: it is disposable, so an unreadable one is
  discarded rather than migrated, which is the whole reason its version is
  separate from the events'.
- **What invalidates one.** A `revision` this build does not read; a `version`
  above the stream's current version, which append-only makes unreachable and
  which is therefore treated as corruption rather than staleness; a digest that
  does not match the payload; and — the one the framework cannot see — a changed
  fold. That last one is the application's obligation to declare by bumping the
  revision, and it is stated in the same breath as key injectivity (§UC-050),
  because it is the same kind of promise: unenforceable, load-bearing, and
  therefore written where the developer reads it.
- **What a corrupt one does.** It is discarded, the load falls back to a full
  replay from version 0, and it **says so** — through the observation seam, with
  the family and the reason and never the key or the payload (§INV-025). A
  snapshot is never a reason a load fails.
- **How it is proved.** A conformance obligation, not a unit test: for a set of
  streams, the state loaded through the snapshot path equals the state loaded
  through full replay, and deleting every snapshot produces the identical state.
  Plus the corruption matrix — truncated payload, wrong digest, unknown
  revision, version above the stream — each asserting the fallback and the
  report.

---

## 6. Rebuild and cutover

The mechanism is the name, and there is nothing else.

**A rebuild is a second projection with a second name, over the same log,
writing to a second destination.** Its checkpoint is absent, so it starts at the
origin, drains, and then follows. Nothing about the live projection is touched —
not its checkpoint, not its destination, not its runner. There is no "rebuild
mode" flag and no `Reset`, because a flag that clears a checkpoint in place has
the blast radius of a typo and is indistinguishable, afterwards, from a
corruption.

**Cutover is the application's**: it flips which destination its reads use. The
framework's contribution is the three things that make the flip safe:

1. Both projections publish `State` and `Progress`, so an operator sees the new
   one leave `Draining` for `Following` rather than guessing from a log line.
2. The comparison that is sound is over `Progress.Highest`, not over cursors,
   and it is a statement about **consumption** and not about content:
   **`new.Highest ≥ old.Highest` means the new projection has consumed —
   applied, or quarantined into its sink — every committed event at or below
   `old.Highest`**, by §1.3(3) and by `Highest` being written only at a save
   (§1.2). Two cursors cannot be compared and no API takes two.

   Three things it is **not**, each of which an operator can otherwise read into
   it:
   - **It is not a completeness claim.** A projection running `Quarantine`
     advances over events it did not apply, so `Highest` alone can hold for a
     read model with known holes. `Progress.Quarantined` is the count that says
     how many, it is persisted beside `Highest` for exactly this reading, and a
     cutover decided without it is decided on half the number. Under
     `OnPermanentFailure: Halt` — the default — `Quarantined` is zero and the
     comparison is as strong as it looks.
   - **It says nothing about routes.** Two projections are supposed to route
     differently; that is what a rebuild is for. A `Router` skips the families
     it covers no route of and no count of those appears anywhere (§3.3), so
     "consumed" means "consumed as this projection's routes define it". Within a
     covered family nothing is skipped, which is what keeps `Highest` from
     hiding a forgotten registration (§INV-081).
   - **It is one instant of two moving values.** `old.Highest` rises while it is
     read. The question an operator is actually asking is "has the new one
     caught up", and the form of it that survives the race is
     `new.Highest ≥ old.Highest` **and** the new projection in `PhaseFollowing`.
3. **What decides a cutover is the content of the two destinations, and that
   comparison is the application's.** The framework offers no equivalence check
   and cannot: the two read models are two different shapes, which is the reason
   the rebuild exists. What it offers is the conditions under which the
   comparison is worth making — a quiescent log, both projections in
   `PhaseFollowing`, both at the same `Highest`, neither with a raised
   `Quarantined` — and §UC-120's control is that the rows actually agree under
   them. This is the same obligation §5.4 puts on a snapshot, on the feature
   that ships rather than on the one that is deferred.
4. Neither projection can be run twice by accident: the supervisor refuses two
   runners of one name in one process, and the checkpoint fence refuses a second
   *process* (§UC-103).

**Rollback is flipping the reads back.** There is nothing to restore, because the
live projection was never stopped and its checkpoint was never touched. The
rebuilt one is left running, or stopped and `Forget`-ed, and the second
destination is dropped. That is the whole procedure and it is documented in the
usage guide rather than encoded in an API, because every step of it is an
application decision.

**Retiring a name** is `Forget`. If the projection is still running, its next
save is refused and it halts — visibly, at the readiness door, which is the
correct outcome of deleting a live projection's progress.

---

## 7. Use cases

Format is phase 2's: **Given**, **Then**, **Must not**, **Control**. Numbers are
allocated in order and cases are placed in the group they belong to, so the
groups do not read in numeric order: UC-129 sits in Group T, UC-128 and UC-130
in Group U.

### Group T — The checkpoint

#### UC-099 A projection runs for the first time  [happy]
- **Given** A checkpoint store holding no row for this name.
- **Then** `Tracker.Load` answers the zero checkpoint, `Fresh()` is true, the walk starts
  at the origin (`event.Read(log, "")`), and the first save carries
  `Advance == 1`.
- **Must not** Absence must not be reported as an error, and must not be
  answered by anything other than the origin — "start from now" would silently
  skip a first-time consumer's whole backlog.
- **Control** The second run of the same projection resumes from the saved
  cursor and does not re-apply what the first applied.

#### UC-100 A projection resumes in another process  [happy]
- **Given** A checkpoint written by one process and a second process, over a
  second store value on the same backing, that reads it.
- **Then** The walk continues where it stopped, skips nothing, repeats nothing
  within the pass, and — with a writer that committed **out of position order**
  between the two — delivers the low position it had not passed.
- **Must not** The checkpoint must carry no per-instance nonce, and the resume
  must not be derived from `Progress.Highest`.
- **Control** A run of the same two halves against a log with a position held
  uncommitted across the restart, asserting the held position is delivered after
  its commit and nothing above it was delivered before.

#### UC-101 A checkpoint names another log, or a format this build no longer reads  [edge]
- **Given** A stored cursor minted over another schema, one that is not this
  store's format, and one whose format tag has been retired.
- **Then** `ErrCursor` at the first read; the projection halts, names itself, and
  reports through `Ready`.
- **Must not** It must not restart from the origin — that re-applies the whole
  log against a live read model — and must not overwrite the checkpoint it could
  not read. The same holds for a checkpoint the door refused (§UC-129): a row
  the projection would not resume from is a row it does not write over either,
  because an operator's only repair is the row that is there.
- **Control** A cursor from this log resumes, and an **absent** checkpoint starts
  at the origin, so the refusal is discriminating rather than universal.

#### UC-129 A checkpoint store answers a checkpoint that is not this projection's  [edge]
- **Given** A `Checkpoints` whose `Load` ignores its `projection` argument, or
  whose statement is mis-parameterised, so it answers another projection's row —
  well-formed, of this log, in this store's format, and hundreds of thousands of
  positions ahead. Beside it: a `Load` answering `Advance == 0` with a cursor
  set, and one answering a cursor longer than `MaxCursorBytes`.
- **Then** `event.Tracker.Load` refuses all three with `ErrWrongStore` before any
  of it reaches a walk. The projection reports a wiring refusal and halts. No
  read is issued from the foreign cursor and no save is issued at all.
- **Must not** The projection must not resume from a cursor answered for another
  name — that skips every event between the two positions, silently and
  permanently, with no error on any path — and must not overwrite the row it
  refused.
- **Control** The honest store answers its own row through the same door and
  resumes, so the check is discriminating rather than universal; and the same
  three defects are what §UC-123's mutation harness reports by section, so the
  door and the suite are not two accounts of one check.

#### UC-102 A checkpoint is forgotten under a running projection  [edge]
- **Given** `Forget` called for a name a projection is running.
- **Then** The next save finds no row at its advance, is refused `ErrConflict`,
  and the projection halts.
- **Must not** The projection must not silently create a new row at advance 1 and
  continue, which would leave the read model holding events no checkpoint
  accounts for.
- **Control** `Forget` of a name nobody is running answers nil and the name is
  free for a fresh projection.

#### UC-103 Two replicas run one projection name  [edge]
- **Given** Two processes, both with the same name, the same log and the same
  checkpoint store, both reading advance `n` and both applying the page.
- **Then** One save lands at `n+1`; the other is refused `ErrConflict`, **takes
  the row the winner left, rebuilds its reader from that row's cursor and carries
  on after a backoff** — it does not halt. `ErrOvertaken` reaches `State.Err` on
  every lost fence and `Ready` once the losing streak outlasts `Tolerate`, which
  is how a deployment learns it is running two. In `InUnit` mode the advance is
  claimed before the handler runs, so against a checkpoint store whose fenced
  save takes the row's lock the loser is refused **before** it applies anything;
  where the store stages optimistically instead, the loser applies and its own
  writes roll back with the refused advance. In `AfterApply` mode there is no
  lock to hold across a handler call: its writes have already happened, and the
  backoff bounds the duplicate rate to one page per `Backoff.Max`.
- **Must not** Two writers must not both advance one checkpoint, and the
  framework must not pretend the `Singleton` declaration prevented it — a
  declaration is a promise about deployment and the fence is the enforcement.
  **The loser must not halt** ([[D-133]]): a rolling restart runs two instances
  of a singleton on purpose for a few seconds, and a projection that dies there
  is one every deploy kills. And a lost fence must not be confused with a row
  that is **absent** or **behind** the fence — those are a name forgotten, reset
  or restored under a running projection, and they still halt.
- **Control** One process alone advances repeatedly with no refusal and stays
  healthy, so a fence that refused everything fails.
- **Changed** 2026-09-09, S3: this case said the loser halts. It was rewritten
  with [[D-133]], against the reference implementation's
  `SELECT … FOR UPDATE SKIP LOCKED` claim (§Adopt A2 of the adjudication) and the
  rolling-deploy argument the halting rule loses on.

#### UC-104 A checkpoint save is unconfirmed  [edge]
- **Given** An autocommit save on `eventpg` whose backend is terminated in
  flight (`pg_terminate_backend`), so the store classifies `Unconfirmed`.
- **Then** The projection performs the bounded resolution once: `Tracker.Load`;
  if the stored advance equals the one it tried to write **and the row carries
  the cursor that save presented**, the save landed and the loop continues from
  the in-memory cursor. The same advance beside any other cursor is a second live
  instance that reached that fence while this pass's unit rolled back: the row is
  adopted and the reader rebuilt from **its** cursor (§UC-103). Anything else
  halts.
- **Must not** It must not re-save blindly, must not re-apply the page on the
  assumption that the save failed, must not treat the `ErrConflict` a blind
  re-save would produce as evidence of a second writer, and **must not read a
  matching advance alone as proof that its own save wrote the row** — every
  position between the two cursors would then be applied by nobody and behind the
  checkpoint at the next save.
- **Control** The same failure inside a bound transaction answers `NotWritten`
  and the pass simply retries, so the two windows are told apart by evidence
  (§INV-052).
- **Changed** 2026-09-09, S4's review: the resolution said "if the stored advance
  equals the one it tried to write, the save landed". It does not — the fence
  admits one writer at each advance and a rolling deploy produces a second one on
  purpose. Driven live: the read model ended holding two of four events while the
  checkpoint stood over all four, with no error on any path.

### Group U — Delivery

#### UC-105 A page is applied and the checkpoint advances in one transaction  [happy]
- **Given** `Advance: InUnit`, a `Unit` that opens a `crud` transaction over the
  read model's source, a checkpoint store on that same source, and
  `Spec.Destination` naming that source.
- **Then** The handler's rows and the advanced checkpoint commit together; the
  tracker's `Transaction(ctx)` inside the unit answers a valid authority; the
  destination resolves inside the unit to an executor `crud.IsTransaction`
  accepts; a rollback leaves neither.
- **Must not** No second connection, no second transaction, no advance outside
  the unit.
- **Control** The same projection at `AfterApply` writes the two separately, and
  a rollback injected between them leaves the handler's rows with the checkpoint
  behind — which is the duplicate window the mode names.

#### UC-106 The process dies between the write and the checkpoint  [edge]
- **Given** `AfterApply`, a handler that has committed its rows, and a kill
  before `Save`.
- **Then** The restart resumes from the previous checkpoint and delivers the same
  page again. The handler sees `Attempt == 1` again — it is a new delivery, not a
  retry — and its idempotency is what makes the second application harmless.
- **Must not** The framework must not claim this window does not exist, must not
  bound it by anything other than `MaxRead`, and must not describe either mode
  with the words "exactly once".
- **Control** The same kill at the same point under `InUnit` leaves no rows at
  all, so the difference between the modes is a measurement rather than a
  sentence.

#### UC-107 `InUnit` is asked for and cannot be honoured  [edge]
- **Given** (a) `InUnit` with a nil `Unit`; (b) `InUnit` with a checkpoint store
  whose `Capabilities().Transactions` is not `Supported`; (c) `InUnit` with no
  `Destination` at all — neither a handle nor `projection.Unchecked`; (d) a
  `Unit` that returns without binding anything the checkpoint store recognises;
  (e) a `Unit` that binds a transaction the checkpoint store recognises while
  `Spec.Destination` names a source with no transaction bound on that context —
  the two-resource wiring §3.4 names.
- **Then** (a), (b) and (c) are refused at `New` with `projection.ErrSpec`,
  naming which half is missing. (d) and (e) are refused on the first pass,
  **before the handler runs**, the unit is rolled back and the projection halts.
- **Must not** None of the five may fall back to `AfterApply`. A projection that
  asked for an atomic advance and silently got a separate one double-applies on
  every crash, and every test over a process that never crashes is green.
- **Control** A correctly wired `InUnit` projection passes through the same code
  path, so a check that refused everything fails; and `Destination:
  projection.Unchecked` is accepted, so (c) is about the field being unstated
  and not about the check being mandatory.

#### UC-108 A page is delivered twice  [edge]
- **Given** Any redelivery: a crash, a retry after a retryable handler failure,
  or a fenced loser's page.
- **Then** The handler receives the same envelopes in the same order, with the
  same `(Stream, Version, Position)` on each — equal values in memory of its
  own, never the array the previous delivery was handed (§3.3) — and `Attempt`
  distinguishing a retry from a fresh delivery.
- **Must not** The framework must not deduplicate, must not reorder, and must not
  hand the second delivery a different subset — a re-read from one cursor is
  stable in its prefix and may only grow (§INV-066).
- **Control** A handler that records `(Stream, Version)` and refuses a repeat
  proves the identity is stable across the redelivery; a handler that counts
  proves the duplicate happened at all.

#### UC-109 A handler fails and then succeeds  [edge]
- **Given** A handler that returns a retryable error twice and then applies.
- **Then** The same page is applied again after a backoff of `First`, then
  `2×First`, capped at `Max`; `Attempt` rises; the phase is `Retrying`; the
  checkpoint does not move until the page applies; `Ready` stays healthy until
  the streak passes `Tolerate`. Every attempt receives **its own page**: the
  envelopes the log answered, in a fresh slice, with fresh payload bytes.
- **Must not** The projection must not advance past a page it did not apply, must
  not re-read the log between attempts, and must not spin without a delay.
- **Control** The first-attempt success takes the same path with no backoff, and
  a test ticker proves the delays rather than sleeping for them. And the
  ownership half has its own control, because it is the one that passes
  vacuously against a handler that reads politely: a handler that **truncates,
  reorders and overwrites the payload bytes of** `Batch.Envelopes` — all legal
  under §INV-021's grant — and then fails retryably, asserting the second
  attempt receives the log's page unchanged and that the checkpoint the applied
  attempt saves covers every position the log answered. Against a framework that
  hands the same slice back, the second attempt applies the mutated page and the
  advance passes events nobody ever saw, so the control fails loudly rather than
  by a subtle diff.

#### UC-110 A handler fails permanently for one event  [edge]
- **Given** A page of several envelopes, one of which the handler always fails
  with an error the classifier calls `Permanent`, the rest of which apply; and
  beside it a retryable failure that exhausts `Attempts`.
- **Then** Under `Halt` (default) the projection stops advancing, stays running,
  publishes `PhaseHalted` **once** with the offending envelope's identity, and
  `Ready` reports the error. Over a bounded window after the halt it issues
  **zero** reads and **zero** saves, publishes no further transition, and its
  `Drain` returns without waiting (§3.2(6)).
  Under `Quarantine` with a sink, the page is re-delivered one envelope at a
  time: the applicable envelopes apply, the offending one reaches the sink with
  its cause, the checkpoint advances over the page exactly once, and
  `Progress.Quarantined` rises by one — by one, not by the page's length, which
  is the assertion that separates envelope granularity from page granularity.
- **Must not** The event must not be skipped by default; `Quarantine` must not
  be selectable without a sink; the projection must not return from `Run`, which
  would take the process down for one unapplied event; and `Quarantine` must not
  drop the envelopes that applied — a page-granular quarantine loses up to
  `MaxRead` good events for one corrupt payload.
- **Control** `Quarantine` with a sink that itself fails halts instead of
  skipping, so the sink is load-bearing rather than decorative; and a
  **retryable** failure inside the isolation pass returns the whole page to
  `Retrying` rather than quarantining anything, so isolation is not a second
  spelling of "skip whatever failed".

#### UC-111 A payload this build cannot read reaches a projection  [edge]
- **Given** An envelope at a revision the handler's declaration does not retain,
  a payload the codec refuses, or an upcaster that says no.
- **Then** `Fact.Read` answers the history-class sentinel; the default classifier
  calls it `Permanent`; the projection halts and names the type and the revision
  and neither the key nor the payload.
- **Must not** A history-class failure must not be retried forever — the bytes do
  not change — and its message must not carry data (§INV-025).
- **Control** A deploy that adds the missing reader and restarts applies the same
  event, which is what proves the halt preserved it rather than passed it — and
  the restart is the **only** exit: the halted value is left running for a
  window first and never resumes by itself, because a halt that could clear
  itself is a retry loop with a longer period (§3.2(6)).

#### UC-128 A projection writes to a database this framework does not know  [edge]
- **Given** A read model in a second database — another server, or a handle
  `crud` has no binding for at all: a document store, an ent client over its own
  pool, a search index. The checkpoint table is in the first database, and the
  `Unit` opens a transaction there.
- **Then** Three wirings and three different outcomes, and the case asserts all
  three because the point is that they are told apart:
  - `AfterApply` with the read model's own writes is the honest mode and works.
    The window §UC-106 names is the whole of what the consumer is exposed to,
    and their idempotency closes it.
  - `InUnit` with `Spec.Destination` naming the read model's `crud` source is
    refused on the first pass, before the handler runs, because no transaction
    of that source is bound inside the unit (§UC-107(e)).
  - `InUnit` with `Destination: projection.Unchecked` is accepted, and what the
    consumer gets is measured rather than described: a crash between the
    handler's commit and the unit's leaves the read model's rows in place while
    the advance rolls back, the page is redelivered, and a non-idempotent
    handler applies it twice. That is `AfterApply` semantics under an `InUnit`
    spec, it is the outcome the obligation in §3.4 is about, and no check in the
    framework can see it.
- **Must not** The framework must not claim atomicity for a write it never saw.
  §3.4's promise must carry its precondition wherever it is stated — the module
  page's delivery row included — and `Unchecked` must not be reachable by
  leaving a field zero, because a silent downgrade is exactly what §INV-070
  refuses.
- **Control** The same handler and the same `Unit` against a read model in the
  checkpoint store's own database, with `Destination` naming it: the crash at
  the same point leaves no rows at all. The pair is the measurement — one
  wiring, two outcomes, and the difference is the precondition rather than the
  mode.

#### UC-130 A router covers two aggregates, and one registration is missing  [happy]
- **Given** A `Router` over two aggregates — `orders` with three declared facts
  and `payments` with two — with `On` for four of the five, `Ignore` for the
  fifth, and a log that also carries a third aggregate's events. Each applier
  takes its decoded `E`, which `Fact.Read` produced through that fact's own
  reader chain, with one of the four at an older revision so a declared upcaster
  runs.
- **Then** Each event reaches its own applier with the value its declaration
  produces; the ignored type reaches none and is not an error; the third
  aggregate's events are skipped as foreign and counted by `Router.Skipped()`;
  the read model holds exactly the rows the four appliers wrote; the projection
  reaches `PhaseFollowing` with `Progress.Quarantined == 0`.
- **Must not** `Fact.Read` must not reflect over the payload, must not accept an
  envelope of another family or another type name, and must not decode a
  revision the chain does not retain — the four history-class refusals and no
  other (§UC-111 is their edge half).
- **Control** The same router with **one `On` removed**: the events of that type
  are of a family the router still covers, so the page fails with `ErrUnrouted`,
  the default classifier calls it permanent and the projection halts on the
  first such event rather than reaching `PhaseFollowing` with a read model
  missing a whole type. Beside it, the same removal for a type in the *third*
  aggregate, which is skipped and counted rather than refused — the pair is what
  makes §INV-081 discriminating rather than either universal refusal or
  universal silence. And the same router built with `RefuseForeign` halts on the
  third aggregate's first event instead, with `ErrUnrouted` classified
  `Permanent` by the default `Classify` — the same sentinel and the same verdict
  as the in-family case, so a consumer reads one refusal rather than two, and
  `ErrUnrouted` is permanent by `Classify`'s own rule rather than by being in
  the history class it is not a member of.

### Group V — The walk

#### UC-112 A projection drains a log and then follows  [happy]
- **Given** A log with more events than several pages and a projection starting
  from the origin.
- **Then** Pages are applied back to back with no wait between them while they
  are non-empty (`PhaseDraining`); the first empty page moves it to
  `PhaseFollowing`; a later append is delivered within `Idle`, or immediately if
  the wake channel is signalled.
- **Must not** The projection must not compute a head position, must not stop at
  one, and must not treat `PhaseFollowing` as terminal.
- **Control** A projection whose log receives nothing stays in `PhaseFollowing`
  indefinitely and issues **no** checkpoint saves at all, which is what makes the
  empty-page rule observable.

#### UC-113 A projection meets a position still in flight  [edge]
- **Given** Writer A holding an uncommitted append at position `p`; writer B has
  committed `p+1`; a projection walking.
- **Then** Nothing at or beyond `p` is delivered and the checkpoint does not pass
  it. After A commits, the next pass delivers `p` then `p+1` in that order, and
  the read model reflects both.
- **Must not** The projection must not deliver `p+1` and checkpoint past `p`.
  This is the case a projection over a naive `position > N` checkpoint passes
  every other test while losing events in production.
- **Control** With no writer in flight the same walk delivers immediately, so a
  projection that stalls forever fails.

#### UC-114 A poll finds nothing  [edge]
- **Given** A following projection whose read answers an empty page, on a store
  whose returned cursor differs from the one that was passed in — which
  `eventpg` produces when a bound it was carrying has settled.
- **Then** The reader's in-memory cursor advances; **no save is issued**; a
  restart resumes from the last *applied* checkpoint and re-settles at the cost
  of one round trip.
- **Must not** The projection must not save a cursor for a page it did not apply
  — an idle projection would write once per poll forever — and must not discard
  the in-memory cursor, which would re-mint a settlement bound every pass.
- **Control** The save count over N idle polls is zero, and the round-trip count
  over the same N is N.

#### UC-115 A projection is given a store rather than a log  [edge]
- **Given** `Spec.Log` set to a value that is also an `event.Store`.
- **Then** `New` refuses with `projection.ErrSpec`, naming `event.ReadOnly`.
- **Must not** A projector must not hold a value it can append through
  (§UC-036): a projector that can append is how a replay writes.
- **Control** `event.ReadOnly(store)` is accepted and serves, so the refusal is
  about the capability and not about the type name.

#### UC-116 The log's store is closed under a running projection  [edge]
- **Given** The composition root closing the store, or the schema failing
  verification after a `Check`, while the projection runs.
- **Then** `ErrClosed` or `ErrRefused` from the read; the projection halts rather
  than retrying, and `Ready` reports it.
- **Must not** It must not retry a closed store — the state never clears and the
  loop becomes a busy wait against a dead handle.
- **Control** A transient `ErrBackend` in the same position is retried and
  recovers, so terminal and retryable are told apart by behaviour.

### Group W — Lifecycle

#### UC-117 The host supervises a projection  [happy]
- **Given** A `runtime.Supervisor` holding the projection among other runners.
- **Then** `New` starts nothing; `Start` runs it; its `Declaration` is
  `{Singleton, Durable}`; its state is visible through `Supervisor.States()`;
  `Supervisor.Ready` aggregates its readiness.
- **Must not** The constructor must not start a goroutine, read an environment
  variable or touch a package-level value ([[D-092]], §INV-013).
- **Control** The `startsNothing` walk extended to the new package, plus a
  construction that is never started leaving no goroutine behind.

#### UC-118 A projection is drained at shutdown  [edge]
- **Given** `Supervisor.Stop` during a pass; and, beside it, `Supervisor.Stop`
  against a **halted** projection.
- **Then** `Drain` returns after the pass in flight completes — its handler and
  its checkpoint advance both — and `Run` then returns on the cancellation.
  Nothing is left half-applied by a clean shutdown. The halted projection's
  `Drain` returns without waiting for anything, because it holds no pass, and
  its `Run` returns on the cancellation like any other; a halt is not a way to
  outlive the supervisor's grace.
- **Must not** The projection must not abandon a page between the handler and the
  save when it was given the chance to finish; and it must not ignore the drain
  deadline, which would hold the whole supervisor past its grace.
- **Control** A cancellation with no drain — the deadline expiring — leaves the
  page unsaved and redelivered on the next start, which is the window `Drain`
  exists to close and the proof that it did.

#### UC-119 A halted projection answers readiness  [edge]
- **Given** A halted projection inside a supervisor the composition root turned
  into a `health.Contribution`.
- **Then** `Ready` returns the failure for as long as `Run` has not returned; the
  contribution reports unhealthy with the **application's** chosen name, code and
  importance; the process keeps serving everything else. Over the same window a
  recording `Log` and a recording `Checkpoints` count **zero** calls of any kind,
  and the `Observer` sees the halted transition exactly once — the same
  observable-count shape §UC-114 uses for idle polls, aimed at the two
  implementations §3.2(6) exists to refuse.
- **Must not** `projection` must not import `health`, must not name an
  importance, and must not take the process down for one unapplied page.
- **Control** A healthy projection's `Ready` answers nil through the same path,
  and a projection retrying for less than `Tolerate` also answers nil, so
  readiness does not flap on one transient error.

### Group X — Rebuild and cutover

#### UC-120 A projection is rebuilt beside the live one  [happy]
- **Given** Projection `orders` running, and `orders-v2` constructed over the
  same log with a second destination and no checkpoint. Both route the same
  facts and hold the same shape of row, so that the two destinations are
  comparable at all — which is the condition the rebuilt-for-a-new-shape case
  does not have and is why §6(3) leaves the comparison to the application.
- **Then** `orders-v2` starts at the origin, drains, reaches `PhaseFollowing`,
  and `orders`'s checkpoint, destination and phase are untouched throughout.
  Then, with the log quiescent and both in `PhaseFollowing` at one `Highest`
  and both with `Progress.Quarantined == 0`, **the two destinations hold the
  same rows** — the assertion the cutover is actually made on, and the one
  §6(2)'s number is not.
- **Must not** A rebuild must not reset, pause or read the live projection's
  checkpoint, and there must be no API that would let it. `new.Highest ≥
  old.Highest` must not be asserted as if it were the row comparison: the case
  makes both assertions and says which is which.
- **Control** `orders`'s `Progress.Applied` rises across the rebuild, proving it
  never stopped; and a deliberately wrong `orders-v2` — one route left
  unregistered for a type outside its covered families, so nothing halts — reaches
  the same `Highest` and holds **different** rows, which is what proves the row
  comparison is load-bearing and the number alone is not.

#### UC-121 The rebuilt projection is wrong and the cutover is rolled back  [edge]
- **Given** Reads flipped to `orders-v2`, then a defect found.
- **Then** The application flips back. `orders` is where it always was, because
  nothing touched it; `orders-v2` is stopped and `Forget`-ed and its destination
  dropped.
- **Must not** No framework state may need repair; there must be nothing to
  restore.
- **Control** `new.Progress.Highest >= old.Progress.Highest` **together with**
  the new projection in `PhaseFollowing` and `new.Progress.Quarantined == 0` is
  the framework's contribution to deciding the cutover was safe, and the case
  states in its own words that it is a consumption comparison and not a claim
  about the rows (§6(2)). A test asserts that comparing the two **cursors** is
  not expressible in the API.

#### UC-122 A retired projection's checkpoint is forgotten  [edge]
- **Given** `Forget("orders-v1")` after its runner is stopped.
- **Then** The row is gone; a fresh projection of that name starts at the origin;
  `Forget` again answers nil.
- **Must not** `Forget` must not touch another name's row and must not be refused
  for a name that does not exist.
- **Control** The other projections' rows are asserted present afterwards.

### Group Y — Evidence

#### UC-123 A checkpoint-store implementer runs the conformance suite  [happy]
- **Given** `eventtest.RunCheckpoints(t, factory)` against `eventmemory` and
  against a live `eventpg`.
- **Then** Every section reports a verdict; the durability section is *not
  certified* for `eventmemory` with its own reason; the transactions section is
  certified for both; the run certifies more than nothing.
- **Must not** No capability may be claimed without its hook, and a run in which
  every section was not certified must fail rather than print `ok`.
- **Control** A mutation harness on §6.11's model: a checkpoint store that
  ignores the fence, one that answers a stale cursor from `Load`, one that
  reports absence for a row that exists, one that **ignores the `projection`
  argument of `Load`** and answers another name's row, and one that saves
  outside the caller's transaction — each must be reported by a named section.
  The fourth is the one `event.Track`'s door refuses in front of every consumer
  (§UC-129); the suite must report it too, because a store that only the door
  catches is a store whose implementer never learns.

#### UC-124 The suite walks the log while a lower position is uncommitted  [edge]
- **Given** `eventtest`'s `resumption` section, extended: a late writer's
  transaction held **open** across the walk rather than committed before it.
- **Then** The walk delivers nothing at or beyond the held position; the
  persisted cursor has not passed it; after the commit, a walk resumed from that
  cursor delivers it.
- **Must not** The suite must not certify a store whose cursor is its newest
  position while a lower one can still commit — which it currently does, measured
  (`## P2` §59): that store passed all twenty sections.
- **Control** A decorator over the real `eventpg` store answering
  `max(position)` of the page must now **fail** the `resumption` section, and the
  unmodified store must pass it.

### Snapshots — deferred, contract only

#### UC-125 A deployment measures its own replay cost  [happy]
- **Given** The benchmark in `event/eventpg` pointed at a stream of a chosen
  size.
- **Then** It reports ns/event and total replay for that size against a live
  database, and the numbers in §5.1 are reproducible from it.
- **Must not** The benchmark must not skip when the DSN is unset — an unset DSN
  fails, as everywhere else in this module.
- **Control** Two sizes an order of magnitude apart produce times an order of
  magnitude apart, so a benchmark measuring nothing is visible.

#### UC-126 A snapshot accelerates a replay  [happy] — **deferred**
Specified in §5.4. Not built in phase 3.

#### UC-127 A snapshot is stale, corrupt, or of an unreadable revision  [edge] — **deferred**
Specified in §5.4. Not built in phase 3.

---

## 8. Invariants

Each states the property and **how it is falsified**.

#### INV-066 A checkpoint is a store-minted cursor, and never a position
- **Statement** The resume authority in a `Checkpoint` is `Cursor`, opaque and
  minted by the store that will be resumed. No function in this framework maps a
  `Position` to a `Cursor`, no API takes two cursors, and nothing compares,
  orders or does arithmetic on one. `Progress` carries positions and resumes
  nothing.
- **Falsified by** §UC-113 — a projection over a position checkpoint delivers
  `p+1` and never `p`, and the case asserts it did not; a source check that
  `event.Cursor` appears in no comparison and no arithmetic outside a store's own
  parser; and a surface check that no exported function takes a `Position` and
  answers a `Cursor`.

#### INV-067 A checkpoint is saved once per page the projection finished with, and never otherwise
- **Statement** A page has four ends and the save rule is stated over all four,
  because "applied" covers only two of them and the loop reaches the other two
  on paths that ship:
  - a page that was **applied** saves exactly once;
  - a page that was **quarantined**, every envelope of it either applied or
    recorded in the sink, saves exactly once (§INV-073 is where that is bounded);
  - a page that **failed under `Halt`** saves none, and none is issued ever
    again;
  - an **empty** page saves none, and advances only the reader's in-memory
    cursor.

  There is no fifth end and no partial save: a page that was half quarantined
  and then met a retryable failure is back in `Retrying` and has not ended.
- **Falsified by** §UC-114 counting saves over N idle polls and asserting zero;
  §UC-109 asserting the stored advance is unchanged across two failed attempts;
  §UC-112 asserting one save per applied page; §UC-110 under `Quarantine`
  asserting the advance moved **once** for a page that was not applied whole,
  with the sink holding exactly the envelopes it passed unapplied; and §UC-110
  under `Halt` asserting zero saves over a window after the halt.

#### INV-068 An unreadable checkpoint never restarts a projection
- **Statement** A cursor the log's store refuses — foreign, unparsable, retired —
  halts the projection. It never resumes from the origin and never overwrites
  the row it could not read. Absence is a different answer and is the origin.
- **Falsified by** §UC-101's three refusals paired with §UC-099's absence, which
  is the pair that shows the refusal is discriminating; and an assertion that the
  stored row is byte-identical after the halt.

#### INV-069 The advance is fenced, the fence is the door's, and a losing writer stops
- **Statement** `Save` lands if and only if the stored advance is one below the
  one presented. The advance presented is the one `event.Tracker` holds from its
  own `Load` plus one — no caller computes it and no caller can present another,
  and a save before a load is refused. A refused save halts the projection; it is
  never retried at a re-read advance, which would make two processes take turns.
- **Falsified by** §UC-103's two-replica race asserting one winner and one halt;
  §UC-102's forgotten row; a `Tracker` asked to save before it has loaded,
  asserting the refusal; and a control in which one process advances a hundred
  times with no refusal.

#### INV-070 `InUnit` is checked as far as it can be seen, and downgraded never silently
- **Statement** `InUnit` requires, at construction, a `Unit`, a checkpoint store
  claiming transactions, and a stated `Destination`; and, on every pass, inside
  the unit and before the handler runs, a valid authority from the tracker and —
  unless the destination is `projection.Unchecked` — a bound transaction for the
  destination. Any of them missing is a refusal and never a fallback to
  `AfterApply`.
- **And its boundary, stated so the invariant is not read wider than it is.**
  Atomicity is a property of one transaction, so `InUnit` promises it for the
  writes that are in that transaction and for nothing else. The framework cannot
  see where a handler writes: a `Destination` that names one resource while the
  handler writes to another, and `Unchecked` itself, are outside every check.
  What the invariant forbids is the framework **silently** deciding for the
  consumer — every downgrade it can see is a refusal, and the one it cannot see
  is a load-bearing obligation stated in §3.4 with a case that measures what
  breaking it costs. §12.7 is decided by this and is no longer a tension.
- **Falsified by** §UC-107's five halves, each asserting the refusal and each
  asserting no page was applied; §UC-105/§UC-106's kill-point pair, where the two
  modes leave measurably different state; and §UC-128's three wirings, which
  assert the refusal for the checkable one and the measured `AfterApply`-shaped
  outcome for `Unchecked`, so the boundary is a measurement rather than a
  sentence.

#### INV-071 The log is read outside every unit of work
- **Statement** No read the projection issues runs on a context carrying a
  transaction of the log's backing. The unit is entered after the page is in
  hand.
- **Falsified by** a live case over a log containing a **burnt gap**: a projection
  in `InUnit` mode must pass the gap and keep applying. A projection that read
  inside its own unit stops at that gap for the life of the transaction and makes
  no progress at all, which is a hang rather than a wrong answer — so the case
  carries a deadline and reports the hang as the failure. Beside it, a
  recording `Log` decorator asserting every `ReadAll` arrived on a context with
  no executor of that backing bound.

#### INV-072 Delivery is at least once, and no wording says otherwise
- **Statement** Every delivery path in phase 3 is at-least-once. `InUnit` makes
  the *transactional* effects of a redelivery invisible; it does not make
  delivery exactly-once, and says nothing about an effect outside the
  transaction.
- **Falsified by** `TestNoDocPromisesExactlyOnceDelivery`, which already walks
  `docs/` and must now read the new module pages, the flow and the decision
  without a claim in any of them; a grep of the `projection` package's own
  comments for the phrase, where no use may be anything but a prohibition; and
  §UC-106, which measures a duplicate rather than arguing about one.

#### INV-073 A projection never skips an event it could not apply
- **Statement** The one spelling that advances the checkpoint past an event that
  was not applied is `Quarantine`. It requires a sink, it records **every
  envelope it passes without applying**, it passes no envelope it could have
  applied — the isolation pass of §3.4 is what buys that — and it is never the
  default. The count of what it passed is on `Progress.Quarantined` and is
  persisted, so a restart does not lose the fact that there are holes.
- **Falsified by** §UC-110 under `Halt` asserting the stored advance is unchanged
  and the offending event is applied after a fixing deploy; §UC-110 under
  `Quarantine` asserting the sink holds exactly the envelopes the checkpoint
  passed unapplied, that the applicable envelopes of that same page are in the
  read model, and that `Progress.Quarantined` rose by one rather than by the
  page's length; and the isolation pass's retryable control, which asserts
  nothing was quarantined at all.

#### INV-074 Nothing starts, nothing is discovered, nothing is logged
- **Statement** `projection.New`, `eventpg.NewCheckpoints` and
  `eventmemory.NewCheckpoints` perform no I/O, start no goroutine and read no
  environment. No package-level mutable state exists. The projection writes to no
  logger and imports neither `port` nor `health`.
- **Falsified by** `scripts/`'s `startsNothing` walk extended to the new package;
  a source check for `log.`, `fmt.Print` and `os.Getenv` outside tests; and
  `scripts/event_test.go`'s extension-cost row, whose allowance closure does not
  contain `port` or `health`.

#### INV-075 A projector holds a log and cannot append
- **Statement** `Spec.Log` is an `event.Log`, and `New` refuses a value that is
  also an `event.Store`.
- **Falsified by** §UC-115's pair — the refusal and `event.ReadOnly` accepted —
  and a compile-time assertion that `Spec` has no field of type `event.Store`.

#### INV-076 Phase 3 adds no sentinel and no class
- **Statement** The refusal vocabulary stays at twenty-four sentinels in six
  classes. A checkpoint store classifies its own failure through the existing
  seven `Outcome` values and the kernel maps them with the existing function; a
  fenced save is `Conflict` because a checkpoint row is a one-row stream. A
  checkpoint store that answers a row that is not this projection's is
  `ErrWrongStore`, which is the existing wiring class and is terminal.
  `projection.ErrSpec`, `projection.ErrHalted` and `projection.ErrUnrouted` are
  package-local, are construction, lifecycle and routing errors, and never cross
  a store seam.
- **Falsified by** the vocabulary table test, which reads
  `event.vocabulary()` and must still hold twenty-four; and a test walking every
  error the two checkpoint stores can produce, asserting each is nil, a bare
  context error, or an `event.Failure` carrying one of the seven.

#### INV-077 Progress is an observation and cannot become a resume point
- **Statement** `Progress` is written beside a cursor and read by nobody who
  resumes. No constructor accepts a `Progress` and answers a `Cursor`; no read
  path takes one.
- **Falsified by** a surface check over the regenerated `docs/api/surface.md`;
  and §UC-121, which asserts the cutover comparison is over `Highest` and that no
  comparison of two cursors is expressible.

#### INV-078 A cursor a store mints fits the published ceiling
- **Statement** `MaxCursorBytes` bounds what a store may mint, is checked where
  store honesty is already checked, and is the operand of the checkpoint
  column's constraint — so a checkpoint store never refuses a legitimate cursor.
- **Falsified by** a `Log` decorator answering a cursor of `MaxCursorBytes + 1`
  and asserting `Reader.Next` refuses it as `ErrBackend`; a `Checkpoints` whose
  `Load` answers one, asserting `Tracker.Load` refuses it as `ErrWrongStore`
  (§INV-082); and a live save of a cursor of exactly `MaxCursorBytes`
  succeeding.

#### INV-079 Full replay stays the only authority
- **Statement** Phase 3 ships no snapshot, no memo and no cache of a folded
  state. §INV-008 is unchanged.
- **Falsified by** the grep §INV-008 already names, extended to `projection`;
  and the absence of any snapshot symbol from the regenerated surface baseline.

#### INV-080 The conformance suite reports a cursor that skips an in-flight position
- **Statement** `eventtest`'s `resumption` section walks the log while a lower
  position is uncommitted, so a store whose cursor is its newest position is
  reported rather than certified.
- **Falsified by** §UC-124's decorator over the real store, which must fail the
  section, paired with the unmodified store, which must pass it. Before this
  change the same decorator passed all twenty sections, measured (`## P2` §59) —
  so the pair is evidence rather than decoration.

#### INV-081 A projection's state is a function of the log and its declared routes
- **Statement** A `Router`'s covered families are the families of the facts
  registered on it, and inside a covered family every recorded type is either
  routed by `On` or declared by `Ignore`; anything else is `ErrUnrouted`, which
  the default classifier calls permanent, and the projection halts. Outside every
  covered family an envelope is skipped, counted by `Router.Skipped()` and
  carried nowhere else. So what a projection did not apply is either declared, or
  a family it has no business with, or a halt — and never a silence.
- **Falsified by** §UC-130's pair: one `On` removed inside a covered family must
  halt on the first such event, and the same removal outside every covered family
  must skip and count. A router that refused both, or skipped both, fails one
  half each, which is what makes the pair discriminating. Beside them, a case
  asserting `Ignore` of a covered family's type applies nothing and refuses
  nothing.

#### INV-082 A checkpoint store's answer is re-checked at the door, like every other store's
- **Statement** No caller reaches a `Checkpoints` except through `event.Track`.
  The door refuses a name the kernel's identifier rule refuses; and, on every
  `Load`, an answer whose `Projection` is not the name asked for, whose absence
  is partial (`Advance == 0` beside a cursor, a progress or a name), or whose
  cursor is over `MaxCursorBytes`. The refusal is `ErrWrongStore`, it is
  terminal, and the row is not written over.
- **Falsified by** §UC-129's three defective stores, each asserting the refusal,
  that no read was issued from the answered cursor and that the stored row is
  byte-identical afterwards — paired with the honest store, which must resume
  through the same door, so the check is discriminating rather than universal.
  And §UC-123's harness, which must report the same defect by section, because a
  door that catches it silently teaches its implementer nothing.

---

## 9. The exported Go surface

Every name below is what `make api` must show, and nothing else is exported.
Receiver name is `this` throughout.

### 9.1 `github.com/frostgrove/vv/event` — additions

**Nothing existing changes.** No signature moves, no type gains a field, no
sentinel is added or removed. These are additions, and they are the whole of what
moves the frozen kernel:

| File | What moves | Why it cannot live elsewhere |
|---|---|---|
| `event/checkpoint.go` (new) | `Checkpoint`, `Progress`, `CheckpointCapabilities`, `Checkpoints`, `Track`, `Tracker` | Two store packages implement it and a third must be provable against it; a contract that lived in the consumer would make every store depend on the consumer. `Track` is the door, and it is here for the reason `Read` is: a second consumer of `Checkpoints` must inherit the re-checks rather than re-derive them (§INV-082) |
| `event/bounds.go` | `MaxCursorBytes` | It is a ceiling on what a store mints, and the ceilings are one list |
| `event/reader.go` | `checkPage` also bounds the returned cursor | Store honesty is checked in one place |
| `event/fact.go` | `Fact.Family`, `Fact.Read` | Only `Fact` holds the reader chain; a decoder outside it would need the chain exported |
| `event/eventtest/` | `RunCheckpoints`, `CheckpointFactory`, the checkpoint sections, and the in-flight case in `resumption` | The suite is the kernel's evidence half |

```go
package event

const MaxCursorBytes = 4096

// Written at one point and one only: the save that carries it. So it describes
// a page that was applied or quarantined, never one that was merely delivered,
// and a projection retrying or halted on the page it just read publishes what
// it had before that page.
type Progress struct {
	// The highest position of a page this projection finished with. An
	// observation: every committed event at or below it was delivered and was
	// then applied or quarantined, and it resumes nothing.
	Highest Position

	Applied uint64

	// How many envelopes went to a quarantine sink instead of the read model.
	// Non-zero means the destination has holes, which is what keeps Highest from
	// reading as a completeness claim.
	Quarantined uint64

	At time.Time
}

// The resume authority is Cursor. Advance is a fence and not a position;
// Progress is an observation and not a resume point.
type Checkpoint struct {
	Projection string
	Cursor     Cursor
	Advance    uint64
	Progress   Progress
}

func (this Checkpoint) Fresh() bool

type CheckpointCapabilities struct {
	Transactions Support
	Persistence  Support
}

// Save admits a checkpoint if and only if the stored advance is one below the
// one presented; a losing save is Failure(Conflict, ...). Load answers the zero
// checkpoint when there is none, and Fresh says so. None of the seven opens,
// commits or rolls back anything.
type Checkpoints interface {
	Capabilities() CheckpointCapabilities
	Backing() Backing
	Transaction(ctx context.Context) (Authority, error)

	Load(ctx context.Context, projection string) (Checkpoint, error)
	Save(ctx context.Context, checkpoint Checkpoint) error
	Forget(ctx context.Context, projection string) error
	Close() error
}

// The one door onto a Checkpoints, as Read is onto a Log: it holds the name,
// owns the fence and re-checks every answer. Nothing calls a Checkpoints raw.
func Track(checkpoints Checkpoints, projection string) (*Tracker, error)

type Tracker struct{ /* unexported */ }

func (this *Tracker) Projection() string
func (this *Tracker) Capabilities() CheckpointCapabilities
func (this *Tracker) Backing() Backing
func (this *Tracker) Transaction(ctx context.Context) (Authority, error)

// ErrWrongStore when the answer is not about this name, when its absence is
// partial, or when its cursor is over MaxCursorBytes.
func (this *Tracker) Load(ctx context.Context) (Checkpoint, error)

// The advance is the one this tracker loaded plus one. A save before a load is
// refused, so no caller can present another.
func (this *Tracker) Save(ctx context.Context, cursor Cursor, progress Progress) error

func (this *Tracker) Forget(ctx context.Context) error

func (this *Fact[S, ID, E]) Family() string

// The recorded bytes through this fact's own reader chain and every declared
// upcaster, in the current reader type. The four history-class refusals a replay
// produces, and no other.
func (this *Fact[S, ID, E]) Read(envelope Envelope) (E, error)
```

### 9.2 `github.com/frostgrove/vv/event/projection` — new package, root module

```go
package projection

var (
	ErrSpec     = errors.New("projection: this projection cannot be assembled from this spec")
	ErrHalted   = errors.New("projection: this projection stopped advancing and is not applying events")
	ErrUnrouted = errors.New("projection: this envelope's type is of a family this router routes and no route claims it")
)

// The zero value resolves to AfterApply, which is the mode that promises less.
type Advance uint8

const (
	UnsetAdvance Advance = iota
	AfterApply
	InUnit
)

func (this Advance) Valid() bool
func (this Advance) String() string

type Phase string

const (
	PhaseStarting  Phase = "starting"
	PhaseDraining  Phase = "draining"
	PhaseFollowing Phase = "following"
	PhaseRetrying  Phase = "retrying"
	PhaseHalted    Phase = "halted"
)

// Yours from the moment it is handed over, including the slice's capacity and
// every payload in it: keep it, read it from any goroutine, write into it. Each
// attempt is handed its own, so a page you rewrote in place is not what the
// retry applies.
type Batch struct {
	Projection string
	Envelopes  []event.Envelope
	Attempt    int
}

type Handler interface {
	Apply(ctx context.Context, batch Batch) error
}

type HandlerFunc func(ctx context.Context, batch Batch) error

func (this HandlerFunc) Apply(ctx context.Context, batch Batch) error

type Verdict uint8

const (
	Retryable Verdict = iota
	Permanent
)

type Classifier func(err error) Verdict

// The history class and ErrUnrouted are permanent — a payload this build cannot
// read does not become readable by trying again, and neither does a type
// nothing routes — and everything else is retryable.
func Classify(err error) Verdict

type Failure uint8

const (
	Halt Failure = iota
	Quarantine
)

type Quarantines interface {
	Quarantine(ctx context.Context, projection string, envelope event.Envelope, cause error) error
}

type State struct {
	Projection string
	Phase      Phase
	Progress   event.Progress
	Attempt    int
	Err        error
	At         time.Time
}

type Observer interface {
	Observed(state State)
}

type ObserverFunc func(state State)

func (this ObserverFunc) Observed(state State)

type Backoff struct {
	First time.Duration
	Max   time.Duration
}

type Spec struct {
	Name        string
	Log         event.Log
	Checkpoints event.Checkpoints
	Handler     Handler

	Advance Advance

	// Required by InUnit and refused beside AfterApply. The framework opens no
	// transaction: crud.InNewTx(ctx, source, work) is the one-line spelling.
	//
	// Under InUnit it must open ONE transaction, on the resource the handler
	// writes to and the checkpoint store lives in, and the handler must write
	// through the context it is given. That is what InUnit's atomicity is a
	// property of, the framework checks as much of it as it can see (§3.2(3)),
	// and the rest is an obligation it cannot check (§3.4, §UC-128).
	Unit func(ctx context.Context, work func(context.Context) error) error

	// The handle the handler writes through — a crud.Source, a *sql.DB, whatever
	// the application's own data source is. Required by InUnit, which resolves it
	// inside the unit, before the handler runs, and refuses the pass when what is
	// bound for it is not a transaction. Unchecked is the one way to say it
	// cannot be resolved that way, and it says so where a reviewer reads the
	// composition rather than by a field left zero.
	Destination any

	Idle     time.Duration
	Wake     <-chan struct{}
	Backoff  Backoff
	Attempts int
	Tolerate time.Duration

	OnPermanentFailure Failure
	Quarantine         Quarantines

	Classifier Classifier
	Observer   Observer
	Ticks      runtime.Ticks
}

// The destination that cannot be resolved through crud's binding — a document
// store, a search index, a second database. InUnit with this is InUnit with the
// alignment obligation and no check on it.
var Unchecked any = unchecked{}

type Projection struct{ /* unexported */ }

var _ runtime.Runner = (*Projection)(nil)

// Performs no I/O, starts nothing, reads no environment.
func New(spec Spec) (*Projection, error)

func (this *Projection) Name() string
func (this *Projection) Declaration() runtime.Declaration
func (this *Projection) Run(ctx context.Context) error
func (this *Projection) Drain(ctx context.Context) error
func (this *Projection) Ready(ctx context.Context) error
func (this *Projection) State() State

// What to do with an envelope of a family this router covers no route of. A
// type of a family it DOES route and no route claims is never skipped under
// either: it is ErrUnrouted, and that is the forgotten registration.
type Foreign uint8

const (
	SkipForeign Foreign = iota
	RefuseForeign
)

type Router struct{ /* unexported */ }

var _ Handler = (*Router)(nil)

func NewRouter(foreign Foreign) *Router

// Declaration-shaped: On and Ignore panic, TryOn and TryIgnore return the error
// ([[D-123]]). On also declares its fact's family as covered.
func On[S, ID, E any](router *Router, fact *event.Fact[S, ID, E], apply func(context.Context, E, event.Envelope) error)
func TryOn[S, ID, E any](router *Router, fact *event.Fact[S, ID, E], apply func(context.Context, E, event.Envelope) error) error

// A covered family's types this projection deliberately does not want. By name
// rather than by fact, so a type this build declares no fact for can be named
// too — and so a renamed wire type leaves the declaration stale in the safe
// direction: the old name stays ignored and the new one halts.
func Ignore(router *Router, family string, types ...string)
func TryIgnore(router *Router, family string, types ...string) error

func (this *Router) Apply(ctx context.Context, batch Batch) error

// Envelopes skipped as foreign. There is no second counter, because inside a
// covered family this router skips nothing. Safe for concurrent use.
func (this *Router) Skipped() uint64
```

**Defaults, and each is the one that promises less.** `Advance` zero →
`AfterApply`. `Idle` zero → 1 s. `Backoff` zero → `{First: 250ms, Max: 30s}`,
doubling, **without jitter** — a projection is a singleton per name, so there is
no herd to spread. `Attempts` zero → 10, and it bounds handler failures only: a
store failure retries without limit, because the database coming back is the
normal case and nothing is lost while the checkpoint stands still. `Tolerate`
zero → 1 min. `Classifier` nil → `Classify`. `Ticks` nil →
`runtime.SystemTicks`. `OnPermanentFailure` zero → `Halt`. `Foreign` zero →
`SkipForeign`, and it is the one zero value that promises less *refusal* rather
than less outright: a global log carries every aggregate, so refusing every
other family would make a router useless, while the mistake that used to hide
behind a skip — an unclaimed type of a family this router does route — is now a
halt no policy relaxes. `Spec.Destination` has **no** default under `InUnit`: an
unstated one is refused at `New`, because the answer "I cannot check this" has
to be written rather than defaulted into (§UC-107(c)).

**What is deliberately absent:**

| Not exported | Why |
|---|---|
| `Reset`, `SetCheckpoint`, `Rewind` | A rebuild is a second name (§6). An API that clears a live checkpoint has a typo's blast radius and is indistinguishable afterwards from corruption |
| `Resume`, `Retry`, `Clear` on a halt | A halt is terminal for that value's life (§3.2(6)). The classifier said the failure was permanent; a halt that can be cleared from inside is a retry loop with a longer period, and one cleared from outside is an operator retrying the thing they were told will not clear. The exit is a new value in a new process, after the fix |
| `WaitUntilCaughtUp`, `Await` | §4.3. The sound predicate exists; the ergonomics around it do not, and the application's own read-model column is exact |
| `Head`, `Tail`, `Lag` | There is no head (§4.1). A lag in events would be a subtraction of positions, which is §1.4's forbidden arithmetic wearing a dashboard's clothes |
| A parallel or sharded projector | Real feature, real ordering questions, no consumer |
| A snapshot of any kind | §5.2 |
| A retry, backoff or circuit-breaker option on the store | [[D-040]] |

### 9.3 `event/eventmemory` and `event/eventpg` — additions

```go
package eventmemory

type CheckpointSpec struct{ Log *Log }

type Checkpoints struct{ /* unexported */ }

var _ event.Checkpoints = (*Checkpoints)(nil)

func NewCheckpoints(spec CheckpointSpec) (*Checkpoints, error)
```

```go
package eventpg

const SchemaVersion = 2 // was 1

type CheckpointSpec struct {
	DB     *sql.DB
	Source crud.Source

	Schema           Schema
	SchemaManagement SchemaManagement
}

type Checkpoints struct{ /* unexported */ }

var _ event.Checkpoints = (*Checkpoints)(nil)

func NewCheckpoints(spec CheckpointSpec) (*Checkpoints, error)

func (this *Checkpoints) Prepare(ctx context.Context) error
func (this *Checkpoints) Check(ctx context.Context) error
```

`Store` and `Checkpoints` over one schema are two resources at one schema
version, so a deployment migrates once and each verifies at its own `Prepare`.

### 9.4 `event/eventtest` — additions

```go
package eventtest

type CheckpointFactory struct {
	New func(t *testing.T) event.Checkpoints

	// Begins a transaction the checkpoint store joins, and returns the context
	// carrying it. Required when the store claims Transactions.
	Begin func(t *testing.T, ctx context.Context, c event.Checkpoints) (context.Context, Tx)

	// A second value over the same backing, which is what a restart is.
	// Required when the store claims Persistence.
	Sibling func(t *testing.T, c event.Checkpoints) event.Checkpoints

	// A cursor this factory's log store would mint, so a section can save one
	// that is real rather than a literal the suite invented.
	Cursor func(t *testing.T) event.Cursor

	// The grain this store keeps Progress.At at, which only the store knows.
	// Every instant the suite mints goes through it, so what a section asserts
	// is the round trip and not the precision. Absent means exact.
	Instant func(minted time.Time) time.Time

	Window time.Duration
}

func RunCheckpoints(t *testing.T, factory CheckpointFactory)
```

Sections: `binding`, `absence`, `round trip`, `fence`, `forget`, `names`,
`bounds`, `refusal classes`, `lifecycle`, `concurrency`, `transactions`
(needs `Transactions`), `durability` (needs `Persistence`). The same three
anti-vacuity rules as `Run`: a claimed capability with no hook is fatal, an
unstated capability is fatal, and a run that certified nothing fails. `Cursor`
and `Instant` carry their own, for the same reason — a hook the suite mints
through is one a store could answer vacuously: two calls to `Cursor` must answer
two cursors, and `Instant` must round rather than invent (idempotent) and must
keep two instants a second apart apart.

**`Instant` was added after S2's round-1 review (GAP-2), and the reason is
measured.** The suite minted `time.Now().UTC().Truncate(time.Microsecond)` and
compared it for exact equality in every section that reads a row back —
microsecond is PostgreSQL's `timestamptz` grain and nothing else's, and no rule
of this section ever asked a store to round-trip `At` at all. A fixture honouring
every rule §3.1 does state and differing only in the grain of its instant column
was reported broken on **ten of twelve** sections at millisecond and at second
grain, against a microsecond control that certified twelve. `RunCheckpoints` is
§UC-123's deliverable, so a store over MySQL `DATETIME(3)`, SQLite or a Redis
hash of Unix millis was told its fence and its cursor were broken. The obligation
is now stated where it belongs — on `Progress.At`, a store answers what it was
handed — and the grain is the store's to declare.

`names` carries the defect `event.Track`'s door also refuses: two projections
saved, each `Load`ed, each answering **its own** row — which a store that
ignores the `projection` argument fails. The suite runs against the raw
`Checkpoints` rather than through the door, precisely so an implementer is told
about it instead of having the kernel quietly cover for them (§UC-129).

---

## 10. What the live suite must prove

The gate names its own command, unchanged from phase 2:

```
FROSTGROVE_EVENTPG_TEST_DSN='postgres://vv:vv@localhost:55432/vv?sslmode=disable' \
  go test -race -count=1 -tags=integration ./event/eventpg/...
```

An unset DSN **fails** the gate. A reviewer checks these off, and each has a
control:

1. **The whole existing conformance run, unchanged and still green** at the
   defaults and at the narrow limits, plus the mutation harness — the schema
   moved to version 2 and nothing about the append or the walk may have moved
   with it.
2. **`eventtest.RunCheckpoints` against live `eventpg`**, and its mutation
   harness: a checkpoint store that ignores the fence, one that answers a stale
   cursor, one that reports absence for a row that exists, one that saves outside
   the caller's transaction. Each must be reported by a named section
   (§UC-123).
3. **The fence under real concurrency** — §UC-103: eight goroutines saving at
   one advance, exactly one winner, seven `ErrConflict`, and a single saver
   afterwards as the control.
4. **The two modes told apart by a kill point** — §UC-105, §UC-106: the same
   projection under `InUnit` and `AfterApply`, interrupted between the handler's
   commit and the advance, asserting the read model's rows and the stored
   advance in both cases against what the database actually holds. And the
   **third** wiring beside them — §UC-128: the same handler writing to a second
   live database, killed at the same point, asserting the rows survive there
   while the advance rolled back here, so `InUnit`'s precondition is a
   measurement rather than a paragraph.
5. **The in-flight watermark, end to end through a projection** — §UC-113: a
   writer holding `p` uncommitted, the projection must apply nothing at or beyond
   it, then apply `p` and `p+1` in order after the commit. The control is the
   same projection over a quiescent log, which must not stall.
6. **The burnt-gap case in `InUnit` mode** — §INV-071: a rolled-back append
   between two committed ones, a projection that must pass it and keep applying,
   under a deadline so a read issued inside the unit reports as a hang rather
   than as a timeout nobody reads.
7. **`resumption` with the late writer's transaction held open** — §UC-124, and
   the decorator that answers `max(position)` must now fail it. This is the case
   `## P2` §59 records as undetected, and it is the one that makes every other
   claim in this document about checkpoints mean something.
8. **The schema migration from version 1 to version 2** — a v1 schema with rows
   in it, migrated, verified at all three levels, and the events read back
   unchanged; plus a fresh database taking the same v2 list; plus the two
   replicas racing `Prepare` (§UC-073) at the new version.
9. **Level 3 over the fourth table** — the same removed/added matrix §UC-072
   already runs, extended: the checkpoints primary key dropped, a check
   constraint altered, a trigger added, row-level security enabled. And the
   exempt control that must still pass.
10. **The replay-cost benchmark** — §UC-125, reproducing §5.1's table within an
    order of magnitude on the gate's own hardware.
11. **A full rebuild and cutover, live** — §UC-120, §UC-121: two projections over
    one log with two destinations, the second built from zero while the first
    keeps applying, `Highest` compared, **the two destinations' rows compared
    over a quiescent log**, then the rollback with nothing restored. The row
    comparison is the item, not the number: its control is the deliberately
    wrong `orders-v2` that reaches the same `Highest` and holds different rows.
12. **A router with a deliberately missing registration** — §UC-130: over a live
    log carrying three aggregates, one `On` removed inside a covered family must
    halt the projection at the first such event with `ErrUnrouted` and leave the
    checkpoint where it was, and the same removal outside every covered family
    must be skipped, counted and invisible. The control is the complete router,
    which must reach `PhaseFollowing` with the rows all four appliers wrote.
13. **A quarantine that is envelope-granular** — §UC-110: a live page in which
    one payload is corrupt and the rest apply, asserting against the database
    that the sink holds one envelope, that the read model holds the rest, and
    that `quarantined` in the checkpoint row is 1 rather than the page's length.
14. **Nothing under `event/` outside the enumerated files moved** — §9.1's table
    is the whole of the kernel diff, and `check-event-kernel` re-baselined
    against it is what says so.

---

## 11. Deliverables that are not code

### 11.1 The kernel baseline, moved deliberately

`check-event-kernel` exists because phase 2 had to prove it needed no kernel
edit. Phase 3 needs five (§9.1), so the arm moves — and how it moves is the
thing that is written down or is not real:

- A new commit under `event/` outside `event/eventpg` fails the arm against the
  recorded baseline, and the baseline can only be set to a commit that already
  exists. So a naive re-baseline is two commits with a red `make check` between
  them. **The plan decides between two spellings and records which**: (a) land
  the kernel additions, then set `EVENT_KERNEL_BASELINE` to that commit in the
  next one and accept one red commit, or (b) freeze a **content digest** of the
  files under `event/` outside `event/eventpg` instead of a revision, which
  re-baselines atomically in the same commit and, as a side effect, closes
  `## P2` §5 — the arm cannot currently run outside a git checkout.
- Whichever is chosen: `scripts/checks.sh`'s comment gains a sentence naming
  **what moved and why**, listing §9.1's five entries; `scripts/checks_test.go`
  keeps its self-test that the arm reports a difference when a frozen file
  differs and reports ok when only `event/eventpg` does; and the arm still
  refuses rather than passing when it cannot run.
- The check is **never loosened**. Widening the pathspec, adding an exclusion for
  the new files, or turning the arm into a warning is the failure this paragraph
  exists to prevent.

### 11.2 Documentation, in the same change as the code

- `docs/modules/en/projection.md` and `docs/modules/ru/projection.md`, with
  their rows in both `Index.md` files. Four rows in those pages are contracts
  rather than prose: a **checkpoint row** saying that a checkpoint is a cursor
  and that `Progress` resumes nothing; a **delivery row** saying at-least-once
  in both modes, what `InUnit` does and does not make atomic, **and its
  precondition in the same breath** — the handler's writes are in the unit's
  transaction or `InUnit` is `AfterApply` with a different name (§3.4, §UC-128);
  a **page row** saying each attempt gets its own page and the handler may do
  what it likes with the one it holds; and a **routing row** saying that an
  unclaimed type of a routed family halts and a foreign family is skipped.
- §INV-021's hand-off enumeration gains its **eighth** row — framework →
  handler, `Batch.Envelopes` and its payloads, the first hand-off whose sender
  re-reads what it handed over, made safe by the sender with a copy per attempt
  (§3.3). It is appended rather than inserted, which is what that invariant
  provides for, and the ordinal is cited from `docs/modules/{en,ru}/projection.md`
  and from the new flow.
- `docs/modules/{en,ru}/eventpg.md` gain the checkpoints table, schema version 2
  and the migration; `event/eventpg/MIGRATIONS.md` gains the v1→v2 row.
- A new flow, the next free number **FL-038**, "a settled cursor becomes a
  durable checkpoint", with its row in `docs/ai/flows/Index.md`, its question row
  in the reverse lookup, and a source-file row for every non-test file in
  `event/projection` and the new files in `eventmemory`, `eventpg` and
  `eventtest`. `FL-036` and `FL-037` gain rows for the kernel and store files
  that changed.
- The `UC-032` row in `docs/ai/usecases/Index.md` extended, and
  `docs/roadmaps/Roadmap.md`'s E2 line rewritten rather than annotated done —
  including the snapshot deferral and its measured number, so the next reader
  finds the decision and not the absence.
- `make api` regenerated. `event`, `event/eventmemory`, `event/eventpg` and
  `event/eventtest` gain lines, and `event/projection` gains a section.

Four decision docs are owed, at the next free numbers after `D-127`:

- **A checkpoint is a store-minted cursor, never a position.** §1 is the
  argument; the decision is what stops the next maintainer adding
  `Checkpoint.Position` for a dashboard. Links [[D-121]], §UC-037, §INV-035,
  §INV-066.
- **A projection is a supervised runner and the checkpoint advance is the
  caller's unit of work.** Why the framework opens no transaction for a
  projection, why `InUnit` is refused rather than downgraded and what its
  precondition is, why the read is outside the unit, why a halted projection
  does nothing at all and never clears, and why each attempt is handed its own
  page. Links [[D-092]], [[D-118]], [[D-126]], §INV-021, §INV-070, §INV-071.
- **A router's coverage is inferred from its routes, and an unclaimed type
  inside it is a refusal.** Why a missing `projection.On` must not be a skip,
  why the skip that remains is not on `State`, and why `Ignore` is by name
  rather than by fact. Links [[D-123]], §INV-081.
- **A snapshot is added from a measured need, not a measured cost.** The number,
  the trigger and the contract, so the deferral is a decision with evidence
  rather than an omission. Links §INV-008, §INV-079.

### 11.3 The extension-cost row

`scripts/event_test.go`'s `charged` map gains
`eventExtension + "/projection": "./runtime"`. That allowance's first-party
closure is `crud`, `errs`, `runtime` and `utils` — which is why the package must
not import `port`, `health`, `jobs` or a store. The `charged` map's value is
currently one package pattern and the row needs exactly one, so nothing about
the check's shape changes; if a later phase needs two, the map's value type
widens and that is a decision recorded there.

`startsNothing` and `noBaseSubsystemDependsOn` cover the new package without
change, and the latter is what keeps a projection from becoming compulsory for
every consumer of the root module.

---

## 12. Tensions and open questions, left to the plan

Each is named so it is decided rather than discovered.

1. **The v2 migration list has two jobs.** Version 1's list creates what is
   absent and asserts; version 2's must both create everything from empty *and*
   transform a deployed v1. §2.2 prescribes the shape for the second (an
   `UPDATE` guarded on the version it migrates from, then the assertion) and the
   plan decides whether one list does both or `MigrationStatements` grows a
   from-version parameter. A list that silently stamps a v2 fingerprint onto a
   schema it did not transform is the failure §UC-074 already names.
2. **`Batch.Attempt` after a crash.** §UC-106 says a redelivery after a restart
   arrives with `Attempt == 1`, because attempts are per-process and nothing
   durable counts them. The plan decides whether that is enough or whether the
   checkpoint should carry a redelivery count — and, if it should, what a
   handler is expected to do with a number that only rises after a crash.
3. **`Tolerate` and readiness flap.** A readiness answer that goes unhealthy on
   one transient error takes a replica out of rotation for a database hiccup; one
   that never flags a projection stuck for an hour is useless. The default is
   1 minute; the plan decides whether the streak is measured from the first
   failure or from the last success, and pins it with a test that a single
   failure followed by a success never reports unhealthy.
4. **Quarantine granularity — decided in §3.4, and what is left of it.** The
   granularity is the envelope, bought by re-delivering the failed page one
   envelope at a time rather than by giving the handler a way to name the
   offender; a page-granular quarantine would drop up to `MaxRead` applicable
   events for one corrupt payload. What the plan still decides is narrower:
   whether the sink is called inside the unit under `InUnit` — the framework
   does not require a sink to be transactional, and a sink that joins the unit
   and one that does not are both legal, so the module page has to say which one
   a `Quarantines` implementer is writing.
5. **`Fact.Read` and payload ownership.** `Read` returns a value that may alias
   the envelope's payload, which is the same rule the replay path already
   follows. The plan pins it with a case, because a handler that keeps the value
   and lets the page go is holding a slice whose ownership it now has to reason
   about.
6. **The `Router`'s cost at scale.** A map lookup per envelope per page is
   nothing, but the router holds a closure per declared fact and phase 3 does not
   bound how many. The plan decides whether a bound exists, and says why if it
   does not.
7. **A checkpoint store in another database — the semantics are decided, the
   example is not.** `eventpg.Checkpoints` is same-database by construction,
   which is what makes `InUnit` expressible for a read model in that database. A
   read model elsewhere is §UC-128: `AfterApply` is the honest mode, `InUnit`
   with a resolvable `Destination` is refused on the first pass, and `InUnit`
   with `Unchecked` gets `AfterApply` semantics under an obligation the consumer
   accepted out loud. That is a stated outcome rather than a silent downgrade,
   which is what §INV-070 needed of it. What the plan decides is only whether
   the module page carries a worked `event.Checkpoints` over a second database,
   because without one every such deployment writes the fence from scratch.
8. **Snapshot re-entry.** §5.2's trigger is a p99 above ~50 ms measured by a
   deployment. The plan records where that measurement is expected to be made
   and by whom, so the deferral has an owner rather than a hope.
9. **Whether a projection emits a line at all.** Three answers, and §3.5 takes
   the first: **(a)** no line — everything through `Observer`, `State` and
   `Ready`, and the package's closure stays at `./runtime`; **(b)**
   `port.Logger(ctx)` for the failures nobody can be returned an error for,
   which is [[D-062]]'s own answer and which widens
   `scripts/event_test.go`'s `charged` value from one package pattern to a list;
   **(c)** a `Spec.Logger *slog.Logger`, which is what `runtime.PeriodicSpec`
   and `runtime.LoopSpec` next door already do and which needs no new import at
   all. The plan decides, and if it stays with (a) it says so in the decision doc
   §11.2 owes, because "this package deliberately emits nothing" is the kind of
   claim that gets quietly reversed by the first person who wants to debug a
   halt.
