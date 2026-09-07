package eventmemory_test

import (
	"bytes"
	"context"
	"slices"
	"testing"

	"github.com/frostgrove/vv/event"
)

func TestNothingACallerHandsToAnAppendIsRetainedOrRewritten(t *testing.T) {
	ctx := context.Background()
	_, store := openStore(t)
	stream := streamOf("billing.invoice", "acme/A-17")

	mine := records("credited 10", "credited 20")
	handed := [][]byte{bytes.Clone(mine[0].Payload), bytes.Clone(mine[1].Payload)}
	if err := store.Append(ctx, event.AppendRequest{Stream: stream, Expected: 0, Records: mine}); err != nil {
		t.Fatalf("appending two records was refused: %v", err)
	}

	t.Run("the store recorded what it was handed, so the assertions below are over something", func(t *testing.T) {
		if got := payloadsOf(drainStream(t, ctx, store, stream)); !slices.Equal(got, []string{"credited 10", "credited 20"}) {
			t.Fatalf("the stream holds %v after two records were appended", got)
		}
	})

	t.Run("the store wrote into nothing the caller still holds", func(t *testing.T) {
		for index, record := range mine {
			if !bytes.Equal(record.Payload, handed[index]) {
				t.Fatalf("the caller's own payload reads %q after the append and it handed over %q, so a fact that was already decided was rewritten under it",
					record.Payload, handed[index])
			}
		}
	})

	t.Run("overwriting those arrays afterwards leaves the history alone", func(t *testing.T) {
		for _, record := range mine {
			for index := range record.Payload {
				record.Payload[index] = 'z'
			}
		}
		if got := payloadsOf(drainStream(t, ctx, store, stream)); !slices.Equal(got, []string{"credited 10", "credited 20"}) {
			t.Fatalf("after the caller wrote into the arrays it had appended, the stream reads %v", got)
		}
	})

	t.Run("the records slice is the caller's to reuse for the next append", func(t *testing.T) {
		mine[0].Payload = []byte("credited 30")
		if err := store.Append(ctx, event.AppendRequest{Stream: stream, Expected: 2, Records: mine[:1]}); err != nil {
			t.Fatalf("appending through the same slice was refused: %v", err)
		}
		if got := payloadsOf(drainStream(t, ctx, store, stream)); !slices.Equal(got, []string{"credited 10", "credited 20", "credited 30"}) {
			t.Fatalf("the stream holds %v after the caller rewrote and reused the slice it had already appended", got)
		}
	})

	t.Run("a staged append keeps the same distance from the caller's memory", func(t *testing.T) {
		staged := records("credited 40")
		inside, tx := begin(t, ctx, store)
		if err := store.Append(inside, event.AppendRequest{Stream: stream, Expected: 3, Records: staged}); err != nil {
			t.Fatalf("appending inside a transaction was refused: %v", err)
		}
		for index := range staged[0].Payload {
			staged[0].Payload[index] = 'z'
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("committing answered %v", err)
		}
		if got := payloadsOf(drainStream(t, ctx, store, stream)); got[3] != "credited 40" {
			t.Fatalf("a record staged in a transaction reads %q after the caller wrote into the array it staged", got[3])
		}
	})
}
