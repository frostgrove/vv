//go:build integration

package eventpg

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/frostgrove/vv/crud/adapter/crudsql"
	"github.com/frostgrove/vv/event"
)

// A store on the counting driver at a page of this size, so what a bounded read
// costs is the number of statements a driver saw rather than this package's own
// account of a loop it also wrote.
func countingPaged(t *testing.T, schema Schema, page int) *Store {
	t.Helper()
	pool := countingPool(t, 4)
	store, err := New(Spec{DB: pool, Source: crudsql.Postgres(pool), Schema: schema, StreamPage: page, MaxRead: page})
	if err != nil {
		t.Fatalf("a store paging %d at a time over the counting pool was refused: %v", page, err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Prepare(t.Context()); err != nil {
		t.Fatalf("a store paging %d at a time did not verify the deployed schema: %v", page, err)
	}
	recount(t)
	return store
}

// One ReadStream is one QueryContext and a bounded read issues nothing else, so
// the driver's query count is the page count.
func pagesOf(t *testing.T, read func()) int64 {
	t.Helper()
	recount(t)
	read()
	_, queries := counted(t)
	return queries
}

// The live fold is last-write-wins, so the state at version v is the payload of
// version v and a prefix that stopped one short or ran one long is visible in
// the value rather than in a count.
func plantHeld(t *testing.T, ctx context.Context, repo *event.Repo[held, string], fact *event.Fact[held, string, held], id string, events int) {
	t.Helper()
	at := loaded(t, ctx, repo, id)
	changes := make([]event.Change[held], 0, events)
	for version := 1; version <= events; version++ {
		changes = append(changes, fact.New(id, held{Bytes: []byte("v" + strconv.Itoa(version))}))
	}
	if _, _, err := repo.Append(ctx, at, changes...); err != nil {
		t.Fatalf("the stream these bounded reads are taken over could not be written: %v", err)
	}
}

func TestThePrefixAtEveryBoundaryOfARealStream(t *testing.T) {
	const family = "eventpg.s5.prefix"
	const page = 4
	ctx := t.Context()
	schema := deployed(t, "eventpg_s5_prefix")
	store := countingPaged(t, schema, page)
	repo, fact := boundRepo(t, store, family)
	plantHeld(t, ctx, repo, fact, "A-prefix", 11)

	if store.Limits().StreamPage != page {
		t.Fatalf("this store pages %d at a time and the counts below are written for %d", store.Limits().StreamPage, page)
	}

	for _, bound := range []struct {
		version event.Version
		pages   int64
		why     string
	}{
		{1, 1, "the first version of the first page reads that page and discards three of its four"},
		{4, 1, "the last version of the first page reads that page and discards nothing"},
		{5, 2, "the first version of the second page reads two pages, where a loop that read the whole stream would cost three"},
		{8, 2, "the last version of the second page reads two pages"},
		{11, 3, "the version the stream is at reads all three, the last of them short"},
	} {
		var state held
		var err error
		pages := pagesOf(t, func() { state, err = repo.StateAt(ctx, "A-prefix", bound.version) })
		if err != nil {
			t.Fatalf("the prefix at version %d of a live eleven-event stream was refused with %v", bound.version, err)
		}
		if want := "v" + strconv.FormatUint(uint64(bound.version), 10); string(state.Bytes) != want {
			t.Fatalf("the prefix at version %d folded to %q where the last fact of that prefix carries %q, so the page the bound falls inside is truncated at the wrong place",
				bound.version, state.Bytes, want)
		}
		if pages != bound.pages {
			t.Fatalf("the prefix at version %d cost %d statements against the %d pages the bound needs — %s", bound.version, pages, bound.pages, bound.why)
		}
	}

	t.Run("version 6 differs from version 7 by exactly the seventh fact", func(t *testing.T) {
		sixth, err := repo.StateAt(ctx, "A-prefix", 6)
		if err != nil {
			t.Fatalf("the prefix at version 6 was refused with %v", err)
		}
		seventh, err := repo.StateAt(ctx, "A-prefix", 7)
		if err != nil {
			t.Fatalf("the prefix at version 7 was refused with %v", err)
		}
		if string(sixth.Bytes) != "v6" || string(seventh.Bytes) != "v7" {
			t.Fatalf("version 6 folded to %q and version 7 to %q, so the bound is not inclusive of the version asked for", sixth.Bytes, seventh.Bytes)
		}
	})

	t.Run("the whole stream through the bound is the state a load answers", func(t *testing.T) {
		state, token, err := repo.Load(ctx, "A-prefix")
		if err != nil {
			t.Fatalf("the load these bounds are measured against was refused with %v", err)
		}
		if token.Version() != 11 {
			t.Fatalf("the stream loaded at version %d where this case wrote eleven facts", token.Version())
		}
		bounded, err := repo.StateAt(ctx, "A-prefix", 11)
		if err != nil {
			t.Fatalf("the bounded read at the version the stream is at was refused with %v", err)
		}
		if string(bounded.Bytes) != string(state.Bytes) {
			t.Fatalf("the bounded read at version 11 answered %q and the load answered %q", bounded.Bytes, state.Bytes)
		}
	})

	t.Run("the same reads through a load cost three pages each", func(t *testing.T) {
		for range 3 {
			pages := pagesOf(t, func() {
				if _, _, err := repo.Load(ctx, "A-prefix"); err != nil {
					t.Errorf("the load the bounds above are measured against was refused with %v", err)
				}
			})
			if pages != 3 {
				t.Fatalf("an unbounded load of eleven events at a page of %d cost %d statements rather than three, so the counts above are not evidence that the bound does work", page, pages)
			}
		}
	})
}

func TestPastTheEndAnEmptyStreamAndVersionZeroLive(t *testing.T) {
	const family = "eventpg.s5.past"
	const page = 4
	ctx := t.Context()
	schema := deployed(t, "eventpg_s5_past")
	store := countingPaged(t, schema, page)
	repo, fact := boundRepo(t, store, family)
	plantHeld(t, ctx, repo, fact, "A-five", 5)

	var beyond held
	var pastTheEnd error
	reads := pagesOf(t, func() { beyond, pastTheEnd = repo.StateAt(ctx, "A-five", 9) })
	if !errors.Is(pastTheEnd, event.ErrVersion) {
		t.Fatalf("a bound past the end of a live five-event stream answered %v, and the version a caller asked for is data only the caller can correct", pastTheEnd)
	}
	if len(beyond.Bytes) != 0 {
		t.Fatalf("a bound past the end answered %q, so a caller that ignores the error decides on the head state under the name of a historical one", beyond.Bytes)
	}
	if reads != 2 {
		t.Fatalf("a bound past the end of five events at a page of %d cost %d statements against the two the same load costs, so the refusal cost a confirming read", page, reads)
	}

	empty, neverWritten := repo.StateAt(ctx, "A-never", 1)
	if !errors.Is(neverWritten, event.ErrVersion) {
		t.Fatalf("a bounded read of a stream nothing was ever appended to answered %v", neverWritten)
	}
	if len(empty.Bytes) != 0 {
		t.Fatalf("a bounded read of an empty stream answered %q beside a refusal", empty.Bytes)
	}
	if pastTheEnd.Error() != neverWritten.Error() {
		t.Fatalf("a bound past the end reads %q and an empty stream reads %q; an append-only log tells a stream that is empty and a stream that never existed apart nowhere a reader can see, so the two must not be told apart here either",
			pastTheEnd, neverWritten)
	}

	var zero held
	var atVersionZero error
	asked := pagesOf(t, func() { zero, atVersionZero = repo.StateAt(ctx, "A-five", 0) })
	if !errors.Is(atVersionZero, event.ErrVersion) {
		t.Fatalf("a bound of version zero answered %v rather than the refusal that names the caller's own off-by-one", atVersionZero)
	}
	if len(zero.Bytes) != 0 {
		t.Fatalf("a bound of version zero answered %q beside a refusal, which is the answer that hides the off-by-one rather than reporting it", zero.Bytes)
	}
	if asked != 0 {
		t.Fatalf("a bound of version zero issued %d statements, and a version a caller cannot have meant is knowable before anything is asked of a database", asked)
	}

	t.Run("the bound the stream does hold is the control", func(t *testing.T) {
		state, err := repo.StateAt(ctx, "A-five", 5)
		if err != nil {
			t.Fatalf("the version the five-event stream is at was refused with %v, so every arm above refuses everything", err)
		}
		if string(state.Bytes) != "v5" {
			t.Fatalf("the prefix at version 5 folded to %q where the fifth fact carries \"v5\"", state.Bytes)
		}
	})
}

// The two payload shapes the upcasting variant reads, so a row at revision 1
// needs the upcaster and a row at revision 2 does not.
type creditedOnce struct{ Minor int64 }

type creditedTwice struct {
	Minor  int64
	Reason string
}

// A row the schema would have taken, written the way an operator's restore or
// another service's writer would leave it: through SQL, beside the stream row
// that names the version the log reached.
func plantRows(t *testing.T, schema Schema, stream event.Stream, rows []event.Record) {
	t.Helper()
	mustExecute(t, "INSERT INTO "+quoteIdentifier(schema.Name)+".streams (family, key, version) VALUES ($1, $2, $3)",
		stream.Family, string(stream.Key), int64(len(rows)))
	for index, row := range rows {
		mustExecute(t, "INSERT INTO "+quoteIdentifier(schema.Name)+
			".events (family, key, version, type, revision, payload, recorded_at) VALUES ($1, $2, $3, $4, $5, $6, statement_timestamp())",
			stream.Family, string(stream.Key), int64(index+1), row.Type, int32(row.Revision), row.Payload)
	}
}

func TestTheUnreadableEventTableLive(t *testing.T) {
	ctx := t.Context()

	readable := func(family string, version int64) event.Record {
		return event.Record{Type: family + ".held", Revision: 1, Payload: []byte("v" + strconv.FormatInt(version, 10))}
	}

	holdingRepo := func(t *testing.T, store *Store, family string) *event.Repo[held, string] {
		t.Helper()
		repo, _ := boundRepo(t, store, family)
		return repo
	}

	// The same fact at two revisions, the older one reached through an upcaster
	// that refuses: every row but the broken one is at the revision this build
	// writes, so the refusal is the fourth version's and not the first's.
	upcastingRepo := func(t *testing.T, store *Store, family string) *event.Repo[held, string] {
		t.Helper()
		aggregate, err := event.TryDefine[held](family, func(id string) event.Key { return event.Key(id) })
		if err != nil {
			t.Fatalf("the aggregate %q was refused: %v", family, err)
		}
		if _, err := event.TryDeclare(aggregate, family+".held",
			event.Then(event.From(event.JSON[creditedOnce]()), event.JSON[creditedTwice](), func(creditedOnce) (creditedTwice, error) {
				return creditedTwice{}, errors.New("this build cannot read what that revision recorded")
			}),
			func(state held, carried creditedTwice) held { return held{Bytes: []byte(carried.Reason)} }); err != nil {
			t.Fatalf("the fact of %q was refused: %v", family, err)
		}
		repo, err := event.Bind(event.Open(store), aggregate)
		if err != nil {
			t.Fatalf("the declaration whose upcaster refuses was not bound: %v", err)
		}
		return repo
	}

	for _, one := range []struct {
		what        string
		family      string
		scratch     string
		constraints []string
		broken      event.Record
		beside      func(family string, version int64) event.Record
		want        error
		bind        func(*testing.T, *Store, string) *event.Repo[held, string]
	}{
		{
			what: "a wire type this declaration does not know", family: "eventpg.s5.unknown", scratch: "eventpg_s5_unknown",
			broken: event.Record{Type: "eventpg.s5.unknown.retired", Revision: 1, Payload: []byte("gone")},
			want:   event.ErrUnknownType, bind: holdingRepo,
		},
		{
			what: "a revision this fact does not retain", family: "eventpg.s5.revision", scratch: "eventpg_s5_revision",
			broken: event.Record{Type: "eventpg.s5.revision.held", Revision: 3, Payload: []byte("v4")},
			want:   event.ErrRevision, bind: holdingRepo,
		},
		{
			what: "an upcaster that refuses the recorded payload", family: "eventpg.s5.upcast", scratch: "eventpg_s5_upcast",
			broken: event.Record{Type: "eventpg.s5.upcast.held", Revision: 1, Payload: []byte(`{"Minor":4}`)},
			beside: func(family string, version int64) event.Record {
				return event.Record{Type: family + ".held", Revision: 2,
					Payload: []byte(`{"Minor":` + strconv.FormatInt(version, 10) + `,"Reason":"v` + strconv.FormatInt(version, 10) + `"}`)}
			},
			want: event.ErrUpcast, bind: upcastingRepo,
		},
		{
			what: "a recorded payload over the bound the deployed schema published", family: "eventpg.s5.payload", scratch: "eventpg_s5_payload",
			constraints: []string{"events_payload_check"},
			broken:      event.Record{Type: "eventpg.s5.payload.held", Revision: 1, Payload: make([]byte, DefaultMaxPayload+1)},
			want:        event.ErrBackend, bind: holdingRepo,
		},
	} {
		t.Run(one.what, func(t *testing.T) {
			// A page of three puts the broken version in the second page, which
			// is what lets the control below read a prefix that stops before it:
			// a payload over the bound is the STORE's refusal and it refuses the
			// page it scanned, not the row.
			schema := deployed(t, one.scratch)
			store := tunedStore(t, schema, 3)
			repo := one.bind(t, store, one.family)
			beside := one.beside
			if beside == nil {
				beside = readable
			}

			for _, constraint := range one.constraints {
				mustExecute(t, "ALTER TABLE "+quoteIdentifier(schema.Name)+".events DROP CONSTRAINT "+constraint)
			}
			stream := aStream(one.family, "A-broken")
			rows := make([]event.Record, 0, 9)
			for version := int64(1); version <= 9; version++ {
				if version == 4 {
					rows = append(rows, one.broken)
					continue
				}
				rows = append(rows, beside(one.family, version))
			}
			plantRows(t, schema, stream, rows)

			bounded, refused := repo.StateAt(ctx, "A-broken", 9)
			if !errors.Is(refused, one.want) {
				t.Fatalf("%s at version 4 of a nine-fact prefix answered %v where the party that refuses it raises %v", one.what, refused, one.want)
			}
			if len(bounded.Bytes) != 0 {
				t.Fatalf("%s answered %q, and the accumulator holding versions 1 to 3 is the partial state a bounded read may never return", one.what, bounded.Bytes)
			}

			state, _, throughALoad := repo.Load(ctx, "A-broken")
			if !errors.Is(throughALoad, one.want) || throughALoad.Error() != refused.Error() {
				t.Fatalf("%s read to the bound answered %q and read through a load answered %q, so the bounded read carries a failure path of its own rather than the one a load already has",
					one.what, refused, throughALoad)
			}
			if len(state.Bytes) != 0 {
				t.Fatalf("%s through a load answered %q, so the comparison above is between two wrong answers", one.what, state.Bytes)
			}

			t.Run("a bound below the broken version reads normally", func(t *testing.T) {
				state, err := repo.StateAt(ctx, "A-broken", 3)
				if err != nil {
					t.Fatalf("the prefix that stops before the unreadable version was refused with %v, so the refusals above are about the whole stream rather than about the row", err)
				}
				if len(state.Bytes) == 0 {
					t.Fatalf("the prefix that stops before the unreadable version folded to nothing, so it is not evidence that the rows beside the broken one read")
				}
			})
		})
	}
}

// [SPEC] §6 item 24, and it is not a gate: what a bound costs against a stream
// nobody would replay at all is the number ES-09's first gate is eventually
// compared against, and it is recorded in the backlog rather than in a decision
// ([[D-132]] refuses a measured cost as a reason).
func BenchmarkStateAt(b *testing.B) {
	const size = 100_000
	repo, id := replayStand(b, size)
	ctx := context.Background()

	for _, bound := range []struct {
		what    string
		version event.Version
	}{
		{"1 per cent", size / 100},
		{"50 per cent", size / 2},
		{"100 per cent", size},
	} {
		b.Run(bound.what, func(b *testing.B) {
			if _, err := repo.StateAt(ctx, id, bound.version); err != nil {
				b.Fatalf("the warm-up read at version %d answered %v", bound.version, err)
			}
			b.ResetTimer()
			for b.Loop() {
				if _, err := repo.StateAt(ctx, id, bound.version); err != nil {
					b.Fatalf("a bounded read at version %d answered %v", bound.version, err)
				}
			}
			b.StopTimer()
			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*int(bound.version)), "ns/event")
		})
	}
}
