//go:build integration

package eventpg

import (
	"errors"
	"math"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/adapter/crudsql"
	"github.com/frostgrove/vv/event"
)

func TestAnAppendOnThePoolIsAtomicWithNothing(t *testing.T) {
	const family = "eventpg.s3.pool"
	schema := sharedSchema(t)
	repo, fact := boundRepo(t, prepared(t, schema), family)
	stream := aStream(family, "A-1")
	ctx := t.Context()

	at := loaded(t, ctx, repo, "A-1")
	moved, receipt, err := repo.Append(ctx, at, fact.New("A-1", held{Bytes: []byte("credited 10")}))
	if err != nil {
		t.Fatalf("an append at the version it was loaded at, with nothing bound, answered %v", err)
	}
	if receipt.First() != 1 || receipt.Last() != 1 || receipt.Count() != 1 {
		t.Errorf("the receipt names versions %d to %d over %d changes", receipt.First(), receipt.Last(), receipt.Count())
	}
	if receipt.Authority().Valid() {
		t.Error("the receipt of an append nothing was bound for names a transaction, and this append was atomic with nothing")
	}

	version, found := streamVersion(t, schema, stream)
	rows := stored(t, schema, stream)
	if !found || version != 1 {
		t.Fatalf("the stream row is version %d (present: %v) after one append", version, found)
	}
	if len(rows) != 1 || rows[0].version != 1 || rows[0].position <= 0 {
		t.Fatalf("the history holds %+v, so the version advance and the event row are not one unit", rows)
	}
	if string(rows[0].payload) != "credited 10" || rows[0].revision != 1 || rows[0].name != family+".held" {
		t.Errorf("the stored row is %+v and the change was a %q carrying %q", rows[0], family+".held", "credited 10")
	}
	if rows[0].recordedAt.IsZero() {
		t.Error("the stored row carries no instant, and recorded_at is the database's own clock")
	}

	t.Run("a second append at the stale token conflicts and the history does not move", func(t *testing.T) {
		if _, _, err := repo.Append(ctx, at, fact.New("A-1", held{Bytes: []byte("credited 20")})); !errors.Is(err, event.ErrConflict) {
			t.Fatalf("an append at a version the stream has left answered %v", err)
		}
		if got := payloads(stored(t, schema, stream)); len(got) != 1 {
			t.Fatalf("the stream holds %v after a refused append, so a conflict wrote something", got)
		}
	})

	t.Run("the token the append answered is the one that is admitted", func(t *testing.T) {
		if _, _, err := repo.Append(ctx, moved, fact.New("A-1", held{Bytes: []byte("credited 20")})); err != nil {
			t.Fatalf("an append at the version the first one produced answered %v", err)
		}
		if got := payloads(stored(t, schema, stream)); len(got) != 2 || got[1] != "credited 20" {
			t.Fatalf("the stream holds %v", got)
		}
	})

	t.Run("a fact that encodes to no bytes is a fact and not a missing one", func(t *testing.T) {
		empty := aStream(family, "A-empty")
		at := loaded(t, ctx, repo, "A-empty")
		if _, _, err := repo.Append(ctx, at, fact.New("A-empty", held{Bytes: nil})); err != nil {
			t.Fatalf("a fact whose codec encoded nothing answered %v, and an empty payload is a fact with nothing to carry", err)
		}
		rows := stored(t, schema, empty)
		if len(rows) != 1 || rows[0].payload == nil || len(rows[0].payload) != 0 {
			t.Fatalf("the stored row is %+v where an empty payload reads back as no bytes rather than as nothing at all", rows)
		}
	})

	t.Run("a version no bigint holds is a version no stream is at", func(t *testing.T) {
		err := prepared(t, schema).Append(ctx, event.AppendRequest{
			Stream:   stream,
			Expected: math.MaxUint64,
			Records:  []event.Record{{Type: family + ".held", Revision: 1, Payload: []byte("never written")}},
		})
		classifiedAs(t, err, event.Conflict, "an append decided at a version above what the schema holds")
		if got := len(stored(t, schema, stream)); got != 2 {
			t.Fatalf("the stream holds %d events where the two above are all there should be", got)
		}
	})

	t.Run("a store that has not verified its schema refuses before any statement", func(t *testing.T) {
		pool := countingPool(t, 2)
		unverified, err := New(Spec{DB: pool, Source: crudsql.Postgres(pool), Schema: schema})
		if err != nil {
			t.Fatalf("a store over a pool of its own was refused: %v", err)
		}
		t.Cleanup(func() { _ = unverified.Close() })
		request := event.AppendRequest{
			Stream:   aStream(family, "A-unverified"),
			Expected: 0,
			Records:  []event.Record{{Type: family + ".held", Revision: 1, Payload: []byte("never written")}},
		}
		recount(t)
		classifiedAs(t, unverified.Append(ctx, request), event.Refused, "an append through a store whose Prepare never ran")
		if execs, queries := counted(t); execs != 0 || queries != 0 {
			t.Fatalf("the refusal issued %d statements and %d queries, and nothing was tried so the caller's transaction is untouched", execs, queries)
		}
		if err := unverified.Prepare(ctx); err != nil {
			t.Fatalf("the store did not verify the deployed schema: %v", err)
		}
		if err := unverified.Append(ctx, request); err != nil {
			t.Fatalf("the same append through the same store after it verified answered %v, so the refusal above is not about readiness", err)
		}
	})

	t.Run("one append is one statement and no query at all", func(t *testing.T) {
		counting, countingFact := boundRepo(t, countingStore(t, schema, 2), "eventpg.s3.counted")
		token := loaded(t, ctx, counting, "A-2")
		recount(t)
		if _, _, err := counting.Append(ctx, token, countingFact.New("A-2", held{Bytes: []byte("counted")})); err != nil {
			t.Fatalf("the append through the counting driver answered %v", err)
		}
		execs, queries := counted(t)
		if execs != 1 || queries != 0 {
			t.Fatalf("one append reached the driver as %d statements and %d queries, and the version advance and the rows are supposed to be one", execs, queries)
		}
	})
}

func TestEightWritersAtOneVersionLeaveOneWinnerAndSevenConflicts(t *testing.T) {
	const family = "eventpg.s3.race"
	const writers = 8
	schema := sharedSchema(t)
	repo, fact := boundRepo(t, prepared(t, schema), family)
	stream := aStream(family, "A-race")
	ctx := t.Context()

	tokens := make([]event.At[held], writers)
	for index := range tokens {
		tokens[index] = loaded(t, ctx, repo, "A-race")
	}
	refused := make([]error, writers)
	start := make(chan struct{})
	var racing sync.WaitGroup
	for index := range writers {
		racing.Add(1)
		go func() {
			defer racing.Done()
			<-start
			_, _, refused[index] = repo.Append(ctx, tokens[index], fact.New("A-race", held{Bytes: []byte("writer " + strconv.Itoa(index))}))
		}()
	}
	close(start)
	racing.Wait()

	admitted := 0
	for index, err := range refused {
		if err == nil {
			admitted++
			continue
		}
		if !errors.Is(err, event.ErrConflict) {
			t.Errorf("writer %d lost the race and was told %v, and a lost race at READ COMMITTED is a conflict", index, err)
		}
		for _, wrong := range []error{event.ErrBackend, event.ErrUncertain, crud.ErrUnavailable} {
			if errors.Is(err, wrong) {
				t.Errorf("writer %d lost the race and was told %v, which reads as %v", index, err, wrong)
			}
		}
	}
	if admitted != 1 {
		t.Fatalf("%d of %d writers at one version were admitted", admitted, writers)
	}
	if got := stored(t, schema, stream); len(got) != 1 {
		t.Fatalf("the stream holds %d events after eight writers raced at version 0", len(got))
	}

	t.Run("a single writer at the fresh version wins, so the seven above were told about the version", func(t *testing.T) {
		at := loaded(t, ctx, repo, "A-race")
		if _, _, err := repo.Append(ctx, at, fact.New("A-race", held{Bytes: []byte("afterwards")})); err != nil {
			t.Fatalf("the only writer at the version the race left answered %v", err)
		}
		if got := stored(t, schema, stream); len(got) != 2 {
			t.Fatalf("the stream holds %d events after the uncontended append", len(got))
		}
	})
}

func TestABatchLandsDenseAscendingAndAtOneInstant(t *testing.T) {
	const family = "eventpg.s3.batch"
	schema := sharedSchema(t)
	store := prepared(t, schema)
	repo, fact := boundRepo(t, store, family)
	stream := aStream(family, "A-batch")
	ctx := t.Context()

	written := []string{"one", "two", "two", "three"}
	changes := make([]event.Change[held], 0, len(written))
	for _, payload := range written {
		changes = append(changes, fact.New("A-batch", held{Bytes: []byte(payload)}))
	}
	at := loaded(t, ctx, repo, "A-batch")
	_, receipt, err := repo.Append(ctx, at, changes...)
	if err != nil {
		t.Fatalf("a batch of %d at the loaded version answered %v", len(changes), err)
	}
	if receipt.First() != 1 || receipt.Last() != event.Version(len(written)) || receipt.Count() != len(written) {
		t.Errorf("the receipt names versions %d to %d over %d changes", receipt.First(), receipt.Last(), receipt.Count())
	}

	rows := stored(t, schema, stream)
	if got := payloads(rows); strings.Join(got, ",") != strings.Join(written, ",") {
		t.Fatalf("the batch was written as %v where the caller listed %v, so it was reordered or deduplicated", got, written)
	}
	for index, row := range rows {
		if row.version != int64(index)+1 {
			t.Fatalf("the batch holds versions %v, and one unit writes them dense from the version it was admitted at", rows)
		}
		if index > 0 && row.position <= rows[index-1].position {
			t.Fatalf("the batch took positions %d then %d, so the log does not hold it in the order the caller listed it", rows[index-1].position, row.position)
		}
		if !row.recordedAt.Equal(rows[0].recordedAt) {
			t.Fatalf("the batch was recorded at %v and %v, so it was assembled over two instants rather than admitted at one", rows[0].recordedAt, row.recordedAt)
		}
	}
	if version, _ := streamVersion(t, schema, stream); version != int64(len(written)) {
		t.Errorf("the stream row is at version %d after a batch of %d", version, len(written))
	}

	// The positions above ascending in the caller's order is what this run's
	// planner did, not what a VALUES scan promises, so the clause that asks for
	// it is pinned here: removing it leaves every assertion in this test green.
	t.Run("the statement asks for the order the caller listed", func(t *testing.T) {
		if !strings.Contains(store.appendStatement(len(written)), "ORDER BY record.ord") {
			t.Fatal("the append does not order its records by the ordinal the caller's list gave them, and neither a VALUES scan nor an unnest promises that order on its own")
		}
	})

	t.Run("a batch at a stale version writes none of its rows", func(t *testing.T) {
		if _, _, err := repo.Append(ctx, at, changes...); !errors.Is(err, event.ErrConflict) {
			t.Fatalf("the same batch at the version the stream has left answered %v", err)
		}
		if got := stored(t, schema, stream); len(got) != len(written) {
			t.Fatalf("the stream holds %d events after a refused batch of %d, so part of a batch was written", len(got), len(changes))
		}
	})
}

func TestTheWidestBatchTheStoreCanBuildIsAppended(t *testing.T) {
	const family = "eventpg.s3.widest"
	schema := sharedSchema(t)
	pool := liveDB(t)
	store, err := New(Spec{DB: pool, Source: crudsql.Postgres(pool), Schema: schema, MaxBatch: event.MaxBatchCount})
	if err != nil {
		t.Fatalf("a store at the kernel's own batch ceiling was refused: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Prepare(t.Context()); err != nil {
		t.Fatalf("the store did not verify the deployed schema: %v", err)
	}
	repo, fact := boundRepo(t, store, family)
	stream := aStream(family, "A-wide")
	ctx := t.Context()

	changes := make([]event.Change[held], 0, event.MaxBatchCount)
	for index := range event.MaxBatchCount {
		changes = append(changes, fact.New("A-wide", held{Bytes: []byte(strconv.Itoa(index))}))
	}
	at := loaded(t, ctx, repo, "A-wide")
	if _, receipt, err := repo.Append(ctx, at, changes...); err != nil {
		t.Fatalf("a batch of %d — the widest statement this store can build — answered %v", event.MaxBatchCount, err)
	} else if receipt.Count() != event.MaxBatchCount {
		t.Fatalf("the receipt names %d changes", receipt.Count())
	}

	rows := stored(t, schema, stream)
	if len(rows) != event.MaxBatchCount {
		t.Fatalf("the widest batch left %d rows of %d", len(rows), event.MaxBatchCount)
	}
	for index, row := range rows {
		if row.version != int64(index)+1 || string(row.payload) != strconv.Itoa(index) {
			t.Fatalf("row %d of the widest batch is %+v", index, row)
		}
	}

	t.Run("the widest statement binds the parameters PostgreSQL admits and no more", func(t *testing.T) {
		widest := store.appendStatement(event.MaxBatchCount)
		last := streamArguments + recordArguments*event.MaxBatchCount
		if last != 3076 {
			t.Fatalf("the kernel's ceiling of %d records needs %d parameters where the measurement against PostgreSQL's 65535 was made at 3076", event.MaxBatchCount, last)
		}
		if !strings.Contains(widest, "($3074, $3075, $3076, 1024)") {
			t.Fatal("the widest statement does not end at the parameters and the ordinal the last record needs")
		}
		if strings.Contains(widest, "$"+strconv.Itoa(last+1)) {
			t.Fatalf("the widest statement binds $%d, which is one more than %d records need", last+1, event.MaxBatchCount)
		}
	})
}
