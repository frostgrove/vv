//go:build integration

package eventpg

import (
	"context"
	"database/sql"
	"flag"
	"math"
	"os"
	"testing"
	"time"

	"github.com/frostgrove/vv/crud/adapter/crudsql"
	"github.com/frostgrove/vv/event"
)

// The size a caller measures at. The default is small enough that the benchmark
// is worth running once beside the suite and large enough that a round trip is
// not the measurement; a deployment deciding whether it needs a snapshot passes
// its own aggregate's length.
var replayEvents = flag.Int("eventpg.replay.events", 10000, "how many events the replay benchmark's stream holds")

const replayBatch = 64

// A scratch schema with one stream of exactly this many events in it, and a
// repository bound to fold them. The fold and the codec both hand back what they
// were given, so what this measures is the store's read, the envelope
// construction and the fold's own loop — not an application's state machine.
func replayStand(tb testing.TB, size int) (*event.Repo[held, string], string) {
	tb.Helper()
	const family = "eventpg.replay"
	ctx := context.Background()

	pool, err := sql.Open("pgx", os.Getenv(testDSN))
	if err != nil {
		tb.Fatalf("a pool of this measurement's own could not be opened: %v", err)
	}
	tb.Cleanup(func() { _ = pool.Close() })
	pool.SetMaxOpenConns(4)

	schema := Schema{Name: scratchName()}
	tb.Cleanup(func() {
		if _, err := pool.ExecContext(ctx, "DROP SCHEMA IF EXISTS "+quoteIdentifier(schema.Name)+" CASCADE"); err != nil {
			tb.Errorf("the scratch schema %q could not be dropped: %v", schema.Name, err)
		}
	})
	store, err := New(Spec{DB: pool, Source: crudsql.Postgres(pool), Schema: schema, SchemaManagement: ManageSchema})
	if err != nil {
		tb.Fatalf("a store over %+v was refused: %v", schema, err)
	}
	tb.Cleanup(func() { _ = store.Close() })
	if err := store.Prepare(ctx); err != nil {
		tb.Fatalf("a store over %q did not prepare it: %v", schema.Name, err)
	}

	stream := aStream(family, "replayed")
	payload := make([]byte, 120)
	for at := 0; at < size; at += replayBatch {
		records := make([]event.Record, 0, replayBatch)
		for range min(replayBatch, size-at) {
			records = append(records, event.Record{Type: family + ".held", Revision: 1, Payload: payload})
		}
		if err := store.Append(ctx, event.AppendRequest{Stream: stream, Expected: event.Version(at), Records: records}); err != nil {
			tb.Fatalf("writing the stream this measurement replays answered %v at version %d: %v", err, at, err)
		}
	}

	aggregate, _ := declareHolding(tb, family)
	repo, err := event.Bind(event.Open(store), aggregate)
	if err != nil {
		tb.Fatalf("a repository over the store could not be bound: %v", err)
	}
	return repo, "replayed"
}

// UC-125: the instrument a deployment measures its own replay cost with, so the
// snapshot decision is made against its own hardware, its own payloads and its
// own aggregate lengths rather than against a table in a document. It reports
// ns/event beside the ns/op the toolchain prints, because the number that
// decides a snapshot is per event and the stream's length is the caller's.
//
// It does not skip when the DSN is unset. TestMain fails the whole binary there,
// which is what keeps a benchmark that measured nothing from printing ok.
func BenchmarkStreamReplay(b *testing.B) {
	size := *replayEvents
	repo, id := replayStand(b, size)
	ctx := context.Background()
	if _, _, err := repo.Load(ctx, id); err != nil {
		b.Fatalf("the warm-up replay answered %v", err)
	}

	b.ResetTimer()
	for b.Loop() {
		if _, _, err := repo.Load(ctx, id); err != nil {
			b.Fatalf("a replay answered %v", err)
		}
	}
	b.StopTimer()
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*size), "ns/event")
}

// The fastest of three, because what a deployment wants from this is the cost of
// the replay rather than the cost of whatever else was running.
func measureReplay(t *testing.T, size int) time.Duration {
	t.Helper()
	repo, id := replayStand(t, size)
	ctx := context.WithoutCancel(t.Context())
	best := time.Duration(math.MaxInt64)
	for range 4 {
		started := time.Now()
		if _, _, err := repo.Load(ctx, id); err != nil {
			t.Fatalf("replaying a stream of %d answered %v", size, err)
		}
		best = min(best, time.Since(started))
	}
	return best
}

// UC-125's control, and it is the one that makes the benchmark an instrument
// rather than a number: two streams two orders of magnitude apart must cost
// times an order of magnitude apart, and the per-event cost must stay in the
// same neighbourhood. A benchmark that measured a constant — a Load that stopped
// at the first page, a fold that never ran — answers the same time for both and
// fails here.
func TestTheReplayBenchmarkMeasuresTwoOrdersApart(t *testing.T) {
	const small, large = 1_000, 100_000

	brief := measureReplay(t, small)
	long := measureReplay(t, large)

	if long < 10*brief {
		t.Fatalf("replaying %d events took %s and replaying %d took %s, which is less than an order of magnitude apart over a hundredfold stream: this benchmark is not measuring the replay",
			small, brief, large, long)
	}
	perEvent := func(taken time.Duration, size int) float64 {
		return float64(taken.Nanoseconds()) / float64(size)
	}
	briefly, lengthily := perEvent(brief, small), perEvent(long, large)
	if lengthily > 4*briefly || briefly > 4*lengthily {
		t.Fatalf("a replay costs %.0f ns an event at %d and %.0f ns an event at %d, and a full replay whose per-event cost moves by more than a factor of four over a hundredfold stream is not the linear walk this number is reported as",
			briefly, small, lengthily, large)
	}
	t.Logf("full replay: %d events in %s (%.0f ns/event), %d events in %s (%.0f ns/event)", small, brief, briefly, large, long, lengthily)
}
