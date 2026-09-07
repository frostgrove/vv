package eventmemory_test

import (
	"context"
	"testing"

	"github.com/frostgrove/vv/event"
	"github.com/frostgrove/vv/event/eventmemory"
)

func TestTheStoreDeclaresWhatItCanDoAndSaysWhatItCannot(t *testing.T) {
	ctx := context.Background()
	log, store := openStore(t)
	stream := streamOf("accounts.account", "acme/A-17")
	claimed := store.Capabilities()

	t.Run("every capability is stated, so nothing this store does is refused for want of a claim", func(t *testing.T) {
		for _, capability := range []struct {
			what    string
			support event.Support
		}{
			{"transactions", claimed.Transactions},
			{"persistence", claimed.Persistence},
			{"monotone visibility", claimed.MonotoneVisibility},
			{"shared backing", claimed.SharedBacking},
		} {
			if capability.support == event.Unstated {
				t.Fatalf("the store leaves %s unstated, and what a store does not state is refused at both doors rather than tried", capability.what)
			}
		}
	})

	t.Run("the transactions it claims are transactions", func(t *testing.T) {
		if claimed.Transactions != event.Supported {
			t.Fatalf("the store answers %v for transactions, and it stages, claims and re-validates like one that supports them", claimed.Transactions)
		}
		inside, tx := begin(t, ctx, store)
		appendTo(t, inside, store, stream, 0, "staged")
		if page := readStream(t, ctx, store, stream, 0); len(page) != 0 {
			t.Fatalf("a store claiming transactions published %v before the transaction that staged it committed", payloadsOf(page))
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("committing answered %v", err)
		}
		if got := payloadsOf(readStream(t, ctx, store, stream, 0)); len(got) != 1 || got[0] != "staged" {
			t.Fatalf("after the commit the stream holds %v, so what was staged went nowhere", got)
		}
	})

	t.Run("the backing it shares is the log's, so a second store value over it is the same store", func(t *testing.T) {
		if claimed.SharedBacking != event.Supported {
			t.Fatalf("the store answers %v for a shared backing while two values over one log read each other's writes", claimed.SharedBacking)
		}
		second := newStore(t, eventmemory.Spec{Log: log, MaxBatch: 8})
		if !store.Backing().Equal(second.Backing()) {
			t.Fatalf("two store values over one log answer different backings while the store claims a shared one")
		}
	})

	t.Run("the persistence it does not claim is the one thing it cannot do", func(t *testing.T) {
		if claimed.Persistence != event.Unsupported {
			t.Fatalf("the store answers %v for persistence, and a history held in a map outlives nothing", claimed.Persistence)
		}
		_, apart := openStore(t)
		if page := readStream(t, ctx, apart, stream, 0); len(page) != 0 {
			t.Fatalf("a store over a fresh log read %v, so the history is somewhere other than the log the caller holds and outlives it", payloadsOf(page))
		}
	})

	t.Run("the monotone visibility it claims is what no reader can ever be shown twice", func(t *testing.T) {
		if claimed.MonotoneVisibility != event.Supported {
			t.Fatalf("the store answers %v for monotone visibility while it assigns every position inside the section that publishes", claimed.MonotoneVisibility)
		}
		other := streamOf("accounts.account", "acme/A-18")
		appendTo(t, ctx, store, other, 0, "before the rollback")
		inside, tx := begin(t, ctx, store)
		appendTo(t, inside, store, other, 1, "rolled back")
		if err := tx.Rollback(ctx); err != nil {
			t.Fatalf("rolling back answered %v", err)
		}
		appendTo(t, ctx, store, other, 1, "after the rollback")

		var highest event.Position
		for _, envelope := range drainLog(t, ctx, store) {
			if envelope.Position <= highest {
				t.Fatalf("the log hands out position %d after it had already handed out %d, so a consumer that checkpointed the higher one loses the lower one",
					envelope.Position, highest)
			}
			highest = envelope.Position
		}
		if highest == 0 {
			t.Fatalf("the log hands out no position at all, so the comparison above passed over nothing")
		}
	})

	t.Run("the three constant answers are the same on every call and after everything that could change them", func(t *testing.T) {
		capabilities, limits, backing := store.Capabilities(), store.Limits(), store.Backing()
		if limits.MaxPayload != 64<<10 || !backing.Equal(store.Backing()) {
			t.Fatalf("the store answers limits %+v and a backing of %v, so the comparisons below are over empty values", limits, backing)
		}
		if err := store.Close(); err != nil {
			t.Fatalf("closing answered %v", err)
		}
		if store.Capabilities() != capabilities {
			t.Fatalf("a closed store claims %+v where it claimed %+v while open, so a binding that read the claim once is holding something that is no longer true",
				store.Capabilities(), capabilities)
		}
		if store.Limits() != limits {
			t.Fatalf("a closed store publishes %+v where it published %+v while open", store.Limits(), limits)
		}
		if !backing.Equal(store.Backing()) {
			t.Fatalf("a closed store answers a different backing, so every token minted through it is refused for the wrong reason")
		}
	})
}
