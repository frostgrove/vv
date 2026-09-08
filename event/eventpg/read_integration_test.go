//go:build integration

package eventpg

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/adapter/crudsql"
	"github.com/frostgrove/vv/event"
)

func tunedStore(t *testing.T, schema Schema, page int) *Store {
	t.Helper()
	db := liveDB(t)
	store, err := New(Spec{DB: db, Source: crudsql.Postgres(db), Schema: schema, StreamPage: page, MaxRead: page})
	if err != nil {
		t.Fatalf("a store paging %d at a time was refused: %v", page, err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Prepare(t.Context()); err != nil {
		t.Fatalf("a store paging %d at a time did not verify the deployed schema: %v", page, err)
	}
	return store
}

func appendOn(t *testing.T, ctx context.Context, store *Store, stream event.Stream, expected event.Version, texts ...string) {
	t.Helper()
	records := make([]event.Record, 0, len(texts))
	for _, text := range texts {
		records = append(records, event.Record{Type: stream.Family + ".held", Revision: 1, Payload: []byte(text)})
	}
	if err := store.Append(ctx, event.AppendRequest{Stream: stream, Expected: expected, Records: records}); err != nil {
		t.Fatalf("appending %v to %v at version %d answered %v", texts, stream, expected, err)
	}
}

func readAll(t *testing.T, ctx context.Context, store *Store, after event.Cursor) ([]event.Envelope, event.Cursor) {
	t.Helper()
	page, cursor, err := store.ReadAll(ctx, after)
	if err != nil {
		t.Fatalf("a walk of the log answered %v", err)
	}
	return page, cursor
}

func drainLog(t *testing.T, ctx context.Context, store *Store) ([]event.Envelope, event.Cursor) {
	t.Helper()
	return drainFrom(t, ctx, store, "")
}

// Walks until a page comes back empty, so what it answers is the whole log this
// store will hand over rather than one page of it.
func drainFrom(t *testing.T, ctx context.Context, store *Store, cursor event.Cursor) ([]event.Envelope, event.Cursor) {
	t.Helper()
	var whole []event.Envelope
	for range 64 {
		page, next := readAll(t, ctx, store, cursor)
		cursor = next
		whole = append(whole, page...)
		if len(page) == 0 {
			return whole, cursor
		}
	}
	t.Fatal("the log did not run out in sixty-four pages, so this walk is not making progress")
	return nil, ""
}

// The store's own cause, read the way a consumer reads it. event.CauseOf reaches
// into the kernel's refusal and a store's own failure is not one, so a case that
// asserts what a refusal carries enters the door through the kernel rather than
// at the store's seam.
func walked(t *testing.T, ctx context.Context, log event.Log, after event.Cursor) error {
	t.Helper()
	reader, err := event.Read(log, after)
	if err != nil {
		t.Fatalf("a reader over the store could not be opened: %v", err)
	}
	_, err = reader.Next(ctx)
	if err == nil {
		t.Fatal("the walk this case is built around was admitted, so nothing below was measured")
	}
	return err
}

func positionsOf(page []event.Envelope) []event.Position {
	held := make([]event.Position, 0, len(page))
	for _, envelope := range page {
		held = append(held, envelope.Position)
	}
	return held
}

func payloadsOf(page []event.Envelope) []string {
	held := make([]string, 0, len(page))
	for _, envelope := range page {
		held = append(held, string(envelope.Payload))
	}
	return held
}

func logOf(t *testing.T, store *Store) [logBytes]byte {
	t.Helper()
	ready := store.state.Load()
	if ready == nil {
		t.Fatal("the store has not verified its schema, so it names no log")
	}
	return ready.log
}

func storedPositions(t *testing.T, schema Schema) []int64 {
	t.Helper()
	rows, err := liveDB(t).QueryContext(t.Context(),
		"SELECT position FROM "+quoteIdentifier(schema.Name)+".events ORDER BY position")
	if err != nil {
		t.Fatalf("the positions of %q could not be read: %v", schema.Name, err)
	}
	defer func() { _ = rows.Close() }()
	var held []int64
	for rows.Next() {
		var position int64
		if err := rows.Scan(&position); err != nil {
			t.Fatalf("a position of %q could not be read: %v", schema.Name, err)
		}
		held = append(held, position)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("the positions of %q could not be read to their end: %v", schema.Name, err)
	}
	return held
}

func TestAStreamPagesToItsEndAndFoldsTheSameThroughASibling(t *testing.T) {
	const family = "eventpg.s4.paging"
	const page = 4
	const events = 9
	schema := sharedSchema(t)
	ctx := t.Context()
	store := tunedStore(t, schema, page)
	stream := aStream(family, "A-paged")

	for version := range events {
		appendOn(t, ctx, store, stream, event.Version(version), fmt.Sprintf("event %d", version+1))
	}

	var whole []event.Envelope
	var sizes []int
	for after := event.Version(0); ; {
		read, err := store.ReadStream(ctx, stream, after)
		if err != nil {
			t.Fatalf("reading %v after version %d answered %v", stream, after, err)
		}
		sizes = append(sizes, len(read))
		whole = append(whole, read...)
		after += event.Version(len(read))
		if len(read) < page {
			break
		}
	}

	if !slices.Equal(sizes, []int{page, page, events - 2*page}) {
		t.Fatalf("a stream of %d events read as pages of %v where a store that pages at %d fills every page but the last", events, sizes, page)
	}
	for offset, envelope := range whole {
		if envelope.Version != event.Version(offset)+1 {
			t.Fatalf("the pages concatenate to versions %v, which is not dense from one", versionsOf(whole))
		}
		if envelope.Stream != stream {
			t.Fatalf("a page of %v carries %v", stream, envelope.Stream)
		}
		if offset > 0 && envelope.Position <= whole[offset-1].Position {
			t.Fatalf("the stream reads back at positions %v, which do not ascend with its versions", positionsOf(whole))
		}
	}
	if got := payloadsOf(whole); len(got) != events || got[0] != "event 1" || got[events-1] != fmt.Sprintf("event %d", events) {
		t.Fatalf("the stream reads back as %v", got)
	}

	t.Run("a page after the end of the stream is empty rather than a page of something else", func(t *testing.T) {
		read, err := store.ReadStream(ctx, stream, events)
		if err != nil || len(read) != 0 {
			t.Fatalf("reading past the end of %v answered %d envelopes and %v", stream, len(read), err)
		}
	})

	t.Run("the same replay through a sibling at a page of one folds to the same state", func(t *testing.T) {
		repo, _ := boundRepo(t, store, family)
		state, at, err := repo.Load(ctx, "A-paged")
		if err != nil {
			t.Fatalf("folding %v through a store paging %d at a time answered %v", stream, page, err)
		}
		sibling, _ := boundRepo(t, tunedStore(t, schema, 1), family)
		byOne, atOne, err := sibling.Load(ctx, "A-paged")
		if err != nil {
			t.Fatalf("folding %v through a store paging one at a time answered %v", stream, err)
		}
		if at.Version() != event.Version(events) || atOne.Version() != at.Version() {
			t.Fatalf("the fold ends at version %d through a page of %d and at version %d through a page of one, where the stream holds %d events",
				at.Version(), page, atOne.Version(), events)
		}
		if string(state.Bytes) != string(byOne.Bytes) {
			t.Fatalf("the same stream folds to %q at a page of %d and to %q at a page of one, so a page boundary changes what a consumer reads",
				state.Bytes, page, byOne.Bytes)
		}
	})
}

func versionsOf(page []event.Envelope) []event.Version {
	held := make([]event.Version, 0, len(page))
	for _, envelope := range page {
		held = append(held, envelope.Version)
	}
	return held
}

func TestARowOutsideTheSchemasPromisesRefusesTheWholeRead(t *testing.T) {
	const family = "eventpg.s4.promises"
	const marker = "a payload no refusal may print"
	ctx := t.Context()
	longName := strings.Repeat("t", event.MaxNameBytes+1)
	longKey := strings.Repeat("k", DefaultMaxKey+1)

	for _, planted := range []struct {
		what        string
		constraints []string
		key         string
		name        string
		revision    int
		payload     []byte
		secrets     []string
	}{
		{
			what:        "a type name over the kernel's own ceiling",
			constraints: []string{"events_type_check"},
			key:         "A-type", name: longName, revision: 1, payload: []byte(marker),
			secrets: []string{longName, marker},
		},
		{
			what:        "a revision of zero",
			constraints: []string{"events_revision_check"},
			key:         "A-revision", name: family + ".held", revision: 0, payload: []byte(marker),
			secrets: []string{marker},
		},
		{
			what:        "a payload over the deployed bound",
			constraints: []string{"events_payload_check"},
			key:         "A-payload", name: family + ".held", revision: 1, payload: make([]byte, DefaultMaxPayload+1),
			secrets: []string{},
		},
		{
			what:        "a key over the deployed bound",
			constraints: []string{"events_key_check", "streams_key_check"},
			key:         longKey, name: family + ".held", revision: 1, payload: []byte(marker),
			secrets: []string{longKey, marker},
		},
	} {
		t.Run(planted.what+" refuses the whole read", func(t *testing.T) {
			schema := deployed(t, "eventpg_s4_promise")
			store := prepared(t, schema)
			beside := aStream(family, "A-good")
			appendOn(t, ctx, store, beside, 0, "a row the schema would have taken")

			whole, _ := drainLog(t, ctx, store)
			if got := payloadsOf(whole); !slices.Equal(got, []string{"a row the schema would have taken"}) {
				t.Fatalf("the log holds %v before anything was planted, so the refusal below would not be about the planted row", got)
			}

			for _, constraint := range planted.constraints {
				table := strings.SplitN(constraint, "_", 2)[0]
				mustExecute(t, "ALTER TABLE "+quoteIdentifier(schema.Name)+"."+table+" DROP CONSTRAINT "+constraint)
			}
			stream := aStream(family, planted.key)
			mustExecute(t, "INSERT INTO "+quoteIdentifier(schema.Name)+".streams (family, key, version) VALUES ($1, $2, $3)",
				stream.Family, string(stream.Key), int64(1))
			mustExecute(t, "INSERT INTO "+quoteIdentifier(schema.Name)+
				".events (family, key, version, type, revision, payload, recorded_at) VALUES ($1, $2, $3, $4, $5, $6, statement_timestamp())",
				stream.Family, string(stream.Key), int64(1), planted.name, int32(planted.revision), planted.payload)

			page, cursor, err := store.ReadAll(ctx, "")
			classifiedAs(t, err, event.Unclassified, "a walk over a log holding "+planted.what)
			if len(page) != 0 || cursor != "" {
				t.Fatalf("the walk answered %d envelopes and the cursor %q, where a row it cannot classify refuses the whole page", len(page), cursor)
			}

			read, err := store.ReadStream(ctx, stream, 0)
			classifiedAs(t, err, event.Unclassified, "a replay of a stream holding "+planted.what)
			if len(read) != 0 {
				t.Fatalf("the replay answered %d envelopes where the row it read is one the schema would not have taken", len(read))
			}

			refusal := walked(t, ctx, store, "")
			if !errors.Is(refusal, event.ErrBackend) {
				t.Fatalf("a consumer walking the log reads %v, where a store that scanned a row it cannot account for has failed", refusal)
			}
			cause := event.CauseOf(refusal)
			if !errors.Is(cause, errRowOutsideSchema) {
				t.Fatalf("the refusal carries %v, and a row the deployed schema would not have taken is what this store says it read", cause)
			}
			rendered := refusal.Error() + " " + cause.Error()
			for _, secret := range planted.secrets {
				if strings.Contains(rendered, secret) {
					t.Fatalf("the refusal renders the value it refused, and a store's refusal names no key, no type name and no payload: %q", rendered)
				}
			}

			t.Run("the valid rows beside it read normally", func(t *testing.T) {
				read, err := store.ReadStream(ctx, beside, 0)
				if err != nil {
					t.Fatalf("a stream of rows the schema would have taken answered %v, so the refusal above is about the schema and not about the row", err)
				}
				if got := payloadsOf(read); !slices.Equal(got, []string{"a row the schema would have taken"}) {
					t.Fatalf("the stream beside the planted row reads back as %v", got)
				}
			})
		})
	}

	t.Run("the same read over the same schema with nothing planted serves", func(t *testing.T) {
		schema := deployed(t, "eventpg_s4_promise_control")
		store := prepared(t, schema)
		stream := aStream(family, "A-control")
		appendOn(t, ctx, store, stream, 0, "one", "two")
		whole, _ := drainLog(t, ctx, store)
		if got := payloadsOf(whole); !slices.Equal(got, []string{"one", "two"}) {
			t.Fatalf("a log nobody planted anything in reads back as %v, so the refusals above are not what this store does with every schema", got)
		}
	})
}

func TestAnUnpreparedStoreRefusesEveryOperationAndAnswersTheThreePureOnes(t *testing.T) {
	const family = "eventpg.s4.unprepared"
	schema := sharedSchema(t)
	ctx := t.Context()
	pool := countingPool(t, 2)
	store, err := New(Spec{DB: pool, Source: crudsql.Postgres(pool), Schema: schema})
	if err != nil {
		t.Fatalf("a store over a pool of its own was refused: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	stream := aStream(family, "A-unprepared")
	record := []event.Record{{Type: family + ".held", Revision: 1, Payload: []byte("never written")}}

	t.Run("the three pure answers are the store's whatever it has verified", func(t *testing.T) {
		if got := store.Capabilities(); got.Transactions != event.Supported || got.MonotoneVisibility != event.Unsupported {
			t.Errorf("a store that has verified nothing publishes %+v", got)
		}
		if got := store.Limits(); got.MaxPayload != DefaultMaxPayload || got.StreamPage != DefaultPage {
			t.Errorf("a store that has verified nothing publishes %+v", got)
		}
		if !store.Backing().Equal(store.Backing()) {
			t.Error("a store that has verified nothing names no backing, and a backing is the database and the schema together")
		}
	})

	recount(t)
	t.Run("the three operations refuse as policy and issue nothing", func(t *testing.T) {
		classifiedAs(t, store.Append(ctx, event.AppendRequest{Stream: stream, Expected: 0, Records: record}),
			event.Refused, "an append through a store that has not verified its schema")
		_, err := store.ReadStream(ctx, stream, 0)
		classifiedAs(t, err, event.Refused, "a replay through a store that has not verified its schema")
		_, _, err = store.ReadAll(ctx, "")
		classifiedAs(t, err, event.Refused, "a walk through a store that has not verified its schema")
		if execs, queries := counted(t); execs != 0 || queries != 0 {
			t.Fatalf("the three refusals issued %d statements and %d queries, and a store that refuses as policy leaves the caller's transaction untouched", execs, queries)
		}
	})

	t.Run("the readiness the refusal names is the one a caller reads", func(t *testing.T) {
		refusal := walked(t, ctx, store, "")
		if !errors.Is(refusal, event.ErrRefused) {
			t.Fatalf("a consumer walking through a store that has verified nothing reads %v, and the two the kernel would otherwise render are both retry classes", refusal)
		}
		if !errors.Is(event.CauseOf(refusal), ErrNotReady) {
			t.Fatalf("the refusal carries %v where a store that has not verified says so", event.CauseOf(refusal))
		}
	})

	t.Run("Transaction answers what the context carries and reports no readiness", func(t *testing.T) {
		if authority, err := store.Transaction(ctx); err != nil || authority.Valid() {
			t.Fatalf("a store with nothing bound answers (%v, %v)", authority, err)
		}
		inside, tx := begin(t, store, nil)
		authority, err := store.Transaction(inside)
		if err != nil || !authority.Valid() {
			t.Fatalf("a store that has not verified answers (%v, %v) for a transaction of its own data source", authority, err)
		}
		if err := tx.Rollback(inside); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("after a successful Prepare the same calls serve", func(t *testing.T) {
		if err := store.Prepare(ctx); err != nil {
			t.Fatalf("the store did not verify the deployed schema: %v", err)
		}
		if err := store.Append(ctx, event.AppendRequest{Stream: stream, Expected: 0, Records: record}); err != nil {
			t.Fatalf("the same append after the store verified answered %v", err)
		}
		if _, err := store.ReadStream(ctx, stream, 0); err != nil {
			t.Fatalf("the same replay after the store verified answered %v", err)
		}
		if _, _, err := store.ReadAll(ctx, ""); err != nil {
			t.Fatalf("the same walk after the store verified answered %v", err)
		}
	})
}

func TestCloseIsIdempotentAndClosesNothingItDidNotOpen(t *testing.T) {
	const family = "eventpg.s4.closed"
	schema := sharedSchema(t)
	ctx := t.Context()
	store := prepared(t, schema)
	beside := prepared(t, schema)
	stream := aStream(family, "A-closed")
	appendOn(t, ctx, store, stream, 0, "written before the close")

	for pass := range 2 {
		if err := store.Close(); err != nil {
			t.Fatalf("closing the store the %dth time answered %v", pass+1, err)
		}
	}

	classifiedAs(t, store.Append(ctx, event.AppendRequest{
		Stream:   stream,
		Expected: 1,
		Records:  []event.Record{{Type: family + ".held", Revision: 1, Payload: []byte("never written")}},
	}), event.Closed, "an append through a closed store")
	_, err := store.ReadStream(ctx, stream, 0)
	classifiedAs(t, err, event.Closed, "a replay through a closed store")
	_, _, err = store.ReadAll(ctx, "")
	classifiedAs(t, err, event.Closed, "a walk through a closed store")

	t.Run("Transaction still answers what the context carries", func(t *testing.T) {
		inside, tx := begin(t, store, nil)
		authority, err := store.Transaction(inside)
		if err != nil || !authority.Valid() {
			t.Fatalf("a closed store answers (%v, %v) for a transaction of its own data source, and closing it did not change what the context carries",
				authority, err)
		}
		if err := tx.Rollback(inside); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("a second store value over the same backing keeps serving", func(t *testing.T) {
		if err := beside.Check(ctx); err != nil {
			t.Fatalf("a store beside the closed one answers %v, so Close closed a pool the composition root owns", err)
		}
		read, err := beside.ReadStream(ctx, stream, 0)
		if err != nil {
			t.Fatalf("a store beside the closed one answered %v", err)
		}
		if got := payloadsOf(read); !slices.Equal(got, []string{"written before the close"}) {
			t.Fatalf("a store beside the closed one reads %v", got)
		}
		appendOn(t, ctx, beside, stream, 1, "written after the close")
	})
}

// Every failure the store can be driven into, walked at the store's own seam
// rather than through the kernel's rendering of it: the kernel maps outcomes and
// a store that answered its own sentinel would be mapped as something else. The
// one door that answers neither shape is Transaction, and it is the kernel's own
// contract: event.Repo turns its error into ErrAmbientNotTransaction and reads
// the cause through errors.Is, which a classified failure does not unwrap to.
func TestEveryErrorTheEightMethodsProduceIsNilAContextErrorOrAFailure(t *testing.T) {
	const family = "eventpg.s4.shapes"
	ctx := t.Context()
	shared := sharedSchema(t)
	stream := aStream(family, "A-shapes")
	record := []event.Record{{Type: family + ".held", Revision: 1, Payload: []byte("a record")}}
	request := event.AppendRequest{Stream: stream, Expected: 0, Records: record}

	serving := prepared(t, shared)
	appendOn(t, ctx, serving, stream, 0, "the row a conflict is refused against")

	cancelled, cancel := context.WithCancel(ctx)
	cancel()

	closed := prepared(t, shared)
	_ = closed.Close()

	unprepared := openStore(t, shared, VerifySchema)

	torn := deployed(t, "eventpg_s4_shapes_torn")
	tornStore := prepared(t, torn)
	dropSchema(t, torn.Name)

	refusing := crud.BindExecutor(ctx, serving.source, crudsql.From(serving.db))

	var seen []event.Outcome
	var contexts int
	for _, entry := range []struct {
		doing string
		door  func() error
	}{
		{"appending on a done context", func() error { return serving.Append(cancelled, request) }},
		{"replaying on a done context", func() error { _, err := serving.ReadStream(cancelled, stream, 0); return err }},
		{"walking on a done context", func() error { _, _, err := serving.ReadAll(cancelled, ""); return err }},
		{"appending through a closed store", func() error { return closed.Append(ctx, request) }},
		{"replaying through a closed store", func() error { _, err := closed.ReadStream(ctx, stream, 0); return err }},
		{"walking through a closed store", func() error { _, _, err := closed.ReadAll(ctx, ""); return err }},
		{"appending through a store that verified nothing", func() error { return unprepared.Append(ctx, request) }},
		{"replaying through a store that verified nothing", func() error { _, err := unprepared.ReadStream(ctx, stream, 0); return err }},
		{"walking through a store that verified nothing", func() error { _, _, err := unprepared.ReadAll(ctx, ""); return err }},
		{"appending under an ambient executor that is not a transaction", func() error { return serving.Append(refusing, request) }},
		{"replaying under an ambient executor that is not a transaction", func() error { _, err := serving.ReadStream(refusing, stream, 0); return err }},
		{"walking under an ambient executor that is not a transaction", func() error { _, _, err := serving.ReadAll(refusing, ""); return err }},
		{"walking from a cursor of another log", func() error { _, _, err := serving.ReadAll(ctx, "vve1nonsense"); return err }},
		{"appending at a version the stream has left", func() error { return serving.Append(ctx, request) }},
		{"appending against a schema that is not there", func() error { return tornStore.Append(ctx, request) }},
		{"replaying against a schema that is not there", func() error { _, err := tornStore.ReadStream(ctx, stream, 0); return err }},
		{"walking against a schema that is not there", func() error { _, _, err := tornStore.ReadAll(ctx, ""); return err }},
		{"appending what nobody decided", func() error { return serving.Append(ctx, event.AppendRequest{Stream: stream, Expected: 1}) }},
		{"closing a store twice", func() error { return closed.Close() }},
	} {
		err := entry.door()
		switch {
		case err == nil:
		case errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded):
			contexts++
		default:
			outcome, classified := outcomeRendered(err)
			if !classified {
				t.Errorf("%s answered %v, which is neither nil, a bare context error, nor a failure this store classified", entry.doing, err)
				continue
			}
			seen = append(seen, outcome)
		}
	}

	if contexts != 3 {
		t.Errorf("%d of the three doors answered a cancellation as itself", contexts)
	}
	for _, wanted := range []event.Outcome{event.Closed, event.Refused, event.Conflict, event.BadCursor, event.Unclassified} {
		if !slices.Contains(seen, wanted) {
			t.Errorf("no door this walk drove answered %v, so the shapes it checked are not the shapes this store produces", wanted)
		}
	}
	if len(seen) < 12 {
		t.Fatalf("this walk classified %d failures, and the doors above produce more than that, so it walked past most of them", len(seen))
	}

	t.Run("Transaction answers the value the kernel turns into its own wiring sentinel", func(t *testing.T) {
		authority, err := serving.Transaction(refusing)
		if authority.Valid() || !errors.Is(err, errAmbientNotTransaction) {
			t.Fatalf("the store answers (%v, %v) for an ambient executor that is not a transaction, and event.Repo reads that cause through errors.Is",
				authority, err)
		}
		if _, classified := outcomeRendered(err); classified {
			t.Fatal("Transaction answered a classified failure, which event.Repo cannot unwrap to the cause it names the refusal after")
		}
	})
}

func outcomeRendered(err error) (event.Outcome, bool) {
	for _, outcome := range []event.Outcome{
		event.Unclassified, event.Conflict, event.NotWritten, event.Unconfirmed,
		event.Closed, event.BadCursor, event.Refused,
	} {
		if err.Error() == event.Failure(outcome, nil).Error() {
			return outcome, true
		}
	}
	return event.Unclassified, false
}
