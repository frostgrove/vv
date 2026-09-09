# EVENTSOURCE — the PostgreSQL event-sourcing reference, adjudicated against `event/`

**Reference:** `github.com/eugene-khyst/postgresql-event-sourcing` @ `90faafb`, clone at `/tmp/pges-ref`.
**Second witness:** its Kotlin port in production at
`photon/new/kotlin/platform-eventsourcing` — where it *diverges*, somebody hit something.
**Target:** `github.com/frostgrove/vv` — `event/`, `event/eventmemory/`, `event/eventpg/`,
`event/projection/`, mid phase 3 (S1 `[x]`, S2 `[x]`, S3/S4/S5 `[ ]`).

**Method.** Every verdict below is decided against the vv source, not against the mining
summary. "Already have" carries a file and line and a statement of whether vv's mechanism is
*genuinely equivalent* or merely aimed at the same problem. "Adopt" names the failure in vv's
current code with the interleaving that triggers it. "Reject" cites a house rule or a decision
doc, and still records what vv must prove in place of the mechanism it refused.

---

## The one-paragraph verdict

**vv's write path is already the reference's, and in three places stronger.** The CAS, the
`UNIQUE` backstop, the parent row and its foreign key, the opaque type discriminator and the
absence of any clock in the ordering are all present, and the isolation-level dependency the
reference leaves *unstated* is a written decision here ([[D-126]]) proved at all three levels.
**vv's read path is the one place the reference has something vv does not**, and it is the
already-recorded `## P2 — the read path should carry the writing transaction's xid8 [high]`
entry. But that entry is scoped wrong in one respect that matters right now: it says "No kernel
change." **That is false.** The reference's delivery order is `(TRANSACTION_ID, ID)`, in which
`ID` — vv's `position` — is **not monotone**, and three kernel/suite sites currently *refuse* a
page whose positions do not ascend. Phase 3 is about to freeze all three. **That is the only
p3-blocking item, and it is a contract decision, not code.** Everything else is P4 or backlog.

---

## The blocking item, stated once, in full

> **DECIDED, 2026-09-09 — [[D-128]], `docs/ai/decisions/D-128-the-log-delivers-in-position-order.md`.**
> Position-ascending delivery is a **kernel law**, and so is one stream's order being a
> subsequence of the log's. There is no capability for either. `Progress.Highest` keeps its
> completeness meaning. **A1's tuple read is refused** — not because vv's page is simpler, but
> because the reference's order presumes the writing transaction takes its id at the append, and
> vv's store joins a transaction the caller opened ([[D-118]], [[D-126]]). Measured on 17.9: an
> append at version 2 from a transaction stamped earlier sorts before the same stream's version 1,
> so the tuple read reorders one stream against itself; the control without the earlier write
> delivers in order. Everything below this box is the record of the question, kept as it was
> asked. Read D-128 before reopening any of it.

### The kernel currently forbids the reference's delivery order

The reference delivers `ORDER BY e.TRANSACTION_ID ASC, e.ID ASC`
(`postgresql-event-sourcing-core/.../repository/EventRepository.java:87`; README §4-7 step 2).
`TRANSACTION_ID` is the writing transaction's `xid8`, assigned at its **first write anywhere**;
`ID` is a `BIGSERIAL` drawn at the **event insert**. The two orders are independent, so `ID`
can go **backwards** inside one page.

The concrete interleaving, which the reference's own model produces:

```
tx A:  BEGIN; UPDATE ES_AGGREGATE …            -- xid 100 assigned here
tx B:  BEGIN; UPDATE ES_AGGREGATE …            -- xid 101 assigned here
tx B:  INSERT INTO ES_EVENT …                  -- draws ID 5
tx A:  INSERT INTO ES_EVENT …                  -- draws ID 6
both commit
```

The tuple read hands back `(100, 6)` then `(101, 5)` — positions **6 then 5**.

vv refuses exactly that page, in three places:

| Site | What it says |
|---|---|
| `event/reader.go:94-98` | `if page[offset].Position <= page[offset-1].Position { return … ErrBackend … "a page whose positions do not ascend, so a consumer resuming from its cursor cannot tile the log" }` |
| `event/store.go:125` | the `Log.ReadAll` contract: *"Envelopes at **ascending positions** after the cursor"* |
| `event/eventtest/sections_read.go:24, 61-64, 169` | `probe.ascending` — the conformance suite certifies it, twice, over two different reads |

And a fourth site puts a semantic on top of it:

| `event/checkpoint.go:15-17` | `Progress.Highest` — *"The highest position of a page a consumer finished with. An observation: **every committed event at or below it was delivered**"* |

Under tuple order that sentence is false: when `Highest` is 6, position 5 has not been
delivered yet.

**Why this is p3-blocking and the rest is not.** The SQL, the `writer_xid xid8` column, the
`(writer_xid, position)` index and the `vve2` cursor encoding are all contained in
`event/eventpg` and ride mechanisms vv already built for exactly this — a versioned schema
(`eventpg.SchemaVersion`, `event/eventpg/schema.go:16`) and a tagged cursor format
(`cursorTag = "vve1"`, `event/eventpg/cursor.go:20`, whose stated rule is that a build retiring
an encoding refuses the previous one). Those can land in P4 as schema version 3. **The
ordering contract cannot.** `event/reader.go`, `event/store.go` and
`event/eventtest/sections_read.go` are kernel and suite; phase 3's S5 freezes them behind
`check-event-kernel` and the manifest fence, and `Progress.Highest`'s wording is a *published
checkpoint contract* that a third-party `Checkpoints` implementer is told to satisfy. Deciding
this after phase 3 means reopening the frozen kernel, the conformance suite that certifies the
read path, and the checkpoint contract — in that order.

**What must be decided before S3 is signed off** (this document does not decide it):

1. Is position-ascending delivery a **kernel law**, or a **store capability** alongside
   `MonotoneVisibility` (`event/store.go:32-37`, `event/eventtest/inventory.go:44`)? The
   capability shape already exists and is exactly the seam for "this store delivers in an order
   that is not position order".
2. Does `Progress.Highest` mean *"the highest position of the page"* (safe under either order)
   or *"the completeness watermark"* (only true under position order)? If it stays the latter,
   the tuple read needs a second progress field or `Highest` becomes the store's own opaque
   number.
3. Is `Reader.checkPage`'s real invariant *"positions ascend"* or *"the cursor tiles the log"*?
   The comment at `event/reader.go:74-76` says the purpose is "refused before a consumer
   checkpoints past an event it never saw" — which the tuple read guarantees **by
   construction**, more strongly than the ascending check does. The check is a proxy for an
   invariant the tuple read satisfies differently.

**If the answer is "the kernel keeps position order"**, then the `## P2 [high]` backlog entry
is not implementable as written and must be rewritten to say so — because the whole point of
the reference's mechanism is that it abandons position order. Say that out loud rather than
leaving a `[high]` entry that cannot be executed.

---

## Verdict table

`✔` already have · `+` adopt · `✗` reject · `→` backlog / later phase

### Write path

| # | Technique (reference site) | Verdict | vv |
|---|---|---|---|
| W1 | Version CAS on a separate row, **before any event insert** (`AggregateStore.java:39-48`, `AggregateRepository.java:45-60`) | ✔ **stronger** | `event/eventpg/append.go:111-127` — one statement, not two: a CTE `INSERT … ON CONFLICT (family,key) DO UPDATE SET version = s.version + $4 WHERE s.version = $3` feeding the event `INSERT … SELECT`. Same admission predicate, evaluated by PostgreSQL against a row it has locked; **no transaction required**, because one statement is atomic. Speculative insertion covers two writers at version 0. |
| W2 | The CAS returns 0 rows **only under READ COMMITTED**, and the reference never states its level (`grep -rn 'isolation' /tmp/pges-ref` → nothing) | ✔ **much stronger** | `docs/ai/decisions/D-126-*.md` states it, refuses to pick a level, and pins it: `TestTheIsolationMatrixTellsAConflictFromASerialisationFailure` runs one losing append at all three levels. `40001` → `errs.KindRetryable`, never `Conflict` (`event/eventpg/classify.go:69-79`). The reference's silent dependency is vv's written one. |
| W3 | `UNIQUE (AGGREGATE_ID, VERSION)` as the physical backstop (`V1__eventsourcing_tables.sql:16`) | ✔ | `events_stream_version_key UNIQUE (family, key, version)`, `event/eventpg/schema.go:263`. Verification refuses to start against a schema that lost it (D-126 "Proven by", `unique-dropped` case). |
| W4 | Parent aggregate row + FK from every event (`V1:1-5,12`) | ✔ **and the reference's failure mode is absent** | `streams` table + `events_stream_fkey` (`event/eventpg/schema.go:264-269`). The reference's orphan-event hole — an event whose `ES_AGGREGATE` row is missing is invisible to **every** subscription forever, because the read is an INNER JOIN — cannot occur here: `logStatement` (`read.go:317-327`) joins nothing. |
| W5 | `pg_current_xact_id()` stamped on the row as `xid8`, **not `xid`** (`EventRepository.java:36-40`, `V1:11`) | **+ adopt** (eventpg-only, P4) | vv **forces** the xid — trigger `events_position_needs_xid`, `BEFORE INSERT … FOR EACH STATEMENT`, body `PERFORM pg_current_xact_id();` (`event/eventpg/schema.go:281, 289-292`) — and then **throws it away**. Half the mechanism is already deployed. See §Adopt A1. |
| W6 | The port dropped the event's own `version` field; the row is the single authority | ✔ | vv never duplicated it: `version` is a column, assigned by the append statement (`append.go:123`), and the payload carries only the fact. |
| W7 | Snapshot written **inside** the append transaction (`AggregateStore.java:57`, README §4-4) | → backlog | No snapshot ships in phase 3 — a *measured* deferral with a re-entry trigger (plan §"The snapshot decision, measured first"; ~50 ms p99 replay in the deployment's own environment). Record the constraint so the later phase implements it rather than re-deriving it. |
| W8 | Cadence is `finalVersion % N == 0` with `N >= 2` enforced twice (`EventSourcingProperties.java:19,30-35`) | → backlog | with W7. Note the reference's own consequence: a multi-event command can **jump** a boundary, so worst-case replay length is not bounded by N. |
| W9 | Loading any revision: newest snapshot **at or below** the version — `AND (:version IS NULL OR s.VERSION <= :version) ORDER BY s.VERSION DESC LIMIT 1` (`AggregateRepository.java:75-94`) | → backlog | The Kotlin port **dropped this clause** and pays a full replay for every historical read. For a framework whose product value is revision history that is the wrong trade — the reference's clause is the technique to carry, not the port's simplification. vv has no at-version load at all today (`event/repo.go:28` `Load` folds to head). |
| W10 | Snapshot schema version + **delete-on-drift** (port `V2:27,40`, `AggregateStore.kt:59-78`) | ✔ for events / → backlog for snapshots | vv already versions **every event row**: `revision integer NOT NULL CHECK (revision > 0)` (`schema.go:258, 275`) with a declared upcaster chain (`event/chain.go:52,88`, `ErrUpcast` at `event/errors.go:51`). That is strictly more than the reference (none) and more than the port (one `JSON_VERSION` per row). The *snapshot* half — never upcast, delete on drift, `ON CONFLICT DO UPDATE` rather than `DO NOTHING` — is owed when W7 lands. |
| W11 | Write-side idempotency key claimed in the append transaction (`ES_IDEMPOTENCY_KEY`, port `V4`) | → backlog | Not blocked by [[D-118]] — a dedup claim is not a durable *intent*. vv already has this exact shape one subsystem over: `jobs.EnqueueOnceIn(ctx, queue, stager, def, intent, …)` (`jobs/queue.go:509`) with `ProducerIntent` (`jobs/identity.go:102`). The event write path has no equivalent, so an at-least-once command transport double-applies at versions N and N+1, both legitimate, undetectable. |
| W12 | `EVENT_TYPE` is an opaque `TEXT` resolved through an explicit mapper, never an FQCN (`EventTypeMapper.java`) | ✔ **stronger** | `type text` (`schema.go:257`) + the declaration registry keyed by name **and** revision. A Go type rename does not touch the stored name. |
| W13 | The serializer's settings are part of the on-disk format; `JSON` (byte-faithful) vs the port's `JSONB` (normalised) | ✔ **stronger than both** | `payload bytea` (`schema.go:259`). Byte-faithful by construction — key order, whitespace and duplicate keys survive, and a hash of a stored payload matches a hash of what was written. The port's archive checksum problem (it hashes the `JSONB` rendering, not the original bytes) cannot arise. |
| W14 | Five index writes per append, two of them redundant (`V1:10,16,19-21`) | ✔ **leaner** | Two: PK `(position)` and `UNIQUE (family, key, version)`. The FK's referencing side is served by the unique index's leading columns. The reference's `IDX_ES_EVENT_AGGREGATE_ID` and `IDX_ES_EVENT_VERSION` have no analogue and should not acquire one. **Carry the caution into A1:** `(writer_xid, position)` makes three, and three is the ceiling. |
| W15 | **Nothing** in the write or read path uses time (absence; `Event.java:19` puts `now()` in the payload only) | ✔ **stronger** | `recorded_at` is `statement_timestamp()` (`append.go:123`) — a *database* clock, not an application one, so it is comparable across writers, which the reference's is not. Nothing orders or filters by it: `logStatement` orders by `position`, `streamStatement` by `version`. |
| W16 | Command retry with backoff **outside** the transaction; stale-token vs internal OCC told apart (port `CommandGateway.kt:132-171,181-195`) | ✔ classification / caller's retry | vv separates the two outcomes at the store: `Conflict` on `RowsAffected()==0` and `errs.KindRetryable` for `40001/40P01/55P03` ([[D-126]]). The retry itself is the caller's — [[D-040]] forbids the store one — which is the port's shape without the store owning it. |

### Read / subscription path

| # | Technique | Verdict | vv |
|---|---|---|---|
| R1 | The two-part gap-free read: `(TRANSACTION_ID, ID) > (:lastTx::xid8, :lastId) AND TRANSACTION_ID < pg_snapshot_xmin(pg_current_snapshot()) ORDER BY TRANSACTION_ID, ID` (`EventRepository.java:74-95`) | ✗ **rejected** by [[D-128]] — the ordering decision went the other way | vv reconstructs the *guarantee* with the watermark walk (`event/eventpg/read.go:82-177`), which is **correct** — see §Why vv's walk is correct — but is 46 lines of unrecoverable argument that has already produced two serious defects. See §Adopt A1 and §Blocking. |
| R2 | `CREATE INDEX … ON ES_EVENT (TRANSACTION_ID, ID)` in exactly that column order (`V1:19`) | ✗ **with A1** ([[D-128]]) | Both predicates land in the Index Cond and the `ORDER BY` needs no Sort. `(ID, TRANSACTION_ID)` serves neither. Without it: measured seq scan, 99 989 rows removed per poll on a 100 k log. |
| R3 | No `aggregate_type` on the event row; the type filter is a JOIN to `ES_AGGREGATE` | ✔ **by a better mechanism** | vv's `ReadAll` filters nothing: one global walk, and `projection.Router` (`event/projection/router.go`) fans out in Go. That removes the reference's own worst case — README-adjacent, `EventSubscriptionProcessor.java:41`: a subscription for a **quiet** aggregate type never advances its checkpoint, so it re-scans an ever-growing index suffix once per second forever. vv's checkpoint advances over every event, so no suffix accumulates. Cost: every projection reads every event. Do **not** add a filter parameter to `Log.ReadAll` (`event/store.go:142`) — see §Reject. |
| R4 | `SELECT … FOR UPDATE SKIP LOCKED` on the checkpoint row; zero rows means "someone else has it" (`EventSubscriptionRepository.java:33-44`) | ✔ under `InUnit` / **+ adopt doc+test for `AfterApply`** | See §Adopt A2. Under `InUnit` vv gets the reference's mutual exclusion **and** the guard the reference lacks: the fenced `UPDATE … WHERE projection = $1 AND advance = $3 - 1` (`checkpoints.go:360-363`) takes the same row lock, the second writer blocks, re-evaluates under READ COMMITTED, matches 0 rows and its whole unit — handler writes included — rolls back. Under `AfterApply` there is no transaction to hold a lock across the handler, so the handler's writes are already committed when the conflict is discovered. |
| R5 | `Propagation.MANDATORY` on every repository, so no half of the drain can escape into autocommit | ✔ **stronger** | vv refuses the *wrong* ambient rather than requiring a right one: `errAmbientNotTransaction` (`event/eventpg/executor.go:14, 52-62`) — an ambient executor of this store's data source that is not a transaction is refused at every door before a statement is built ([[D-118]]). And `onExecutor` never touches the pool: `*sql.Tx` or a checked-out `*sql.Conn` only, because `(*sql.DB).ExecContext` retries `driver.ErrBadConn` and executes one append up to three times (`executor.go:74-88`). |
| R6 | The checkpoint `UPDATE` is **unguarded** — the SKIP LOCKED lock is the only thing preventing regression (`EventSubscriptionRepository.java:46-61`; the reference gives no reason) | ✔ **strictly stronger** | `event/eventpg/checkpoints.go:353-364` — `WHERE projection = $1 AND advance = $3 - 1`, with `advance == 1` split into a separate `INSERT … ON CONFLICT DO NOTHING` for the interleaving reason written at `checkpoints.go:339-352`. A repair script, a second implementation or an admin "reset to position" **cannot** silently lower the checkpoint here. |
| R7 | Handle first, advance last, both in one transaction — the deliberate choice of at-least-once (`EventSubscriptionProcessor.java:41-46`) | ✔ | `InUnit` is exactly that (`event/projection/pass.go:129-162`: check, apply, save, all inside `spec.Unit`). `AfterApply` is the honestly weaker mode and is named as such. INV-072 ("delivery is at least once, and no wording says otherwise") is in the coverage matrix with `TestNoDocPromisesExactlyOnceDelivery` and its self-falsifier. |
| R8 | Skip the checkpoint `UPDATE` when the batch is empty — `if (!events.isEmpty())` (`EventSubscriptionProcessor.java:41`) | ✔ | `event/projection/pass.go:64-77`: `read` returns `waitIdle` **before** any save when `!more`; `projection.go:91-93` says so ("an idle projection issues no writes at all"). The reference's cost this avoids — one dead tuple and one real XID per idle poll per subscription, 86 400/day — is avoided identically. |
| R9 | Checkpoint seeded at `('0'::xid8, 0)`; subscription name defaults to the handler's FQCN (`EventSubscriptionRepository.java:23-31`, `AsyncEventHandler.java:14-16`) | ✔ **and the FQCN trap is absent** | Absence is total and means the origin: `Checkpoint.Fresh()` (`event/checkpoint.go:51`), the empty cursor is the origin (`event/eventpg/cursor.go:49-52`), and `Tracker.admit` refuses a half-absent row (`checkpoint.go:163-183`). The name is `Spec.Name`, caller-chosen — so the reference's worst operational trap (renaming or repackaging a handler class silently creates a new subscription that replays the entire history to a live external system) is unreachable. **Owed:** the README §4-8 WARNING itself — a *new* projection reads the whole log — must be in the module page S5 writes. |
| R10 | `REQUIRES_NEW` + a short drain, because `SELECT … FOR UPDATE SKIP LOCKED` assigns a real xid and freezes the reader's own read horizon at its own xid | ✔ **avoided entirely** | vv's read is one statement on a checked-out connection, **outside every unit of work**, and `event/projection/projection.go:80-87` says why in as many words. The reference's self-inflicted stall — one slow handler holds `xmin` down and freezes every other subscription — cannot occur, because no vv projection holds a transaction across a handler call under either mode's *read*. |
| R11 | The subscription's own transaction **is** the long-running transaction README §4-9(3) warns about (`EventSubscriptionProcessor.java:17` + `:42`) | ✔ avoided | as R10. |
| R12 | `@Async` dispatch of the drain onto an 8-thread pool (`EventSubscriptionProcessor.java:26`, `application.yml:3`) | ✗ **reject** | [[D-092]] — a background activity is a supervised `runtime.Runner`, and constructors start nothing. `Projection.Run` is the loop and `scripts/extensions_test.go`'s `startsNothing` arm forbids a `go` statement in any non-test file of `event/projection` (plan §"What this plan delivers"). The Kotlin port dropped `@Async` too, for a related reason (its `catch` around the drain was dead code with it). **What vv must prove instead:** parallelism is one `Runner` per projection name under one `Supervisor`, and one slow handler must not stall another projection — which holds because each `Projection` is its own runner with its own loop. |
| R13 | The **lost wake-up**: a SKIP LOCKED miss in notification-only mode has no retry and no safety-net poll; the two modes are mutually exclusive `@ConditionalOnProperty`s | ✔ **structurally closed** | `Projection.follow` (`event/projection/projection.go:183-192`) selects on `ctx.Done()`, `drain`, **`spec.Wake`** *and* the idle ticker together. A wake signal is a hint layered on top of a poll that always runs — the combination the reference cannot express. Record this as a property, not an accident: `Spec.Wake` must never become a *replacement* for `Spec.Idle`. |
| R14 | NOTIFY is transactional, collapses identical `(channel, payload)` pairs within one transaction, and delivers only at COMMIT — hence the payload is the aggregate **type** (`V2__notify_trigger.sql:1-17`) | → backlog | Phase 3 explicitly does not deliver LISTEN/NOTIFY. The seam is `Spec.Wake`. The five semantics that make it safe — commit-time delivery, per-transaction payload collapse, distinct payloads each delivered, the 8 GB cluster-wide `max_notify_queue_pages` ring that makes **writers** fail when a listener stalls, and the per-row trigger's cost on the hottest write path — must be recorded now so whoever builds it does not re-derive them. |
| R15 | The LISTEN connection is a dedicated, **unpooled**, long-lived `DriverManager` connection with an outer reconnect loop (`PostgresChannelEventSubscriptionProcessor.java:30-40,50,101-107`) | → backlog with R14 | |
| R16 | **Drain every handler once on (re)connect**, before entering the notification loop (`…java:64`; port comment: "so we never miss a NOTIFY that fired before we were listening") | ✔ for free / → backlog with R14 | vv's idle ticker already gives this unconditionally. Recorded so a future `Wake` producer is not written as if it were the delivery mechanism. |
| R17 | Shutdown: `getNotifications(0)` can only be interrupted by **closing its connection**; `cancel(true)` then `conn.close()` then a 5 s latch (`…java:69-74, 126-146`) | → backlog with R14 | vv's `Drain`/`Run` contract already bounds shutdown between passes (`projection.go:144-157`); a LISTEN producer added later inherits this problem and not vv's solution. |
| R18 | The documented, **unimplemented** alternative: `pg_sequence_last_value` + `LOCK … IN SHARE ROW EXCLUSIVE MODE` in its own `REQUIRES_NEW` transaction containing only that command (README §4-7-2) | → backlog, situational | Not rejected on a house rule — rejected on its cost, which the reference states: it blocks **all writes** once per poll. It is the one escape hatch if vv's `xmin` stall proves intolerable in a deployment, because it bounds delivery by the **slowest writer** instead of by the **oldest transaction in the database**. Record all five steps and the two lock modes, because the mode choice is the whole technique. |
| R19 | Per-event dead-lettering with a durable attempt table (port `V9`, `DeadLetterStore.kt`, `EventSubscriptionProcessor.kt:76-122`) — the reference has none and one poison event stalls a subscription **forever** | ✔ in shape / **+ adopt the durable count** | vv has the liveness half and it is good: `oneAtATime` (`pass.go:181-201`) re-delivers the page one envelope at a time, quarantines the permanent failure with its cause, and passes it. That closes the reference's total-stall failure. What vv lacks is the **durable** attempt count — see §Adopt A3. |
| R20 | Per-event checkpoint watermark instead of the reference's whole-batch advance (port `EventSubscriptionProcessor.kt:73-122`) | ✔ different mechanism, same outcome | vv saves once per page after the whole page is applied *or quarantined*, so a mid-page permanent failure does not redeliver the successful prefix — the isolation pass handles it inside the same page. The port's `lastGood` watermark solves the reference's "a batch of 500 that fails on 499 redelivers all 500"; vv's isolation pass solves it too, and additionally records *what* was skipped (`Progress.Quarantined`, `checkpoint.go:24`). |
| R21 | Fail at boot on duplicate subscription names (port `EventHandlers.kt:39-50`) — the reference relies on the FQCN default | ✔ **stronger** | `runtime/supervisor.go:67` — `ErrDuplicateRunner` at supervisor construction, a general rule rather than one subsystem's, over `Projection.Name()` = `"vv.event.projection." + spec.Name` (`projection.go:64`). The port's failure — two handlers sharing one checkpoint row, each permanently skipping what the other advanced past, with no error anywhere — is a boot failure here. |
| R22 | Bounded replay: the same guarded read plus `LIMIT :batchSize`, each page in its own transaction (port `EventRepository.kt:156-181`, `ProjectionReplayer.kt:94-112`) — the reference has **no `LIMIT` anywhere** | ✔ **by construction, on every path** | `logStatement` (`read.go:317-327`) carries `LIMIT` = `Limits().MaxRead` on *every* read, and the loop pages. The reference's §4-8 first poll — the whole table materialised in one transaction pinning global `xmin` — is unreachable. The port applied its fix to *replay* but **not** to `readEventsAfterCheckpoint`; vv has no such asymmetry. |
| R23 | Producer ordering: key by aggregate id **and** `max.in.flight.requests.per.connection: 1` (`OrderIntegrationEventSender.java:43-47`, `application.yml:8-11`) | → backlog, doc | vv has no sink. The transferable lesson is the one that survives Kafka: **a gap-free ordered read plus an unordered sink is an unordered system**, and the setting that saves it appears in the reference's config with no comment. Owed to whoever writes an integration-event sender over `event/projection`. |
| R24 | An integration event is the aggregate re-read **at the event's own version**, never the current state (`OrderIntegrationEventSender.java:31-38`) | → backlog | vv has no at-version load (`event/repo.go:28`). Under an at-least-once, lagging sender, reading head state instead means every replayed event carries today's state — indistinguishable from N copies of the current state. This is what makes W9's `s.VERSION <= :version` clause a **correctness** requirement rather than a performance one. |
| R25 | The integration event carries the aggregate `version` — the consumer's only dedupe and reorder tool (`OrderDto.java:42-43`) | ✔ | `event.Envelope` carries `Stream` and `Version` (`event/store.go:87-111`), dense per stream by `UNIQUE (family,key,version)`. The reference's caution transfers: `RecordedAt` is **not** usable for this, and here it is at least a database clock rather than an application one. |
| R26 | The dual write is **unguarded**: `kafkaTemplate.send` is never awaited, so a broker outage advances the checkpoint and loses the integration event permanently — worse than the duplicates README §4-9 does warn about | ✔ answered, differently | [[D-118]]: a durable write made while the caller's transaction is bound to the store's `crud.Source` is written **inside** it, and that is this framework's outbox — `jobs.Stager` (`jobs/queue.go:36`, `EnqueueIn` at `:483`). **What vv must prove** in place of the reference's mechanism, and record in the module page: an `InUnit` projection whose handler stages a job commits the stage with the advance, so no send can be lost by a checkpoint that moved; and an `AfterApply` projection that stages a job outside a unit has exactly the reference's window and must say so. |
| R27 | The polling interval is the lag floor; `fixedDelay`, not `fixedRate` (`application.yml:20-24`, `ScheduledEventSubscriptionProcessor.java:21-24`) | ✔ | `Spec.Idle` defaults to 1 s (`spec.go:91`), and `follow` waits **after** the pass ends — `fixedDelay` semantics, so a slow pass cannot overlap itself. The ticker is injectable (`runtime.Ticks`), so the schedule is driven in tests rather than slept through. |
| R28 | The acceptance run is **two instances behind a load balancer** (`README §7`: `--scale event-sourcing-app=2`), with a deliberately inexact assertion (`hasSizeGreaterThanOrEqualTo(23)`) | **+ adopt (test)** | See §Adopt A2. S4's list has no concurrent two-instance case; `TestAProjectionResumesThroughASecondValueOverOneBacking` is *sequential* replacement. The reference's point stands verbatim: single-instance testing never exercises the contention branch at all, so an implementation that is wrong at N=2 passes. |
| R29 | Range-partitioning an event store by time is **incompatible** with `UNIQUE (AGGREGATE_ID, VERSION)` — reproduced on 17.9: `unique constraint on partitioned table must include all partitioning columns` | → backlog, warning | vv has exactly that constraint (`events_stream_version_key`). Anyone who later reaches for partitioning must know that widening it to include the partition key **destroys** the write-side dedup guarantee — two rows at one `(family, key, version)` in two months' partitions. The reference does not partition, which on this evidence is the safer position. |
| R30 | Cold-tier archival deletes events with **no** checkpoint or snapshot interlock (port `EventArchiveService.kt:218-232, 266-274`) | → backlog, warning | vv has no archival. When it does, the interlock is against `checkpoints.cursor` for **every** projection name, not against a time cutoff — a stalled, dead-lettered or newly-added projection otherwise has its undelivered events deleted out from under it. |
| R31 | The reference's own latent bug: the snapshot `INSERT` sits inside the per-event append loop but tests the aggregate's **final** version, so a 2+-event command on a snapshot boundary violates `PRIMARY KEY (AGGREGATE_ID, VERSION)` (`AggregateStore.java:53-58, 66`) | → backlog note | Invisible only because every sample command emits exactly one event. Record it beside W7 so vv's snapshot is not written by reading the reference's code. |
| R32 | The port's `METADATA JSONB` — actor, `parent_event_id` causation, `correlation_id`, bitemporal `effective_at`/`recorded_at` | → backlog | `event.Envelope` carries none. Out of phase-3 scope; recorded because the *column* is cheap to add at a schema version and impossible to backfill. |

---

## Adopt

### A1 — store the writing transaction's `xid8` on the event row, and read by the tuple

> **REFUSED by [[D-128]], 2026-09-09.** The ordering contract this depends on was decided the
> other way, and the tuple read without the tuple order buys nothing. The recipe below is kept
> in full because it is correct for the system it came from, because §Documentation obligations
> item 1 survives it unchanged, and because it is what a later phase must read if [[D-118]] is
> ever superseded. Do not implement it under D-128.

*(scope: `eventpg-only` for the SQL and the cursor; **kernel** for the ordering contract — see
§Blocking. Phase: the contract decision is **P3-now**, the implementation is **P4**.)*

**The failure in vv's current code.** `event/eventpg/read.go:82-177` reconstructs gap-free
reading with a three-number cursor `(from, bound, reach)` (`event/eventpg/cursor.go:34-38`) and
a settling rule. It is **correct** (§Why vv's walk is correct), and it is correct only because
five facts compose, none of which is local:

1. the `events_position_needs_xid` statement trigger assigns the writer's xid **before** the
   identity default draws `position` (`schema.go:281, 289-292`);
2. sequence draws are totally ordered, so seeing `reach` proves every position `< reach` was
   drawn;
3. `bound` is minted **after** the fetch that saw `reach`, so every writer holding a position
   `<= reach` has an xid `< bound`;
4. `floor > bound` therefore proves all of them finished (`read.go:232-237`);
5. the mint is skipped when a transaction of this backing is bound (`read.go:149`), because on
   the caller's own transaction the floor never passes an id the transaction holds, and minting
   on a second connection deadlocks a pool at its limit.

Break any one and the walk either skips a committed event or stalls for good. **Both have
already happened under review** — `ReadAll` declaring a gap settled from a floor read in a
separate statement (fixed by step 8's second fetch, `read.go:155-161`), and a walk stalling
permanently on a burnt position (fixed by `reached` taking the query's highest rather than the
delivered highest, `read.go:225-230`). The reference's version has **one** fact:
`TRANSACTION_ID < pg_snapshot_xmin(pg_current_snapshot())` with a tuple resume, index-served,
nothing to settle.

**What to write, verbatim, without simplification.**

Schema version 3 in `event/eventpg`:

```sql
ALTER TABLE <schema>.events ADD COLUMN writer_xid xid8 NOT NULL;   -- see the backfill note
CREATE INDEX events_writer_xid_position ON <schema>.events (writer_xid, position);
```

- The type is **`xid8`, not `xid`**. README:416 gives the reason and it is load-bearing: `xid8`
  increases strictly monotonically and cannot be reused in the lifetime of a cluster, so `<`
  and `>` are total and wraparound-free. 32-bit `xid` has no ordering operators at all.
- The value is `pg_current_xact_id()`, evaluated **in the writing statement**, so it is the
  writer's own id. In the append statement (`append.go:122-126`) it is one more expression in
  the `SELECT` list. It costs nothing there: the transaction already has an xid, because the
  CTE's `INSERT`/`UPDATE` on `streams` ran first.
- The column must be read back as **`writer_xid::text`** and parsed as a `uint64`. `xid8` is
  not a type `database/sql`'s scan contract covers, and the two mainstream drivers hand it back
  differently — vv already does exactly this for the floor (`read.go:188, 196, 316`).
- **Index column order is not negotiable.** `(writer_xid, position)` puts both guards in the
  Index Cond and satisfies the `ORDER BY` with no Sort node. `(position, writer_xid)` serves
  neither. Three indexes on `events` is then the ceiling (W14).

The read:

```sql
SELECT e.position, e.writer_xid::text, e.family, e.key, e.version, e.type, e.revision, e.payload, e.recorded_at
  FROM <schema>.events e
 WHERE (e.writer_xid, e.position) > ($1::xid8, $2::bigint)
   AND e.writer_xid < pg_snapshot_xmin(pg_current_snapshot())
 ORDER BY e.writer_xid ASC, e.position ASC
 LIMIT <MaxRead>
```

- `(a, b) > (c, d)` is a **row comparison**, equivalent to `a > c OR (a = c AND b > d)`
  (README:385) — **not** two ANDed comparisons, which would be wrong and would also not be an
  index start condition.
- The ceiling is **strict `<`**. `<=` admits rows written by the transaction that *is* `xmin`;
  the reader takes what it can see, checkpoints past that xid, and any further event from that
  same transaction is stranded behind the checkpoint for good.
- The ceiling is **per transaction, never per row**: a transaction's whole event set is either
  entirely below `xmin` or entirely excluded, so a batch is never split across the boundary.
- There is **no `ORDER BY position` alone** and no `position >` resume. That is the whole point.

The cursor: mint `vve2` = `log[16] || be64(lastWriterXid) || be64(lastPosition)`, 32 bytes,
`"vve2"` + 43 base64 characters. `walk.possible` (`cursor.go:84-92`) becomes: a cursor carrying
a zero xid carries a zero position; `position <= math.MaxInt64`. Under the existing rule
(`cursor.go:13-18`) a build that ships `vve2` **refuses** `vve1` rather than reading it as
something else.

**The migration obligation the format tag creates, and it is not optional.** `ErrCursor` on a
read is `halt — never restart from the origin` (plan's failure table, line 1391). So a
deployment that upgrades with `vve1` cursors in `checkpoints.cursor` sees **every projection
halt at once**. The schema-3 migration must therefore *rewrite* the stored cursors, which is
possible exactly because the new column exists:

```sql
-- for each checkpoint row: the vve1 'from' position P becomes (writer_xid of the row at P, P)
```

and a `vve1` cursor whose position names no row (the origin, or a burnt position) maps to the
highest `(writer_xid, position)` at or below `P`. Write this down before the code, or the
upgrade is an outage.

**Backfilling `writer_xid NOT NULL` on a non-empty table has no honest answer**, and that must
be said out loud: the writing transaction's id is not recoverable after the fact. The two
options are (a) `DEFAULT '0'::xid8` for pre-existing rows — every historical row sorts first,
which is correct, since they are all long committed — or (b) a two-step migration that adds it
nullable, backfills `'0'`, then sets `NOT NULL`. Option (a) is the one that keeps the schema a
single transactional DDL list (`migration.go:419-420`).

**Once the tuple read is in, the `events_position_needs_xid` trigger is removable.** Its only
job is fact (1) above, which the tuple read does not need: within one transaction, positions are
drawn in insert order, and across transactions the xid decides. Removing it is a fingerprint
change and therefore part of the same schema version. Do **not** remove it earlier, and do
**not** assume a `DEFAULT pg_current_xact_id()` on the new column substitutes for it while the
watermark is still live — the evaluation order of two column defaults (an identity and an
expression) on one row is not specified, and the watermark's correctness rests on that order.

**The operational cost ships with it, in the module page, or it is not adopted.** See
§Documentation obligations, item 1.

---

### A2 — prove and document what two live instances of one projection name do

*(scope: `kernel-and-eventpg` — one live test in `event/eventpg`, one row in two module pages,
possibly one arm in `pass.go`. Phase: **P3-now**, in S4 and S5.)*

> **ADOPTED, 2026-09-09 — the arm by [[D-133]], the test in S3's review.** The `Conflict` arm was
> decided **reload-and-retry**, not halting: a rolling deploy runs two instances of a singleton on
> purpose and halting kills one of them permanently. The live case is
> `TestTwoLiveInstancesOfOneNameOverOneSchema` (`event/eventpg/projection_integration_test.go`),
> driven at N=2 in both modes with a gate holding each instance's first save until both have
> issued one, and it **corrects one sentence below**: at advance 1 there is no row to lock, so what
> refuses the `InUnit` loser is PostgreSQL's speculative insertion on the store's
> `INSERT … ON CONFLICT DO NOTHING`, not the fenced `UPDATE`'s row lock. Measured: `InUnit` leaves
> the contended page in one row and calls the handlers exactly once per event; `AfterApply` leaves
> it in two. The owed module-page row is rewritten in the backlog — the loser **takes its turn**.

**The failure in vv's current code.** `event/projection/pass.go:256-261`: a `Conflict` from
`Tracker.Save` is not `ErrUncertain`, so it goes to `refused(err, "the checkpoint store")`
(`pass.go:295-304`), which is not `stopping` and not `ErrBackend`, so it **halts permanently**.
The plan decides this (`| ErrConflict | save | halt — another writer owns this name, or it was
forgotten |`, plan line 1391). Two things are wrong with leaving it there unexamined:

1. **Under `AfterApply` the handler's writes are already committed when the conflict is
   discovered.** Interleaving: instances P and Q of projection `orders`, both at advance 7.
   Both `Load` (advance 7), both `Read` the same page, both call `Handler.Apply` — **the read
   model is written twice** — then P's `UPDATE … WHERE advance = 7` matches and commits, Q's
   matches nothing and Q halts. The reference prevents the double-apply outright: its
   `SELECT … FOR UPDATE SKIP LOCKED` runs **before** the read, so Q never reads and never
   handles. vv cannot take that lock under `AfterApply` — there is no transaction to hold it
   across the handler call, and opening one is forbidden ([[D-126]]: none of the eight methods
   begins a transaction). At-least-once already obliges the handler to tolerate a duplicate, so
   this is not a correctness bug — **it is an undocumented one**, and the module page's delivery
   row is where it belongs.
2. **`event/checkpoint.go:190-196` was written for the opposite behaviour.** `Tracker.established`
   explicitly admits an advance *above* the fence and says: *"Above the fence is a second writer
   at the same name, and taking its advance is what makes two processes take turns over one
   checkpoint, each re-reading the other's number and saving one above it."* The kernel supports
   take-turns; the loop halts. That is an internal inconsistency, and the backlog already records
   the hole from the other side (`## P3` §6 and §27: "what a store failure on `Save` retries — the
   save or the whole pass — is unstated").

**Under `InUnit` vv already has the reference's mechanism and the guard the reference lacks**,
and this is worth stating because it is the answer to the whole family: the fenced `UPDATE`
takes the checkpoint row's lock; a second writer blocks on it; under READ COMMITTED it
re-evaluates `advance = $3 - 1` against the committed tuple, matches 0 rows, and its **whole
unit rolls back — the handler's writes with it**. That is `FOR UPDATE SKIP LOCKED` plus the
guard the reference's unguarded `UPDATE` never had, obtained optimistically.

**What to adopt.**

- **A live test**, in `event/eventpg/projection_integration_test.go` alongside S4's list —
  the reference's `--scale event-sourcing-app=2`, which is the only reason its contention
  branch is ever executed. Two `Projection` values of one name, over one schema, run
  concurrently against one log, in **both** modes. Assert what actually happens, out of the
  database: under `InUnit` the read model holds each event's effect exactly once and the loser
  halts; under `AfterApply` the overlapping page's effect is present **twice** (or the handler
  is idempotent and it is present once — say which, in the test) and the loser halts.
  **Control:** one instance alone must reach `PhaseFollowing` and never halt, so a test that
  proves nothing is visible.
- **A row in both module pages** (S5), in these words or better: *under `AfterApply`, two live
  instances of one projection name both apply the overlapping page before the fence fires; the
  handler must be idempotent, the loser halts and does not resume, and `Placement: Singleton`
  is a promise to the deployment rather than an enforcement.*
- **Decide, and record, whether the `Conflict` arm should halt or reload-and-retry.** Halting is
  defensible (it is loud, and a second writer at one name is a deployment error). Reload-and-
  retry is what `Tracker.established` was built for and what the reference does. Do not leave the
  two in the tree disagreeing.

---

### A3 — the redelivery count that survives a process restart

*(scope: `eventpg-only` (a column) or `kernel-and-eventpg` (a `Checkpoints` field). Phase:
**P4**.)*

**The failure in vv's current code.** `Projection.attempt` is an in-memory `int`
(`projection.go:29`), reset to 1 at every `read` (`pass.go:74`) and therefore at every process
start. `permanent()` (`pass.go:246-254`) reaches its cap through `this.attempt >= this.spec.Attempts`
— default 10 (`spec.go:94`). So: an envelope whose application **kills the process** — an
allocation the handler cannot make, a `SIGKILL` from an OOM killer, a supervisor taking the
process down because an unrelated runner returned — is retried at attempt 1 forever. The cap is
never reached, `OnPermanentFailure` never fires, the quarantine sink is never called, and the
projection is in a restart loop whose only symptom is the process dying. This is precisely the
failure the Kotlin port added `ES_EVENT_SUBSCRIPTION_ATTEMPT` for, and the port's migration
comment says so in as many words.

**Plan decision D3 does not cover this case.** D3's argument is: *"a handler that reads 'this
page has now been delivered 14 times across 3 processes' can do nothing with it that
`(Stream, Version)` idempotency does not already do"*, and *"a durable count would have to be
written on the path whose entire property is that it wrote nothing — the failing pass"*. The
first half answers a question about the **handler**; the port's counter is for the **engine's
cap**. The second half is a real objection and the port's own answer is a real hole worth
recording: the port writes the attempt row in the **same transaction as the failing handler**,
so a handler that failed by aborting the PostgreSQL transaction (a constraint violation, a
statement error) cannot record its attempt — there is no savepoint around `handleEvent` — and
that event retries forever without ever dead-lettering. **Both implementations therefore have an
infinite-retry hole; they are different holes.**

**What to adopt, if it is adopted.** The port's exact shape, because the shape is the
transferable part:

```sql
CREATE TABLE ES_EVENT_SUBSCRIPTION_ATTEMPT (
  SUBSCRIPTION_NAME  TEXT NOT NULL,
  EVENT_ID           BIGINT NOT NULL,
  ATTEMPT_COUNT      INT NOT NULL,
  LAST_ERROR         TEXT,
  LAST_ATTEMPT_AT    TIMESTAMPTZ NOT NULL,
  DEAD_LETTERED_AT   TIMESTAMPTZ,
  PRIMARY KEY (SUBSCRIPTION_NAME, EVENT_ID)
);
CREATE INDEX … ON ES_EVENT_SUBSCRIPTION_ATTEMPT (SUBSCRIPTION_NAME, DEAD_LETTERED_AT)
  WHERE DEAD_LETTERED_AT IS NOT NULL;
```

with `INSERT … ON CONFLICT (SUBSCRIPTION_NAME, EVENT_ID) DO UPDATE SET ATTEMPT_COUNT =
ES_EVENT_SUBSCRIPTION_ATTEMPT.ATTEMPT_COUNT + 1, LAST_ERROR = :err, LAST_ATTEMPT_AT = :now`,
a **success deletes the row** so a later transient failure starts at attempt 1 again, and the
partial index makes "what did we give up on" cheap. The migration comment also names the
operator's "currently stuck" query, which is worth carrying: `dead_lettered_at IS NULL AND
last_attempt_at < now() - interval '5m'`.

**The lighter option, which fits vv better.** vv already persists two cumulative counters on the
checkpoint row (`applied`, `quarantined`, `event/eventpg/schema.go:318-320`) and already saves
per page rather than per event. A third column — the attempt count for *the page the cursor is
about to deliver* — would be written on the **succeeding** save only, which sidesteps D3's real
objection entirely: it is not a write on the path that writes nothing. It does not survive a
crash mid-page either, so it does not close the whole hole. **Decide which hole is worth closing
and say so; do not leave D3 as the answer to a question it does not ask.**

**Note the trade the port makes and vv already makes:** advancing past a poison event breaks
per-aggregate completeness. A consumer sees version 3 then version 5 and the version field alone
cannot detect the gap. vv records it — `Progress.Quarantined`, and `checkpoint.go:22-24` says
*"Non-zero means that destination has holes, which is what keeps `Highest` from reading as a
completeness claim."* That is better than either implementation and should not be lost.

---

## Reject

Three, each on a named rule, each with what vv must prove in its place.

1. **`@Async` drain dispatch onto a shared thread pool** (R12) — [[D-092]]: a background
   activity is a supervised `runtime.Runner` and constructors start nothing; and
   `scripts/extensions_test.go`'s `startsNothing` arm forbids a `go` statement in any non-test
   file of `event/projection`. *What vv must prove instead:* one `Runner` per projection name,
   so one slow handler delays only its own projection — which the reference achieves with a pool
   and the Kotlin port **lost** when it dropped `@Async` (its single LISTEN thread now runs every
   handler's full batch before it can poll again).

2. **A second durable-intent table for the outbox** (R26, README §4-7) — [[D-118]]: a durable
   write made while the caller's transaction is bound to the store's own `crud.Source` is
   written inside it, and that is this framework's outbox; `jobs.Stager` (`jobs/queue.go:36`)
   already stages inside the caller's transaction. *What vv must still answer, and record:* the
   reference's ordering guarantee (events of one aggregate reach the sink in version order),
   its retry, and its dead-letter behaviour are questions `jobs` must answer for a staged
   integration event, not questions that disappear. And the reference's unguarded dual write —
   `kafkaTemplate.send` never awaited, so a broker outage advances the checkpoint and loses the
   event permanently — is exactly the failure `jobs.EnqueueIn` inside an `InUnit` pass cannot
   have, and exactly the failure an `AfterApply` pass **can**. That asymmetry belongs on the
   module page.

3. **A filter parameter on `Log.ReadAll`** (R3) — not a house-rule reject but a
   contract-freeze one, and the reference supplies the evidence *against* itself: filtering by
   `a.AGGREGATE_TYPE` means a subscription whose type has gone quiet never advances its
   checkpoint (`EventSubscriptionProcessor.java:41` writes nothing on an empty batch) and
   therefore re-scans an ever-growing index suffix once per second forever. vv's unfiltered
   walk plus `projection.Router` has no such suffix, because every projection's cursor advances
   over every event. *What vv must accept:* every projection reads every event, so N
   projections cost N × the read traffic. If that ever becomes the binding cost, the answer is
   a shared reader fanned out to N routers — not a filter on the store contract.

Two more things the reference does that are **deliberately not adopted**, recorded so they are
not re-proposed:

- **`SELECT … FOR UPDATE` before the write** — [[D-126]] forbids it by name, with two reasons:
  it reads the version into Go and decides there, which is correct only against a snapshot
  nobody else can move; and the whole of `eventpg`'s atomicity is that an append is **one**
  statement, which needs no transaction the store is forbidden to open.
- **Choosing an isolation level** — [[D-126]] again. The reference depends on READ COMMITTED
  and never says so; vv is correct at all three and documents what differs.

---

## Documentation obligations

An adopted mechanism whose stall mode is not on the module page has not been fully adopted.
These are owed by S5, which writes the pages.

1. **The global `xmin` stall, which vv has *today* and does not document.** `logStatement`
   (`event/eventpg/read.go:319`) reads `pg_snapshot_xmin(pg_current_snapshot())` and
   `walk.settledAt` (`read.go:232-237`) settles a gap only when that floor passes the minted
   bound. `pg_snapshot_xmin` is the **oldest running transaction id in the whole database**, not
   in the event tables — so **any** session anywhere that has written something and then gone
   idle-in-transaction holds the floor down and the walk **cannot pass a burnt gap** until that
   session ends. Events are never lost; delivery freezes. `docs/modules/en/eventpg.md:141-179`
   documents the two *local* stalls (a live writer holding the gap, a walk inside a caller's
   transaction) and **not this one** — `grep -n 'long-running\|idle_in_transaction' docs/` returns
   nothing. Owed, with the mitigations the Kotlin port prescribes and the reference does not
   (`EventRepository.kt:101-108`): `idle_in_transaction_session_timeout` and `statement_timeout`
   on the application role, and a monitored subscription-lag metric. This obligation **survives**
   A1 unchanged — the tuple read has the same dependency, more strongly (it defers *all*
   delivery, not just gap settlement), which is README §4-9 drawback 3.
2. **A new projection reads the whole log** (R9). The reference states it in a WARNING block
   (README §4-8). vv's is bounded per page by `MaxRead` and resumable, which is better, but the
   consequence is the same: adding a projection to a live deployment replays every event ever
   written through its handler. Say so beside `Checkpoint.Fresh()`.
3. **The `AfterApply` two-instance double-apply** (A2).
4. **What it costs in transaction ids.** `docs/modules/en/eventpg.md:180-186` already covers the
   trigger and the mint; if A1 lands, that whole section changes — the mint disappears and the
   trigger becomes removable.
5. **A `Wake` signal is a hint and never a delivery mechanism** (R13/R16). The reference proves
   it does not depend on NOTIFY by shipping polling as an equivalent alternative; vv should say
   the same about `Spec.Wake` before anyone writes a producer for it.

---

## Why vv's walk is correct

Recorded because the adjudication above rests on it, and because "the reference does it
differently" is not by itself evidence that vv is wrong.

`event/eventpg/read.go` delivers a page while positions are contiguous with the cursor, **or**
the whole gap before them lies at or below what is settled (`deliverable`, `read.go:214-223`).
A gap is settled when `floor > bound` (`settledAt`, `read.go:232-237`). The claim `bound` makes
is: *every position at or below `reach` was drawn before `bound` was minted*. It holds because

- sequence draws are totally ordered, so a snapshot that returned a row at `reach` proves every
  position `< reach` was already drawn;
- the `events_position_needs_xid` **statement-level** trigger runs `PERFORM pg_current_xact_id()`
  before the identity default is evaluated, so a writer's xid is assigned **before** its
  position is drawn;
- therefore any writer holding a position `<= reach` has an xid `<` the `bound` minted after
  that fetch, and `floor > bound` proves every such xid has finished.

The two known holes are documented: a writer that draws a position with `nextval` in one
transaction and inserts it later with `OVERRIDING SYSTEM VALUE` is skipped
(`docs/modules/en/eventpg.md:157-165`), and a walk on a bound transaction never settles
(`read.go:117-121`). A transaction's own events are never split across the boundary, because a
batch is one statement and two batches in one transaction become visible together.

So the case for A1 is **not** that vv is broken. It is that vv's guarantee is a five-fact chain
that a refactor keeping the code correct can still break — and has, twice — where the
reference's is one tuple comparison and one index.

---

## Backlog

Every `→` verdict above is appended to
[`EVENTSOURCE_BACKLOG.md`](EVENTSOURCE_BACKLOG.md) under `## From the reference`, with the
reference site, the failure, and the mechanism recorded in full rather than at a level of
abstraction where it would have to be re-derived.
