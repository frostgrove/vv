//go:build integration

package eventpg

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/frostgrove/vv/event"
)

// What a two-statement append cannot hold under load, asked of the rows rather
// than of the code: a stream row and its events agree about the version, no
// event row is outside a stream, and inside a stream the positions follow the
// versions. The same three questions are asked of every schema a conformance
// store wrote to, through the factory's own cleanup.
type audited struct {
	streams  int
	events   int
	findings []string
}

func TestTheAuditOverTheWholeSchemaHolds(t *testing.T) {
	t.Run("a schema many writers raced into holds all three", func(t *testing.T) {
		schema := deployed(t, "eventpg_audit_load")
		store := prepared(t, schema)
		streams, events := raceInto(t, store, "eventpg.audit.load")

		held := auditOf(t, schema.Name)
		if len(held.findings) != 0 {
			t.Fatalf("a schema this test wrote through the store alone is inconsistent: %v", held.findings)
		}
		if held.streams != streams || held.events != events {
			t.Fatalf("the audit read %d streams and %d events where this test wrote %d and %d, so the three questions above were asked of the wrong rows",
				held.streams, held.events, streams, events)
		}
	})

	t.Run("a stream whose version outran its events is reported", func(t *testing.T) {
		schema := deployed(t, "eventpg_audit_version")
		store := prepared(t, schema)
		raceInto(t, store, "eventpg.audit.version")
		if held := auditOf(t, schema.Name); len(held.findings) != 0 {
			t.Fatalf("the schema is inconsistent before this case broke anything: %v", held.findings)
		}

		mustExecute(t, "UPDATE "+quoteIdentifier(schema.Name)+".streams SET version = version + 1")
		findings := auditOf(t, schema.Name).findings
		if len(findings) == 0 {
			t.Fatal("every stream row names a version its events do not reach and the audit found nothing, so a store that advanced the version without writing the rows would pass it")
		}
	})

	t.Run("an event row outside every stream is reported", func(t *testing.T) {
		schema := deployed(t, "eventpg_audit_orphan")
		store := prepared(t, schema)
		raceInto(t, store, "eventpg.audit.orphan")

		mustExecute(t, "ALTER TABLE "+quoteIdentifier(schema.Name)+".events DROP CONSTRAINT events_stream_fkey")
		mustExecute(t, "DELETE FROM "+quoteIdentifier(schema.Name)+".streams")
		findings := auditOf(t, schema.Name).findings
		if len(findings) == 0 {
			t.Fatal("every event row is outside every stream and the audit found nothing, so the foreign key is the only thing that ever answered this question")
		}
	})

	t.Run("a stream whose positions do not follow its versions is reported", func(t *testing.T) {
		schema := deployed(t, "eventpg_audit_order")
		name := quoteIdentifier(schema.Name)
		mustExecute(t, "INSERT INTO "+name+".streams (family, key, version) VALUES ('eventpg.audit.order', 'a', 2)")
		for _, planted := range []struct{ version, position int }{{1, 200}, {2, 100}} {
			mustExecute(t, "INSERT INTO "+name+".events (position, family, key, version, type, revision, payload, recorded_at)"+
				" OVERRIDING SYSTEM VALUE VALUES ($1, 'eventpg.audit.order', 'a', $2, 'eventpg.audit.order.held', 1, ''::bytea, statement_timestamp())",
				planted.position, planted.version)
		}
		findings := auditOf(t, schema.Name).findings
		if len(findings) == 0 {
			t.Fatal("a stream holds version 2 at a position below version 1 and the audit found nothing, so delivery order and version order were never compared")
		}
	})

	t.Run("the schema the rest of this suite wrote to holds all three", func(t *testing.T) {
		schema := sharedSchema(t)
		store := prepared(t, schema)
		raceInto(t, store, "eventpg.audit.shared")

		held := auditOf(t, schema.Name)
		if len(held.findings) != 0 {
			t.Fatalf("the schema every other case of this suite wrote to is inconsistent: %v", held.findings)
		}
		if held.streams == 0 || held.events == 0 {
			t.Fatalf("the shared schema holds %d streams and %d events, so nothing was audited", held.streams, held.events)
		}
	})
}

// Contention, a rollback and a losing append, because the audit is only worth
// running over rows several writers produced at once: a two-statement append
// leaves the stream row and the events disagreeing exactly when two writers
// interleave, and never when one writer works alone.
func raceInto(t *testing.T, store *Store, family string) (int, int) {
	t.Helper()
	const streams, writers, each = 3, 4, 3
	repo, fact := boundRepo(t, store, family)
	ctx := t.Context()
	_, stale, err := repo.Load(ctx, "account-0")
	if err != nil {
		t.Fatalf("loading a fresh stream answered %v", err)
	}

	var group sync.WaitGroup
	for stream := range streams {
		id := "account-" + strconv.Itoa(stream)
		for range writers {
			group.Add(1)
			go func() {
				defer group.Done()
				for done := 0; done < each; {
					_, at, err := repo.Load(ctx, id)
					if err != nil {
						t.Errorf("loading %q under contention answered %v", id, err)
						return
					}
					_, _, err = repo.Append(ctx, at, fact.New(id, held{Bytes: []byte("audited")}))
					switch {
					case err == nil:
						done++
					case errors.Is(err, event.ErrConflict):
					default:
						t.Errorf("an append to %q under contention answered %v", id, err)
						return
					}
				}
			}()
		}
	}
	group.Wait()

	rolledBack(t, store, repo, fact, "account-0")
	if _, _, err := repo.Append(ctx, stale, fact.New("account-0", held{Bytes: []byte("stale")})); !errors.Is(err, event.ErrConflict) {
		t.Fatalf("an append at a version this stream left long ago answered %v, so the losing writer this audit wanted never lost", err)
	}
	return streams, streams * writers * each
}

func rolledBack(t *testing.T, store *Store, repo *event.Repo[held, string], fact *event.Fact[held, string, held], id string) {
	t.Helper()
	inside, tx := begin(t, store, nil)
	_, at, err := repo.Load(inside, id)
	if err != nil {
		t.Fatalf("loading %q inside a transaction answered %v", id, err)
	}
	if _, _, err := repo.Append(inside, at, fact.New(id, held{Bytes: []byte("burnt")})); err != nil {
		t.Fatalf("an append inside a transaction answered %v", err)
	}
	if err := tx.Rollback(context.WithoutCancel(inside)); err != nil {
		t.Fatalf("rolling back answered %v", err)
	}
}

func auditOf(t *testing.T, schema string) audited {
	t.Helper()
	name := quoteIdentifier(schema)
	held := audited{
		streams: rowCount(t, "SELECT count(*) FROM "+name+".streams"),
		events:  rowCount(t, "SELECT count(*) FROM "+name+".events"),
	}
	held.findings = append(held.findings, reported(t, schema,
		"a stream row names a version its own events do not reach",
		"SELECT s.family, s.key, s.version, COALESCE(max(e.version), 0)"+
			"  FROM "+name+".streams s"+
			"  LEFT JOIN "+name+".events e ON e.family = s.family AND e.key = s.key"+
			" GROUP BY s.family, s.key, s.version"+
			" HAVING s.version <> COALESCE(max(e.version), 0)")...)
	held.findings = append(held.findings, reported(t, schema,
		"an event row belongs to no stream",
		"SELECT e.family, e.key, count(*), 0 FROM "+name+".events e"+
			" WHERE NOT EXISTS (SELECT 1 FROM "+name+".streams s WHERE s.family = e.family AND s.key = e.key)"+
			" GROUP BY e.family, e.key")...)
	held.findings = append(held.findings, reported(t, schema,
		"a stream's positions do not follow its versions",
		"SELECT family, key, count(*), 0 FROM ("+
			"SELECT family, key, position, lag(position) OVER (PARTITION BY family, key ORDER BY version) AS previous"+
			"  FROM "+name+".events) ordered"+
			" WHERE previous IS NOT NULL AND position <= previous"+
			" GROUP BY family, key")...)
	return held
}

// A context of its own, because the audit also runs from the conformance
// factory's cleanup, where the test's own context has already been cancelled.
func auditWindow(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	return context.WithTimeout(context.WithoutCancel(t.Context()), 30*time.Second)
}

func reported(t *testing.T, schema, what, statement string) []string {
	t.Helper()
	ctx, stop := auditWindow(t)
	defer stop()
	rows, err := liveDB(t).QueryContext(ctx, statement)
	if err != nil {
		t.Fatalf("the audit of %q could not ask whether %s: %v", schema, what, err)
	}
	defer func() { _ = rows.Close() }()
	var found []string
	for rows.Next() {
		var family, key string
		var left, right int64
		if err := rows.Scan(&family, &key, &left, &right); err != nil {
			t.Fatalf("the audit of %q could not read what it found: %v", schema, err)
		}
		found = append(found, fmt.Sprintf("%s in %s: %s/%s (%d, %d)", what, schema, family, key, left, right))
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("the audit of %q could not be read to its end: %v", schema, err)
	}
	return found
}

func rowCount(t *testing.T, statement string) int {
	t.Helper()
	ctx, stop := auditWindow(t)
	defer stop()
	var count int
	if err := liveDB(t).QueryRowContext(ctx, statement).Scan(&count); err != nil {
		t.Fatalf("%.60q answered no count: %v", statement, err)
	}
	return count
}

type walletV1 struct{ Amount int64 }

type walletV2 struct {
	Amount   int64
	Currency string
}

type wallet struct {
	Balance  int64
	Currency string
}

// The rows a v1 deployment wrote, read by a v2 one: the stored bytes carry
// revision 1 for ever and the upcaster is what makes them the current shape. A
// store that rewrote history on read would answer the same folded state and be
// unrecoverable, which is why the bytes are compared before and after.
func TestStoredRowsAreUpcastAndNeverRewritten(t *testing.T) {
	schema := deployed(t, "eventpg_upcast")
	store := prepared(t, schema)
	const family = "eventpg.upcast.wallet"
	ctx := t.Context()

	writing, credited := declareWalletV1(t, family)
	repo, err := event.Bind(event.Open(store), writing)
	if err != nil {
		t.Fatalf("binding the v1 declaration answered %v", err)
	}
	_, at, err := repo.Load(ctx, "a")
	if err != nil {
		t.Fatalf("loading a fresh stream answered %v", err)
	}
	for _, amount := range []int64{1, 2, 3} {
		if at, _, err = repo.Append(ctx, at, credited.New("a", walletV1{Amount: amount})); err != nil {
			t.Fatalf("appending a v1 credit of %d answered %v", amount, err)
		}
	}

	written := stored(t, schema, aStream(family, "a"))
	if len(written) != 3 {
		t.Fatalf("three v1 credits left %d rows", len(written))
	}
	for _, row := range written {
		if row.revision != 1 {
			t.Fatalf("a payload written through a one-reader declaration was stored at revision %d", row.revision)
		}
	}

	reading, _ := declareWalletV2(t, family)
	upcasting, err := event.Bind(event.Open(store), reading)
	if err != nil {
		t.Fatalf("binding the v2 declaration answered %v", err)
	}
	state, _, err := upcasting.Load(ctx, "a")
	if err != nil {
		t.Fatalf("loading v1 rows through the v2 declaration answered %v", err)
	}
	if state.Balance != 6 || state.Currency != "eur" {
		t.Fatalf("three v1 credits fold through the upcaster to %+v, where the upcaster names every one of them eur", state)
	}

	if after := stored(t, schema, aStream(family, "a")); !sameRows(written, after) {
		t.Fatalf("the stored rows changed when a later build read them: %v then %v", payloads(written), payloads(after))
	}
	if again, _, err := repo.Load(ctx, "a"); err != nil || again.Balance != 6 || again.Currency != "" {
		t.Fatalf("the v1 declaration folds the same rows to %+v (%v), so the upcast above is the v2 declaration's and not a rewrite", again, err)
	}
}

func declareWalletV1(t *testing.T, family string) (*event.Aggregate[wallet, string], *event.Fact[wallet, string, walletV1]) {
	t.Helper()
	aggregate, err := event.TryDefine[wallet](family, func(id string) event.Key { return event.Key(id) })
	if err != nil {
		t.Fatalf("the v1 aggregate was refused: %v", err)
	}
	fact, err := event.TryDeclare(aggregate, family+".credited", event.From(event.JSON[walletV1]()),
		func(state wallet, carried walletV1) wallet {
			state.Balance += carried.Amount
			return state
		})
	if err != nil {
		t.Fatalf("the v1 fact was refused: %v", err)
	}
	return aggregate, fact
}

func declareWalletV2(t *testing.T, family string) (*event.Aggregate[wallet, string], *event.Fact[wallet, string, walletV2]) {
	t.Helper()
	aggregate, err := event.TryDefine[wallet](family, func(id string) event.Key { return event.Key(id) })
	if err != nil {
		t.Fatalf("the v2 aggregate was refused: %v", err)
	}
	chain := event.Then(event.From(event.JSON[walletV1]()), event.JSON[walletV2](),
		func(before walletV1) (walletV2, error) {
			return walletV2{Amount: before.Amount, Currency: "eur"}, nil
		})
	fact, err := event.TryDeclare(aggregate, family+".credited", chain,
		func(state wallet, carried walletV2) wallet {
			state.Balance += carried.Amount
			state.Currency = carried.Currency
			return state
		})
	if err != nil {
		t.Fatalf("the v2 fact was refused: %v", err)
	}
	return aggregate, fact
}

func sameRows(before, after []storedRow) bool {
	if len(before) != len(after) {
		return false
	}
	for index := range before {
		if before[index].position != after[index].position || before[index].version != after[index].version ||
			before[index].revision != after[index].revision || before[index].name != after[index].name ||
			string(before[index].payload) != string(after[index].payload) {
			return false
		}
	}
	return true
}

// The other direction of the same mixed release, and it is the one a rollback
// takes: the v2 build wrote while a v1 build was still serving. An old reader
// meeting revision 2 must refuse loudly and fold nothing, because the rows it
// can read are a prefix of a history whose tail it cannot — and a state folded
// from that prefix is a balance that is simply wrong, with no refusal anywhere
// to say so.
//
// What the v1 build keeps is the bounded read: the prefix up to the version
// before the first revision-2 row still folds, because every row in it is a
// revision this build retains. That is the one thing an operator can still do
// during a rollback, and it is asked here rather than assumed.
func TestARolledBackBuildMeetingRevisionTwoRefusesRatherThanFolding(t *testing.T) {
	schema := deployed(t, "eventpg_rollback")
	store := prepared(t, schema)
	const family = "eventpg.rollback.wallet"
	ctx := t.Context()

	old, oldCredited := declareWalletV1(t, family)
	v1, err := event.Bind(event.Open(store), old)
	if err != nil {
		t.Fatalf("binding the v1 declaration answered %v", err)
	}
	_, at, err := v1.Load(ctx, "a")
	if err != nil {
		t.Fatalf("loading a fresh stream answered %v", err)
	}
	for _, amount := range []int64{1, 2} {
		if at, _, err = v1.Append(ctx, at, oldCredited.New("a", walletV1{Amount: amount})); err != nil {
			t.Fatalf("appending a v1 credit of %d answered %v", amount, err)
		}
	}
	stale := at

	current, currentCredited := declareWalletV2(t, family)
	v2, err := event.Bind(event.Open(store), current)
	if err != nil {
		t.Fatalf("binding the v2 declaration answered %v", err)
	}
	state, ahead, err := v2.Load(ctx, "a")
	if err != nil {
		t.Fatalf("the v2 build could not read the two v1 rows: %v", err)
	}
	if state.Balance != 3 {
		t.Fatalf("the v2 build folded the two v1 credits to %+v", state)
	}
	if _, _, err = v2.Append(ctx, ahead, currentCredited.New("a", walletV2{Amount: 4, Currency: "usd"})); err != nil {
		t.Fatalf("the v2 build could not append at revision 2: %v", err)
	}

	written := stored(t, schema, aStream(family, "a"))
	if len(written) != 3 {
		t.Fatalf("two v1 credits and one v2 credit left %d rows", len(written))
	}
	if written[2].revision != 2 {
		t.Fatalf("the credit the v2 build wrote was stored at revision %d, so the case below meets nothing new", written[2].revision)
	}

	t.Run("the v1 build refuses the whole stream and folds nothing", func(t *testing.T) {
		folded, reached, err := v1.Load(ctx, "a")
		if !errors.Is(err, event.ErrRevision) {
			t.Fatalf("a build that retains one revision read a stream holding revision 2 and answered %v", err)
		}
		if folded != (wallet{}) {
			t.Fatalf("the refused load handed back %+v, and a partially folded balance is the one answer a rollback may not produce", folded)
		}
		if reached.Version() != 0 {
			t.Fatalf("the refused load handed back version %d, and a version from a refused read is the first half of a write", reached.Version())
		}
	})

	t.Run("the v1 build cannot append over what it could not read", func(t *testing.T) {
		if _, _, err := v1.Append(ctx, stale, oldCredited.New("a", walletV1{Amount: 8})); !errors.Is(err, event.ErrConflict) {
			t.Fatalf("an append decided at the version this build last saw answered %v, and the stream has moved past it", err)
		}
		if after := stored(t, schema, aStream(family, "a")); len(after) != 3 {
			t.Fatalf("the refused append left %d rows where the stream held 3", len(after))
		}
	})

	t.Run("the v1 build still reads the prefix written before the rollback", func(t *testing.T) {
		folded, err := v1.StateAt(ctx, "a", event.Version(2))
		if err != nil {
			t.Fatalf("the prefix of two revision-1 rows answered %v to the build that wrote them", err)
		}
		if folded.Balance != 3 {
			t.Fatalf("the prefix folded to %+v where its two credits sum to 3", folded)
		}
		if _, err := v1.StateAt(ctx, "a", event.Version(3)); !errors.Is(err, event.ErrRevision) {
			t.Fatalf("a prefix reaching the revision-2 row answered %v", err)
		}
	})

	t.Run("nothing the v1 build did rewrote a row", func(t *testing.T) {
		if after := stored(t, schema, aStream(family, "a")); !sameRows(written, after) {
			t.Fatalf("the stored rows changed while an older build failed to read them: %v then %v", payloads(written), payloads(after))
		}
		state, _, err := v2.Load(ctx, "a")
		if err != nil || state.Balance != 7 || state.Currency != "usd" {
			t.Fatalf("the v2 build reads %+v (%v) after the v1 build's refusals, where its three credits sum to 7", state, err)
		}
	})
}
