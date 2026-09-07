package eventmemory_test

import (
	"context"
	"slices"
	"strconv"
	"testing"

	"github.com/frostgrove/vv/event"
)

func kindsOf(page []event.Envelope) []string {
	held := make([]string, 0, len(page))
	for _, envelope := range page {
		held = append(held, envelope.Type+"@"+strconv.Itoa(envelope.Revision))
	}
	return held
}

func kindsIn(written []event.Record) []string {
	held := make([]string, 0, len(written))
	for _, record := range written {
		held = append(held, record.Type+"@"+strconv.Itoa(record.Revision))
	}
	return held
}

func distinct(held []string) int {
	seen := map[string]bool{}
	for _, one := range held {
		seen[one] = true
	}
	return len(seen)
}

func TestAnEnvelopeCarriesTheTypeAndRevisionOfTheRecordItWasWrittenFrom(t *testing.T) {
	ctx := context.Background()
	_, store := openStore(t)
	stream := streamOf("accounts.account", "acme/A-17")
	committed := []event.Record{
		{Type: "accounts.credited", Revision: 1, Payload: []byte("credited 10")},
		{Type: "accounts.debited", Revision: 2, Payload: []byte("debited 4")},
		{Type: "accounts.frozen", Revision: 7, Payload: []byte("frozen after a dispute")},
	}
	staged := []event.Record{
		{Type: "accounts.thawed", Revision: 3, Payload: []byte("thawed")},
		{Type: "accounts.credited", Revision: 2, Payload: []byte("credited 20")},
	}
	written := append(kindsIn(committed), kindsIn(staged)...)

	t.Run("the records this test drives are of five kinds, so nothing below is satisfied by a constant", func(t *testing.T) {
		if distinct(written) != len(written) {
			t.Fatalf("the records appended here are %v, so a store answering one type at one revision passes every assertion below", written)
		}
	})

	if err := store.Append(ctx, event.AppendRequest{Stream: stream, Records: committed}); err != nil {
		t.Fatalf("appending three records of three kinds was refused: %v", err)
	}
	inside, tx := begin(t, ctx, store)
	if err := store.Append(inside, event.AppendRequest{Stream: stream, Expected: 3, Records: staged}); err != nil {
		t.Fatalf("appending two more kinds inside a transaction was refused: %v", err)
	}

	t.Run("a committed read answers the kind each record was written under", func(t *testing.T) {
		if got := kindsOf(readStream(t, ctx, store, stream, 0)); !slices.Equal(got, kindsIn(committed)) {
			t.Fatalf("the stream reads as %v where %v was written, so every fold of it looks up the wrong fact and the wrong decoder for a history that is intact",
				got, kindsIn(committed))
		}
	})

	t.Run("a read inside the transaction answers the kinds that transaction staged", func(t *testing.T) {
		if got := kindsOf(readStream(t, inside, store, stream, 3)); !slices.Equal(got, kindsIn(staged)) {
			t.Fatalf("the staged records read as %v where %v was staged, so an operation that appends and reloads folds something else than it decided", got, kindsIn(staged))
		}
	})

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("committing answered %v", err)
	}

	t.Run("the log answers the same kinds the two reads did", func(t *testing.T) {
		if got := kindsOf(drainLog(t, ctx, store)); !slices.Equal(got, written) {
			t.Fatalf("the log reads as %v where %v was written", got, written)
		}
	})
}

func TestTwoFamiliesSharingOneKeyAreTwoStreams(t *testing.T) {
	ctx := context.Background()
	_, store := openStore(t)
	const shared = "acme/A-17"
	account, order := streamOf("accounts.account", shared), streamOf("orders.order", shared)

	appendTo(t, ctx, store, account, 0, "credited 10")
	if err := store.Append(ctx, event.AppendRequest{Stream: order, Records: records("placed")}); err != nil {
		t.Fatalf("creating an order at the key an account already uses was refused with %v, so a key names one stream whatever family it was written under and two aggregates share one history and one version counter",
			err)
	}
	appendTo(t, ctx, store, account, 1, "credited 20")

	t.Run("each family counts its own versions from one and reads back its own events", func(t *testing.T) {
		held := drainStream(t, ctx, store, account)
		if got := payloadsOf(held); !slices.Equal(got, []string{"credited 10", "credited 20"}) {
			t.Fatalf("the account at %s reads %v, so two families sharing a key are one history and a fold replays another aggregate's events", shared, got)
		}
		if got := versionsOf(held); !slices.Equal(got, []event.Version{1, 2}) {
			t.Fatalf("the account at %s holds versions %v", shared, got)
		}
		beside := drainStream(t, ctx, store, order)
		if got := payloadsOf(beside); !slices.Equal(got, []string{"placed"}) {
			t.Fatalf("the order at %s reads %v", shared, got)
		}
		if got := versionsOf(beside); !slices.Equal(got, []event.Version{1}) {
			t.Fatalf("the order at %s holds versions %v, so it counts from where another family left off and every second append conflicts", shared, got)
		}
	})

	t.Run("a claim on one family's stream leaves the other family's alone", func(t *testing.T) {
		inside, tx := begin(t, ctx, store)
		appendTo(t, inside, store, account, 2, "credited 30")

		appendTo(t, ctx, store, order, 1, "shipped")
		err := store.Append(ctx, event.AppendRequest{Stream: account, Expected: 2, Records: records("from outside")})
		classifiedAs(t, err, event.Conflict, "an append to the stream the live transaction holds")

		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("committing answered %v", err)
		}
	})

	t.Run("the log holds both, each envelope under the family it was written to", func(t *testing.T) {
		of := map[event.Stream][]string{}
		for _, envelope := range drainLog(t, ctx, store) {
			of[envelope.Stream] = append(of[envelope.Stream], string(envelope.Payload))
		}
		if got := of[account]; !slices.Equal(got, []string{"credited 10", "credited 20", "credited 30"}) {
			t.Fatalf("the log holds %v under the account family at %s", got, shared)
		}
		if got := of[order]; !slices.Equal(got, []string{"placed", "shipped"}) {
			t.Fatalf("the log holds %v under the order family at %s", got, shared)
		}
		if len(of) != 2 {
			t.Fatalf("the log holds %d streams where two families were written at one key", len(of))
		}
	})
}
