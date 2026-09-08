# EVENTSOURCE phase 2 — live gate

Run on 2026-09-08 against PostgreSQL 17.9 in Docker (`vv-postgres-1`,
`postgres://vv:vv@localhost:55432/vv?sslmode=disable`). Working tree
`dev/ai-improvements`, HEAD `2199910`, phase-1 kernel baseline `c798fc0`.

    $ docker exec vv-postgres-1 psql -U vv -d vv -c "select version();"
                                             version
    -----------------------------------------------------------------------------------------
     PostgreSQL 17.9 on x86_64-pc-linux-musl, compiled by gcc (Alpine 15.2.0) 15.2.0, 64-bit

**Verdict: green.** Nothing blocking. No `[critical]` and no `[high]` survived.
Every section below is a command that was run and the output it produced.

---

## 1. The conformance suite against live PostgreSQL

`event/eventtest` is byte-identical to what phase 1 shipped:

    $ git status --porcelain event/eventtest/
    $ git diff --stat c798fc0b28b270ec0368a918810ff3d7c17e6f8a -- event/eventtest/
    (both empty)

    $ FROSTGROVE_EVENTPG_TEST_DSN='postgres://vv:vv@localhost:55432/vv?sslmode=disable' \
        go test -race -count=1 -tags=integration -run 'TestTheStoreSatisfiesTheContract' ./event/eventpg/...
    ok  	github.com/frostgrove/vv/event/eventpg	4.829s

Verbose, both configurations, twenty sections each, 42 subtests, 0 failures,
0 skips:

    --- PASS: TestTheStoreSatisfiesTheContract (1.91s)
        --- PASS: TestTheStoreSatisfiesTheContract/binding (0.05s)
        --- PASS: TestTheStoreSatisfiesTheContract/stream_identity (0.04s)
        --- PASS: TestTheStoreSatisfiesTheContract/expected_version (0.04s)
        --- PASS: TestTheStoreSatisfiesTheContract/dense_versions (0.04s)
        --- PASS: TestTheStoreSatisfiesTheContract/global_order (0.04s)
        --- PASS: TestTheStoreSatisfiesTheContract/conservation (0.04s)
        --- PASS: TestTheStoreSatisfiesTheContract/stream_paging (0.04s)
        --- PASS: TestTheStoreSatisfiesTheContract/global_paging (0.04s)
        --- PASS: TestTheStoreSatisfiesTheContract/resumption (0.10s)
        --- PASS: TestTheStoreSatisfiesTheContract/bounds (0.04s)
        --- PASS: TestTheStoreSatisfiesTheContract/payload_ownership (0.07s)
        --- PASS: TestTheStoreSatisfiesTheContract/refusal_classes (0.04s)
        --- PASS: TestTheStoreSatisfiesTheContract/cancellation (0.04s)
        --- PASS: TestTheStoreSatisfiesTheContract/lifecycle (0.06s)
        --- PASS: TestTheStoreSatisfiesTheContract/concurrency (0.08s)
        --- PASS: TestTheStoreSatisfiesTheContract/transactions (0.20s)
        --- PASS: TestTheStoreSatisfiesTheContract/durability (0.04s)
        --- PASS: TestTheStoreSatisfiesTheContract/shared_backing (0.04s)
        --- PASS: TestTheStoreSatisfiesTheContract/monotone_visibility (0.00s)
        --- PASS: TestTheStoreSatisfiesTheContract/store_failure_classification (0.80s)

    (TestTheStoreSatisfiesTheContractAtNarrowerLimits, same twenty sections, all PASS)

One section is reported not certified rather than passed:

    eventtest: monotone visibility: not certified — this store does not promise monotone visibility

That is an honest declaration, not a downgrade: `Store.Capabilities()` answers
`MonotoneVisibility: event.Unsupported` (`event/eventpg/config.go:189`), because
positions come from an identity sequence and a lower position can become visible
after a higher one. `eventtest` reports a section it cannot certify with `t.Log`
only, so `go test` would print `ok` for a run whose sections all quietly
downgraded — and `event/eventpg/census_integration_test.go` is what closes that:
`census()` writes the nineteen certified sections and the one declined claim
down, `TestBothConformanceRunsCertifyEverySectionButTheOneThisStoreDeclines`
drives both runs as subprocesses and reads their verdict lines, and
`TestADowngradedSectionIsCaughtByTheCensusAndByNothingElse` withdraws three
Factory hooks and requires each to leave `go test` green and the census red.

### The suite is not vacuous — proven by breaking the store

`event/eventpg/append.go` was temporarily edited so the CTE's conditional
`WHERE s.version = $3::bigint` became `WHERE TRUE OR s.version = $3::bigint`,
i.e. the optimistic-concurrency predicate was removed. Six sections went red:

    --- FAIL: TestTheStoreSatisfiesTheContract (1.28s)
        --- FAIL: .../expected_version — a second append at the version a fresh stream was
            loaded at answered event: the store failed
        --- FAIL: .../dense_versions — an append at a version the stream has left answered
            event: the store failed
        --- FAIL: .../refusal_classes — ... answered event: the store failed where the
            contract answers event: the stream is not at the version this append was
            decided at: conflict
        --- FAIL: .../concurrency — one of 8 writers that all loaded version 0 answered
            event: the store failed, which is neither a win nor a conflict
        --- FAIL: .../transactions — a transaction that had loaded the stream at the version
            another has since committed over answered event: the store failed
        --- FAIL: .../store_failure_classification
        conformance_integration_test.go:333: the schema a conformance store wrote to is
            inconsistent: a stream row names a version its own events do not reach

The edit was reverted. `event/eventpg` hashes identically before and after:
`sha256 d34151e96cea0ce76cf194e1a687a6db3529ef3a981855280b33ec51fdc71e17`.

---

## 2. The integration suite

    $ FROSTGROVE_EVENTPG_TEST_DSN='postgres://vv:vv@localhost:55432/vv?sslmode=disable' \
        go test -race -count=1 -tags=integration ./event/eventpg/...
    ok  	github.com/frostgrove/vv/event/eventpg	78.886s

Run a second time consecutively, per the repository rule:

    ok  	github.com/frostgrove/vv/event/eventpg	79.029s

Verbose accounting: **60 top-level tests, 316 subtests, 0 FAIL, 0 SKIP.**

The repository's own suite is unaffected:

    $ make integration
    ok  	github.com/frostgrove/vv/test/integration	19.478s
    ... (all green, exit 0)

---

## 3. The unset-DSN behaviour — it fails, it does not skip

    $ env -u FROSTGROVE_EVENTPG_TEST_DSN go test -race -count=1 -tags=integration ./event/eventpg/...
    FROSTGROVE_EVENTPG_TEST_DSN is not set, and this suite proves nothing without a database,
    so it fails rather than skipping. Run:

      FROSTGROVE_EVENTPG_TEST_DSN='postgres://vv:vv@localhost:55432/vv?sslmode=disable' go test -race -count=1 -tags=integration ./event/eventpg/...

    FAIL	github.com/frostgrove/vv/event/eventpg	0.004s
    FAIL
    EXIT=1

The empty-string case behaves the same way. `TestMain`
(`event/eventpg/main_integration_test.go:23`) checks the variable's presence and
nothing else, so a value that is present and wrong fails the test that needed a
database rather than this gate. The suite also carries its own
`TestTheGateFailsWhenTheDSNIsUnset`.

---

## 4. Concurrency, driven independently

Driven from a program outside the repository (`/tmp/eventgate`, its own module
with `replace` directives onto this tree) so nothing in the module's own test
helpers is trusted. Two `*sql.DB` pools of one connection each, two
`*eventpg.Store` values over one schema, two `event.Repo` values; both load the
same version, both append behind a barrier.

    === 1. two concurrent writers on one stream ===
    PASS  exactly one winner per round
            25 rounds driven through two pools, 25 appends admitted
    PASS  every loser is a typed conflict and nothing else
            25 losers matched event.ErrConflict, 0 errors of any other kind []
    PASS  the conflict is the kernel's own class, not a driver error
            a loser reads "event: the stream is not at the version this append was decided
            at: conflict"; errors.Is event.ErrConflict=true crud.ErrConflict=true
            context.Canceled=false
    PASS  no loser was a serialisation failure surfacing unclassified
            unclassified or backend errors seen: []
    PASS  the rows in the database are dense, unique and complete
            25 event rows, 25 distinct versions, 1..25, streams.version=25, 25 expected
    PASS  a batched race admits one batch per round and refuses the other whole
            15 rounds of 4 changes: 15 admitted, 15 conflicts, 0 other
    PASS  the rows in the database are dense, unique and complete
            60 event rows, 60 distinct versions, 1..60, streams.version=60, 60 expected
    PASS  no admitted batch is a fragment of two writers
            0 version window(s) hold rows from more than one writer

The loser's text carries no SQLSTATE and no driver name. Nothing was a `40001`
serialisation failure reaching the caller unclassified.

### The rows, read with psql rather than through Go

    $ docker exec vv-postgres-1 psql -U vv -d vv -c "..."

        family    |      key      | rows | distinct_versions | lo | hi | holes
    --------------+---------------+------+-------------------+----+----+-------
     gate.account | racer-1       |   25 |                25 |  1 | 25 |     0
     gate.account | racer-batched |   60 |                60 |  1 | 60 |     0

        family    |      key      | version
    --------------+---------------+---------
     gate.account | racer-1       |      25
     gate.account | racer-batched |      60

    -- duplicates over (family, key, version)
     family | key | version | count
    --------+-----+---------+-------
    (0 rows)

    -- gaps in the identity sequence, i.e. positions a loser burnt
     position_holes
    ----------------
                  0

    -- which writer won, so the race is not a scheduling artefact
     writer | count
    --------+-------
     left   |     8
     right  |    17

No fragment, versions dense from 1 with no hole, no duplicate, the stream row
exactly at the count, and not one position consumed by a loser — a lost race
writes zero rows out of the CTE rather than drawing a sequence value and rolling
it back.

### History is append-only against a psql session too

    $ docker exec vv-postgres-1 psql -U vv -d vv -c "UPDATE gate_live.events SET payload = 'tampered' WHERE version = 1;"
    ERROR:  eventpg: UPDATE on an event row: history is append-only
    $ ... -c "DELETE FROM gate_live.events WHERE version = 1;"
    ERROR:  eventpg: DELETE on an event row: history is append-only
    $ ... -c "TRUNCATE gate_live.events;"
    ERROR:  eventpg: TRUNCATE on an event row: history is append-only

### The independent gate is not vacuous either

With the same `WHERE TRUE OR ...` break in `append.go`, the concurrency gate
went red and named the shape of the defect:

    FAIL  every loser is a typed conflict and nothing else
            0 losers matched event.ErrConflict, 25 errors of any other kind
            [round 0: *event.refusal event: the store failed ...]
    FAIL  a batched race admits one batch per round and refuses the other whole
            15 rounds of 4 changes: 15 admitted, 0 conflicts, 15 other

Data integrity still held under the break — the `events_stream_version_key`
unique constraint refused the second writer — which is the schema doing the job
the doc claims for it. What broke was the *classification*: a conflict surfaced
as `event: the store failed`.

---

## 5. Rollback and cancellation, verified in the database

    === 2. rollback and cancellation ===
    PASS  the uncommitted append is inside the transaction and invisible to everybody else
            inside the transaction 1 row(s), on a third connection 0
    PASS  a rolled-back append leaves no event row
            SELECT count(*) on a third connection after ROLLBACK is 0
    PASS  a rolled-back append advances no stream version
            the streams row after ROLLBACK is -1 (-1 is no row at all)
    PASS  an append on a cancelled context is refused as a cancellation and writes nothing
            Append answered "context canceled" (context.Canceled=true) and the database
            holds 0 row(s)

The counts after `ROLLBACK` were read on a third `*sql.DB` connection, and the
final psql dump above lists no `gate.rollback` row in either `events` or
`streams` at all. A cancellation reaches the caller as the bare
`context.Canceled`, not wrapped into a store failure.

### D-118, checked independently

    New with Source over another *sql.DB: eventpg: this store cannot be assembled from this
      spec: Spec.Source reads another data source than Spec.DB, and atomic across two
      handles is a word with no meaning (ErrSpec=true)
    New with Source over its own *sql.DB: <nil>
    Transaction() over an ambient non-transaction: eventpg: this context carries an executor
      of this store's data source that is not a transaction, and an operation through it
      would write beside the caller's own work rather than inside it
    Append over an ambient non-transaction: event: the store reported [outcome refused]
    rows written on autocommit by that refused append: 0

---

## 6. Schema verification fails closed before an append

Each case deploys a schema, mints a real token against it and writes one event,
then breaks the schema underneath and asks a store that describes what this
build expects. Every one refuses `Prepare` with `ErrSchemaMismatch`, refuses the
read and the append with `event.ErrRefused`, and leaves the baseline row
untouched.

    === 3. a wrong schema fails closed before an append ===
    PASS  the schema is not deployed at all
            Prepare answered "eventpg: the deployed schema is not the one this build expects:
            gate_absent.schema_meta is not there, so nothing this store expects was ever
            deployed here: ERROR: relation \"gate_absent.schema_meta\" does not exist
            (SQLSTATE 42P01)"
    PASS  the deployed schema records another version than this build
            "... \"gate_version\" is schema version 2 and this build is version 1"
    PASS  the schema was migrated at other bounds than this build describes
            "... \"gate_bounds\" was migrated at sha256:c0d87e9b... and this build describes
            sha256:41af0c28..."
    PASS  a column's type was changed
            "... gate_column.events.revision is bigint and this build expects integer"
    PASS  the append-only trigger was dropped
            "... gate_trigger.events.events_append_only_row is missing"
    PASS  a CHECK constraint was dropped
            "... gate_check.events.events_payload_check is missing"
    PASS  the unique constraint over (family, key, version) was dropped
            "... gate_unique.events.events_stream_version_key is missing"

In each case:

    a read answered "event: the store refused this operation as a matter of its own policy" (ErrRefused=true)
    an append answered "event: the store refused this operation as a matter of its own policy" (ErrRefused=true)
    1 event row(s) where the baseline left 1

The refusal names the schema, the table and the column, constraint or trigger
that is wrong, or the two fingerprints side by side.

---

## 7. The zero-diff obligation

    $ git status --porcelain event/
    ?? event/eventpg/

    $ git status --porcelain -- event/ ':(exclude)event/eventpg'
    (empty)

    $ git diff --stat c798fc0b28b270ec0368a918810ff3d7c17e6f8a -- event/ ':(exclude)event/eventpg'
    (empty)

The obligation is executable and in `make check` as `check-event-kernel`
(`scripts/checks.sh:333`, baseline pinned at `EVENT_KERNEL_BASELINE=c798fc0b...`).
It was proven able to fail — a single newline appended to `event/store.go`:

    event/ outside event/eventpg has moved since c798fc0b28b270ec0368a918810ff3d7c17e6f8a:
       event/store.go | 1 +
       1 file changed, 1 insertion(+)
       M event/store.go
      a second store is written with zero diffs to the vocabulary. Either the
      constructor you need already exists and was not found, or this is a real
      kernel gap — which is reported out loud rather than patched here.
    EXIT=1

restored, then:

    check-event-kernel: ok

---

## 8. The structural and unit gates

    $ make check
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
    EXIT=0

`check-deps` reports `./event/eventpg: 0 external packages` — pgx is a fixture
dependency only, and the production code stays on `database/sql` + `crudsql`.

    $ make unit
    ==> ./event/eventpg
    ok  	github.com/frostgrove/vv/event/eventpg	(cached)
    EXIT=0

    $ go test -race -count=1 ./event/...
    ok  	github.com/frostgrove/vv/event	5.940s
    ok  	github.com/frostgrove/vv/event/eventmemory	1.249s
    ok  	github.com/frostgrove/vv/event/eventtest	2.519s

    $ go -C event/eventpg test -race -count=1 ./...
    ok  	github.com/frostgrove/vv/event/eventpg	36.169s

    $ make vet
    EXIT=0

    $ gofmt -l .
    (no output)

    $ cp docs/api/surface.md /tmp/surface_before.md && make api && diff /tmp/surface_before.md docs/api/surface.md
    diff lines: 0

The module's registration is complete: `go.work` carries `./event/eventpg`,
`docs/modules/en/eventpg.md` and `docs/modules/ru/eventpg.md` exist with their
index rows, `docs/ai/flows/FL-037-...` maps all eleven production files and is in
the flow index and reverse index, and `D-126` and `D-127` are in the decision
index.

---

## Findings

No `[critical]` and no `[high]`. Nothing was appended to
`EVENTSOURCE_BACKLOG.md` from this gate.

One observation worth stating rather than filing, because it is a deliberate,
declared property and not a defect: **`eventpg` does not certify the monotone
visibility section**, and a reader of `go test` output alone would never learn
that a section was skipped. The census harness is what makes it visible, and it
is a per-store file — a third store added later without a census of its own can
downgrade sections and print `ok`. That mechanism lives in `eventpg` rather than
in `eventtest`, and phase 1's kernel is frozen, so nothing was changed here.
