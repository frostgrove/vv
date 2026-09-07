package eventmemory_test

import (
	"context"
	"slices"
	"testing"

	"github.com/frostgrove/vv/event"
	"github.com/frostgrove/vv/event/eventmemory"
)

func TestAPageIsTheCallersIncludingItsCapacity(t *testing.T) {
	ctx := context.Background()
	log := newLog(t, eventmemory.LogSpec{})
	store := newStore(t, eventmemory.Spec{Log: log, StreamPage: 2, MaxRead: 2})
	stream := streamOf("catalog.product", "acme/A-17")
	written := []string{"credited 10", "credited 20", "credited 30"}
	appendTo(t, ctx, store, stream, 0, written...)

	t.Run("the page holds what was written, so the assertions below are over something", func(t *testing.T) {
		if got := payloadsOf(readStream(t, ctx, store, stream, 0)); !slices.Equal(got, written[:2]) {
			t.Fatalf("the first page holds %v rather than the first two events written", got)
		}
	})

	t.Run("appending to one payload leaves its neighbour alone", func(t *testing.T) {
		page := readStream(t, ctx, store, stream, 0)
		page[0].Payload = append(page[0].Payload, '!')
		if got := string(page[1].Payload); got != written[1] {
			t.Fatalf("appending one byte to the first payload of a page turned the second into %q, so the two are slices of one buffer", got)
		}
	})

	t.Run("writing into every payload leaves the history alone", func(t *testing.T) {
		page := readStream(t, ctx, store, stream, 0)
		for _, envelope := range page {
			for index := range envelope.Payload {
				envelope.Payload[index] = 'z'
			}
		}
		if got := payloadsOf(readStream(t, ctx, store, stream, 0)); !slices.Equal(got, written[:2]) {
			t.Fatalf("after a reader wrote into the page it was handed, the stream reads %v", got)
		}
	})

	t.Run("appending to a page leaves the next page and the held one alone", func(t *testing.T) {
		page := readStream(t, ctx, store, stream, 0)
		held := payloadsOf(page)
		_ = append(page, event.Envelope{})

		next := readStream(t, ctx, store, stream, 2)
		if got := payloadsOf(next); len(got) != 1 || got[0] != written[2] {
			t.Fatalf("after a reader appended to the first page, the second page reads %v", got)
		}
		if got := payloadsOf(page); !slices.Equal(got, held) {
			t.Fatalf("the page a reader was still holding became %v when the next one was read", got)
		}
	})

	t.Run("the same holds for the global read", func(t *testing.T) {
		page, next := readAll(t, ctx, store, "")
		page[0].Payload = append(page[0].Payload, '!')
		_ = append(page, event.Envelope{})
		held := payloadsOf(page)

		following, _ := readAll(t, ctx, store, next)
		if got := payloadsOf(following); len(got) != 1 || got[0] != written[2] {
			t.Fatalf("after a reader wrote into and appended to the first page, resuming the log read %v", got)
		}
		if got := payloadsOf(page); !slices.Equal(got, held) {
			t.Fatalf("the global page a reader was still holding became %v when the read resumed", got)
		}
	})
}

func TestAPageOfStagedRecordsIsTheCallersToo(t *testing.T) {
	ctx := context.Background()
	_, store := openStore(t)
	stream := streamOf("catalog.product", "acme/A-17")
	written := []string{"committed", "staged", "staged too"}
	appendTo(t, ctx, store, stream, 0, written[0])
	inside, tx := begin(t, ctx, store)
	appendTo(t, inside, store, stream, 1, written[1], written[2])

	page := readStream(t, inside, store, stream, 0)
	if got := payloadsOf(page); !slices.Equal(got, written) {
		t.Fatalf("the page read inside the transaction holds %v, so nothing below is over a page holding a committed record beside a staged one", got)
	}
	for _, envelope := range page {
		for index := range envelope.Payload {
			envelope.Payload[index] = 'z'
		}
	}
	_ = append(page, event.Envelope{})

	t.Run("reading again inside the same transaction answers what was staged", func(t *testing.T) {
		if got := payloadsOf(readStream(t, inside, store, stream, 0)); !slices.Equal(got, written) {
			t.Fatalf("after a reader wrote into the page it was handed inside a transaction, that transaction reads %v — so the record it is about to commit is the reader's to rewrite", got)
		}
	})

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("committing answered %v", err)
	}

	t.Run("the commit publishes what was staged rather than what the reader wrote into", func(t *testing.T) {
		if got := payloadsOf(drainStream(t, ctx, store, stream)); !slices.Equal(got, written) {
			t.Fatalf("the committed history reads %v, so the fact recorded is not the fact that was decided", got)
		}
	})
}

func TestAStreamIsReadInPagesTheStorePublished(t *testing.T) {
	ctx := context.Background()
	log := newLog(t, eventmemory.LogSpec{})
	store := newStore(t, eventmemory.Spec{Log: log, StreamPage: 2, MaxRead: 3})
	stream := streamOf("catalog.product", "acme/A-17")
	appendTo(t, ctx, store, stream, 0, "one", "two", "three", "four", "five")

	t.Run("the two published bounds are different numbers and each read is capped by its own", func(t *testing.T) {
		if store.Limits().StreamPage == store.Limits().MaxRead {
			t.Fatalf("this store publishes %d for both page bounds, so nothing below can tell which of the two a read applied", store.Limits().StreamPage)
		}
		if got := len(readStream(t, ctx, store, stream, 0)); got != store.Limits().StreamPage {
			t.Fatalf("a stream page holds %d envelopes where the store publishes a stream page of %d and a log page of %d, so a read of one stream is cut by the other number and the kernel either refuses every load or reads a short page as the end of the history",
				got, store.Limits().StreamPage, store.Limits().MaxRead)
		}
		page, _ := readAll(t, ctx, store, "")
		if len(page) != store.Limits().MaxRead {
			t.Fatalf("a log page holds %d envelopes where the store publishes a log page of %d and a stream page of %d",
				len(page), store.Limits().MaxRead, store.Limits().StreamPage)
		}
	})

	t.Run("a page is at most the published length and starts one after the cursor", func(t *testing.T) {
		for after := range event.Version(6) {
			page := readStream(t, ctx, store, stream, after)
			if len(page) > store.Limits().StreamPage {
				t.Fatalf("the page after version %d holds %d envelopes against a published page of %d",
					after, len(page), store.Limits().StreamPage)
			}
			if after >= 5 {
				if len(page) != 0 {
					t.Fatalf("reading past the end of the stream answered %d envelopes", len(page))
				}
				continue
			}
			if page[0].Version != after+1 {
				t.Fatalf("the page after version %d starts at version %d", after, page[0].Version)
			}
			for index, envelope := range page {
				if envelope.Version != after+event.Version(index)+1 {
					t.Fatalf("the page after version %d holds versions %v, which is not dense and ascending", after, versionsOf(page))
				}
				if envelope.Stream != stream {
					t.Fatalf("the page holds an envelope of %v", envelope.Stream)
				}
			}
		}
	})

	t.Run("the log tiles from cursor to cursor", func(t *testing.T) {
		var seen []string
		cursor := event.Cursor("")
		for range 5 {
			page, next := readAll(t, ctx, store, cursor)
			if len(page) == 0 {
				break
			}
			if len(page) > store.Limits().MaxRead {
				t.Fatalf("a global page holds %d envelopes against a published cap of %d", len(page), store.Limits().MaxRead)
			}
			seen = append(seen, payloadsOf(page)...)
			cursor = next
		}
		if want := []string{"one", "two", "three", "four", "five"}; !slices.Equal(seen, want) {
			t.Fatalf("reading the log page by page saw %v", seen)
		}
	})

	t.Run("a cursor nobody minted is refused and an empty one starts at the beginning", func(t *testing.T) {
		for _, cursor := range []event.Cursor{"7", "not-a-cursor:1", ":1", "nope"} {
			_, _, err := store.ReadAll(ctx, cursor)
			classifiedAs(t, err, event.BadCursor, "reading the log from the hand-built cursor "+string(cursor))
		}
		page, _ := readAll(t, ctx, store, "")
		if len(page) == 0 {
			t.Fatalf("the empty cursor read nothing, so a consumer has no way to start")
		}
	})
}
