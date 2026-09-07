package eventmemory_test

import (
	"context"
	"fmt"
	"math"
	"slices"
	"testing"
	"time"

	"github.com/frostgrove/vv/event"
	"github.com/frostgrove/vv/event/eventmemory"
)

var decided = time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)

func TestAnAppendIsAdmittedOnlyAtTheVersionItWasDecidedAt(t *testing.T) {
	ctx := context.Background()
	log := newLog(t, eventmemory.LogSpec{})
	store := newStore(t, eventmemory.Spec{Log: log, Clock: ticking(decided, time.Second)})
	stream := streamOf("accounts.account", "acme/A-17")
	appendTo(t, ctx, store, stream, 0, "credited 10")

	t.Run("no version but the observed one is admitted", func(t *testing.T) {
		for _, expected := range []event.Version{0, 2, 3, math.MaxUint64} {
			err := store.Append(ctx, event.AppendRequest{Stream: stream, Expected: expected, Records: records("never written")})
			classifiedAs(t, err, event.Conflict, fmt.Sprintf("an append at version %d against a stream at version 1", expected))
		}
		if got := payloadsOf(drainStream(t, ctx, store, stream)); !slices.Equal(got, []string{"credited 10"}) {
			t.Fatalf("the stream holds %v after four appends at versions it was not at", got)
		}
	})

	t.Run("the observed version is admitted, so the refusals above are about the version", func(t *testing.T) {
		appendTo(t, ctx, store, stream, 1, "credited 20")
		if got := payloadsOf(drainStream(t, ctx, store, stream)); !slices.Equal(got, []string{"credited 10", "credited 20"}) {
			t.Fatalf("the stream holds %v after an append at the version it was at", got)
		}
	})

	t.Run("a refused batch leaves none of its records behind", func(t *testing.T) {
		batch := records("one", "two", "three")
		err := store.Append(ctx, event.AppendRequest{Stream: stream, Expected: 1, Records: batch})
		classifiedAs(t, err, event.Conflict, "a batch of three at a version the stream has left")
		if got := len(drainStream(t, ctx, store, stream)); got != 2 {
			t.Fatalf("the stream holds %d events after a refused batch of three, so part of a batch was written", got)
		}

		if err := store.Append(ctx, event.AppendRequest{Stream: stream, Expected: 2, Records: batch}); err != nil {
			t.Fatalf("the same batch at the version the stream is at was refused: %v", err)
		}
		page := drainStream(t, ctx, store, stream)
		if got := versionsOf(page); !slices.Equal(got, []event.Version{1, 2, 3, 4, 5}) {
			t.Fatalf("the stream holds versions %v, which a batch admitted as one unit cannot produce", got)
		}
		recorded := page[2].RecordedAt
		for _, envelope := range page[2:] {
			if !envelope.RecordedAt.Equal(recorded) {
				t.Fatalf("the three records of one batch were recorded at %v, %v and %v, so the batch was assembled over three instants rather than admitted at one",
					page[2].RecordedAt, page[3].RecordedAt, page[4].RecordedAt)
			}
		}
		positions := positionsOf(drainLog(t, ctx, store))
		if got := positions[2:]; !slices.Equal(got, []event.Position{positions[1] + 1, positions[1] + 2, positions[1] + 3}) {
			t.Fatalf("the batch took positions %v after the event at %d, so one unit was split across the log", got, positions[1])
		}
	})

	t.Run("the caller decides again and nothing retried the list that was refused", func(t *testing.T) {
		refused := records("refused a", "refused b")
		err := store.Append(ctx, event.AppendRequest{Stream: stream, Expected: 1, Records: refused})
		classifiedAs(t, err, event.Conflict, "an append the caller decided at a version the stream has left")

		at := event.Version(len(drainStream(t, ctx, store, stream)))
		appendTo(t, ctx, store, stream, at, "decided again")

		got := payloadsOf(drainStream(t, ctx, store, stream))
		if slices.Contains(got, "refused a") || slices.Contains(got, "refused b") {
			t.Fatalf("the stream holds %v, so a layer between the caller and the log re-issued the list the store had refused", got)
		}
		if got[len(got)-1] != "decided again" {
			t.Fatalf("the stream ends with %q rather than the decision the caller made after the conflict", got[len(got)-1])
		}
	})
}

func TestAClaimCoversOnlyTheStreamsATransactionWroteTo(t *testing.T) {
	ctx := context.Background()
	_, store := openStore(t)
	held, apart := streamOf("accounts.account", "acme/held"), streamOf("accounts.account", "acme/apart")

	inside, tx := begin(t, ctx, store)
	appendTo(t, inside, store, held, 0, "staged")

	t.Run("an append outside the transaction is refused on the stream it holds and admitted on every other", func(t *testing.T) {
		err := store.Append(ctx, event.AppendRequest{Stream: held, Expected: 0, Records: records("from outside")})
		classifiedAs(t, err, event.Conflict, "an autocommit append to a stream a live transaction holds")
		appendTo(t, ctx, store, apart, 0, "from outside")
	})

	t.Run("a second transaction writes to a stream the first never touched", func(t *testing.T) {
		second, other := begin(t, ctx, store)
		appendTo(t, second, store, streamOf("accounts.account", "acme/second"), 0, "the second transaction")
		if err := other.Commit(ctx); err != nil {
			t.Fatalf("committing a transaction that touched no stream of the first answered %v", err)
		}
	})

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("committing answered %v", err)
	}

	t.Run("the claim goes when the transaction finishes", func(t *testing.T) {
		appendTo(t, ctx, store, held, 1, "after the commit")
		if got := payloadsOf(drainStream(t, ctx, store, held)); !slices.Equal(got, []string{"staged", "after the commit"}) {
			t.Fatalf("the stream holds %v, so what the transaction claimed or committed is not what it published", got)
		}
	})

	t.Run("a rollback releases what it claimed as well", func(t *testing.T) {
		burned := streamOf("accounts.account", "acme/burned")
		rolling, discarded := begin(t, ctx, store)
		appendTo(t, rolling, store, burned, 0, "rolled back")
		if err := discarded.Rollback(ctx); err != nil {
			t.Fatalf("rolling back answered %v", err)
		}
		appendTo(t, ctx, store, burned, 0, "after the rollback")
		if got := payloadsOf(drainStream(t, ctx, store, burned)); !slices.Equal(got, []string{"after the rollback"}) {
			t.Fatalf("the stream a rolled-back transaction had claimed holds %v", got)
		}
	})
}

func TestVersionsAreDenseAndPositionsAscendWhateverTheClockSays(t *testing.T) {
	ctx := context.Background()
	log := newLog(t, eventmemory.LogSpec{})
	store := newStore(t, eventmemory.Spec{Log: log, Clock: ticking(decided, -time.Second)})
	first, second, third := streamOf("accounts.account", "acme/first"), streamOf("accounts.account", "acme/second"), streamOf("accounts.account", "acme/third")

	appendTo(t, ctx, store, first, 0, "one")
	appendTo(t, ctx, store, second, 0, "one", "two")
	appendTo(t, ctx, store, first, 1, "two")
	inside, tx := begin(t, ctx, store)
	appendTo(t, inside, store, third, 0, "staged and discarded")
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("rolling back answered %v", err)
	}
	appendTo(t, ctx, store, third, 0, "one")
	appendTo(t, ctx, store, second, 2, "three")

	global := drainLog(t, ctx, store)

	t.Run("every stream is dense from one and every position is higher than the last", func(t *testing.T) {
		for _, stream := range []event.Stream{first, second, third} {
			page := drainStream(t, ctx, store, stream)
			for index, envelope := range page {
				if envelope.Version != event.Version(index)+1 {
					t.Fatalf("%v holds versions %v, and a fold that counts from one reads a different history than the store holds",
						stream, versionsOf(page))
				}
			}
		}
		var highest event.Position
		for _, envelope := range global {
			if envelope.Position <= highest {
				t.Fatalf("the log holds positions %v, so a position was reused or reassigned", positionsOf(global))
			}
			highest = envelope.Position
		}
	})

	t.Run("one stream's events ascend in the log in the order that stream holds them", func(t *testing.T) {
		for _, stream := range []event.Stream{first, second, third} {
			var last event.Version
			for _, envelope := range global {
				if envelope.Stream != stream {
					continue
				}
				if envelope.Version != last+1 {
					t.Fatalf("%v appears in the log at version %d after version %d, so the log reorders one stream", stream, envelope.Version, last)
				}
				last = envelope.Version
			}
			if last == 0 {
				t.Fatalf("%v appears in the log not at all, so the ordering above was asserted over nothing", stream)
			}
		}
	})

	t.Run("the instants descend while the positions ascend, so nothing ordered by the clock", func(t *testing.T) {
		if !global[0].RecordedAt.Equal(decided) {
			t.Fatalf("the first event was recorded at %v rather than at the instant the store's own clock answered, so the clock a test injects is not the one the store reads",
				global[0].RecordedAt)
		}
		descending := 0
		for index := 1; index < len(global); index++ {
			if global[index].RecordedAt.Before(global[index-1].RecordedAt) {
				descending++
			}
		}
		if descending == 0 {
			t.Fatalf("no event in the log was recorded before the one at the position under it, so a backwards clock reached the history nowhere and the ordering assertions above prove nothing about it")
		}
	})

	t.Run("a store built with no clock records the instant its append ran at", func(t *testing.T) {
		_, byDefault := openStore(t)
		stream := streamOf("accounts.account", "acme/A-19")
		before := time.Now()
		appendTo(t, ctx, byDefault, stream, 0, "credited 10")
		after := time.Now()

		recorded := drainStream(t, ctx, byDefault, stream)[0].RecordedAt
		if recorded.Before(before) || recorded.After(after) {
			t.Fatalf("a store constructed with no clock recorded an event at %v where the append ran between %v and %v, so every event a consumer appends through the default carries an instant that is not when anything happened",
				recorded, before, after)
		}
	})
}
