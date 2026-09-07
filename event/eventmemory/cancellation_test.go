package eventmemory_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/frostgrove/vv/event"
	"github.com/frostgrove/vv/event/eventmemory"
)

func TestACancellationTravelsAsItselfFromEveryDoorTheStoreOperates(t *testing.T) {
	background := context.Background()
	_, store := openStore(t)
	stream := streamOf("shipping.shipment", "acme/A-17")
	appendTo(t, background, store, stream, 0, "committed")

	cancelled, cancel := context.WithCancel(background)
	cancel()
	expired, stopExpiring := context.WithDeadline(background, time.Now().Add(-time.Minute))
	defer stopExpiring()

	doors := func(ctx context.Context) []struct {
		doing string
		door  func() error
	} {
		return []struct {
			doing string
			door  func() error
		}{
			{"appending", func() error {
				return store.Append(ctx, event.AppendRequest{Stream: stream, Expected: 1, Records: records("credited 10")})
			}},
			{"reading a stream", func() error { _, err := store.ReadStream(ctx, stream, 0); return err }},
			{"reading the log", func() error { _, _, err := store.ReadAll(ctx, ""); return err }},
			{"asking whether the store is ready", func() error { return store.Check(ctx) }},
			{"beginning a transaction", func() error { _, err := store.Begin(ctx); return err }},
		}
	}

	for _, window := range []struct {
		what     string
		ctx      context.Context
		sentinel error
	}{
		{"a context the caller cancelled", cancelled, context.Canceled},
		{"a context whose deadline has passed", expired, context.DeadlineExceeded},
	} {
		t.Run("every door answers "+window.what+" as itself", func(t *testing.T) {
			for _, entry := range doors(window.ctx) {
				err := entry.door()
				if !errors.Is(err, window.sentinel) {
					t.Fatalf("%s under %s answered %v, which a caller matching a cancellation cannot recognise as one", entry.doing, window.what, err)
				}
				for _, outcome := range []event.Outcome{
					event.Unclassified, event.Conflict, event.NotWritten,
					event.Unconfirmed, event.Closed, event.BadCursor, event.Refused,
				} {
					if err.Error() == event.Failure(outcome, nil).Error() {
						t.Fatalf("%s under %s answered a failure the store classified %v, and a write this store never issued is neither uncertain nor anything else it could have classified",
							entry.doing, window.what, outcome)
					}
				}
			}
		})
	}

	t.Run("nothing the cancelled appends carried was written", func(t *testing.T) {
		if got := payloadsOf(drainStream(t, background, store, stream)); !slices.Equal(got, []string{"committed"}) {
			t.Fatalf("the stream holds %v after two appends under a context that was already over", got)
		}
	})

	t.Run("the same doors answer on a live context, so the refusals above are the cancellation's", func(t *testing.T) {
		for _, entry := range doors(background) {
			if err := entry.door(); err != nil {
				t.Fatalf("%s under a live context answered %v", entry.doing, err)
			}
		}
		if got := len(drainStream(t, background, store, stream)); got != 2 {
			t.Fatalf("the stream holds %d events after the same append was issued on a live context", got)
		}
	})

	t.Run("finishing a transaction reads no deadline, so a cancelled request never leaves a claim behind", func(t *testing.T) {
		inside, tx := begin(t, background, store)
		appendTo(t, inside, store, stream, 2, "staged")

		refusal := store.Append(eventmemory.WithTransaction(cancelled, tx), event.AppendRequest{
			Stream:   stream,
			Expected: 3,
			Records:  records("never written"),
		})
		if !errors.Is(refusal, context.Canceled) {
			t.Fatalf("an append inside a live transaction on a cancelled context answered %v", refusal)
		}
		if err := tx.Commit(cancelled); err != nil {
			t.Fatalf("committing under the cancelled context answered %v, and a transaction that refuses to finish holds its claims and starves every other writer of its streams", err)
		}
		if got := payloadsOf(drainStream(t, background, store, stream)); got[2] != "staged" {
			t.Fatalf("the stream holds %v after a commit under a cancelled context", got)
		}
	})
}
