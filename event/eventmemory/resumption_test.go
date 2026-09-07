package eventmemory_test

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/frostgrove/vv/event"
	"github.com/frostgrove/vv/event/eventmemory"
)

func TestACursorIsTheBackingsAndResumesThroughAnyStoreValueOverIt(t *testing.T) {
	ctx := context.Background()
	log := newLog(t, eventmemory.LogSpec{})
	store := newStore(t, eventmemory.Spec{Log: log, MaxRead: 2})
	stream := streamOf("audit.entry", "acme/A-17")
	appendTo(t, ctx, store, stream, 0, "one", "two", "three", "four", "five")

	page, first := readAll(t, ctx, store, "")
	if got := payloadsOf(page); !slices.Equal(got, []string{"one", "two"}) {
		t.Fatalf("the first page holds %v", got)
	}
	checkpoint := string(first)

	t.Run("a restart resumes through a store value that did not exist when the cursor was minted", func(t *testing.T) {
		restarted := newStore(t, eventmemory.Spec{Log: log, MaxRead: 3})
		resumed, next := readAll(t, ctx, restarted, event.Cursor(checkpoint))
		if got := payloadsOf(resumed); !slices.Equal(got, []string{"three", "four", "five"}) {
			t.Fatalf("resuming from the persisted cursor in a second store value read %v, so a restart either skipped or repeated what the first pass had returned", got)
		}
		if page, _ := readAll(t, ctx, restarted, next); len(page) != 0 {
			t.Fatalf("resuming from the end of the log read %v", payloadsOf(page))
		}
	})

	t.Run("the cursor names the backing and not the value that minted it", func(t *testing.T) {
		second := newStore(t, eventmemory.Spec{Log: log, MaxRead: 2})
		_, mintedElsewhere := readAll(t, ctx, second, "")
		if string(mintedElsewhere) != checkpoint {
			t.Fatalf("two store values over one log mint %q and %q at one position, so a cursor carries something of the value that minted it and no restart can use it",
				mintedElsewhere, checkpoint)
		}
		_, apart := openStore(t)
		_, _, err := apart.ReadAll(ctx, event.Cursor(checkpoint))
		classifiedAs(t, err, event.BadCursor, "resuming another backing's read from this one's cursor")
	})

	t.Run("a checkpoint that is up to date does not rewind and does not skip what is written after it", func(t *testing.T) {
		atEnd := drainCursor(t, ctx, store)
		if page, again := readAll(t, ctx, store, atEnd); len(page) != 0 || again != atEnd {
			t.Fatalf("reading from a cursor at the end of the log answered %v and moved the cursor to %q", payloadsOf(page), again)
		}
		appendTo(t, ctx, store, stream, 5, "six")
		resumed, _ := readAll(t, ctx, store, atEnd)
		if got := payloadsOf(resumed); !slices.Equal(got, []string{"six"}) {
			t.Fatalf("resuming from a cursor that was at the end read %v rather than the one event written after it", got)
		}
	})

	t.Run("burned positions skip nothing", func(t *testing.T) {
		before := drainCursor(t, ctx, store)
		inside, tx := begin(t, ctx, store)
		appendTo(t, inside, store, streamOf("audit.entry", "acme/burned"), 0, "rolled back", "rolled back too")
		if err := tx.Rollback(ctx); err != nil {
			t.Fatalf("rolling back answered %v", err)
		}
		appendTo(t, ctx, store, stream, 6, "seven")

		resumed, _ := readAll(t, ctx, store, before)
		if got := payloadsOf(resumed); !slices.Equal(got, []string{"seven"}) {
			t.Fatalf("resuming across the positions a rollback burned read %v, so the gap either hid the next event or handed back a rolled-back one", got)
		}
	})

	t.Run("the empty cursor is the start of the log and of an empty one", func(t *testing.T) {
		if got := payloadsOf(drainLog(t, ctx, store)); len(got) != 7 {
			t.Fatalf("reading from the empty cursor tiled %d of the seven committed events", len(got))
		}
		_, fresh := openStore(t)
		page, cursor := readAll(t, ctx, fresh, "")
		if len(page) != 0 {
			t.Fatalf("an empty log answered %v", payloadsOf(page))
		}
		appendTo(t, ctx, fresh, stream, 0, "the first event of all")
		resumed, _ := readAll(t, ctx, fresh, cursor)
		if got := payloadsOf(resumed); !slices.Equal(got, []string{"the first event of all"}) {
			t.Fatalf("a cursor an empty log minted resumed at %v, so a consumer that checkpointed before the first write loses it", got)
		}
	})
}

func drainCursor(t *testing.T, ctx context.Context, store *eventmemory.Store) event.Cursor {
	t.Helper()
	cursor := event.Cursor("")
	for range drainCeiling {
		page, next := readAll(t, ctx, store, cursor)
		cursor = next
		if len(page) == 0 {
			return cursor
		}
	}
	t.Fatalf("reading the log page by page never reached the end")
	return ""
}

// The cursor is the one value a consumer persists, so what comes back is
// whatever a checkpoint column, a file or another store's minting held — and
// the store's answer to all of it is one of two shapes.
func FuzzACursorEitherResumesInsideTheLogOrIsRefused(f *testing.F) {
	for _, seed := range []string{
		"", "7", ":", ":1", "nope", "not-a-cursor:1", "A:1:2", "-1", "1", " :1",
		"18446744073709551616", strings.Repeat("9", 40), "\x00", "\xff\xfe:1", "%00:1",
	} {
		f.Add(seed)
	}

	ctx := context.Background()
	log := newLog(f, eventmemory.LogSpec{})
	store := newStore(f, eventmemory.Spec{Log: log, MaxRead: 2})
	stream := streamOf("audit.entry", "acme/A-17")
	written := []string{"one", "two", "three", "four", "five", "six"}
	if err := store.Append(ctx, event.AppendRequest{Stream: stream, Records: records(written...)}); err != nil {
		f.Fatalf("appending the log the cursors are read against was refused: %v", err)
	}
	_, minted, err := store.ReadAll(ctx, "")
	if err != nil {
		f.Fatalf("reading the first page was refused: %v", err)
	}

	f.Fuzz(func(t *testing.T, text string) {
		for _, cursor := range []event.Cursor{event.Cursor(text), minted + event.Cursor(text)} {
			page, next, err := store.ReadAll(ctx, cursor)
			if err != nil {
				if err.Error() != event.Failure(event.BadCursor, nil).Error() {
					t.Fatalf("the cursor %q was refused with %v, which is not the one answer a cursor this store did not mint has", cursor, err)
				}
				if len(page) != 0 || next != "" {
					t.Fatalf("the refused cursor %q came back with %d envelopes and the cursor %q, so a caller that ignores one refusal checkpoints past events it never read",
						cursor, len(page), next)
				}
				continue
			}
			if len(page) > store.Limits().MaxRead {
				t.Fatalf("the cursor %q read %d envelopes against a published cap of %d", cursor, len(page), store.Limits().MaxRead)
			}
			if len(page) == 0 {
				if _, again, err := store.ReadAll(ctx, next); err != nil || again != next {
					t.Fatalf("the cursor %q read nothing and answered %q, which then answered %q and %v, so a consumer at the end of the log cannot checkpoint",
						cursor, next, again, err)
				}
				continue
			}
			at := slices.Index(written, string(page[0].Payload))
			if at < 0 || at+len(page) > len(written) || !slices.Equal(payloadsOf(page), written[at:at+len(page)]) {
				t.Fatalf("the cursor %q read %v, which is not a run of the log %v in the order it was written", cursor, payloadsOf(page), written)
			}
			following, _, err := store.ReadAll(ctx, next)
			if err != nil {
				t.Fatalf("the cursor %q answered %q, which the store that minted it then refused with %v", cursor, next, err)
			}
			if at+len(page) < len(written) && (len(following) == 0 || string(following[0].Payload) != written[at+len(page)]) {
				t.Fatalf("resuming from the cursor %q answered %v where the log continues with %q, so the resume point skipped an event",
					next, payloadsOf(following), written[at+len(page)])
			}
		}
	})
}

// A payload is caller bytes at both ends: it is written from an array the caller
// keeps and read into one the reader may write.
func FuzzAPayloadIsHandedBackByteForByteAndBelongsToWhoeverReadsIt(f *testing.F) {
	for _, seed := range []string{"", "credited 10", "\x00", "\xff\xfe\xfd", "{\"minor\":1}", strings.Repeat("f", 4096), "рога", "\n\r\t"} {
		f.Add([]byte(seed))
	}

	ctx := context.Background()
	stream := streamOf("audit.entry", "acme/A-17")

	f.Fuzz(func(t *testing.T, payload []byte) {
		log := newLog(t, eventmemory.LogSpec{MaxPayload: event.MaxPayloadBytes})
		store := newStore(t, eventmemory.Spec{Log: log})
		mine := bytes.Clone(payload)
		if err := store.Append(ctx, event.AppendRequest{
			Stream:  stream,
			Records: []event.Record{{Type: "accounts.credited", Revision: 1, Payload: mine}},
		}); err != nil {
			t.Fatalf("appending %d bytes was refused: %v", len(mine), err)
		}
		for index := range mine {
			mine[index] ^= 0xff
		}

		page, err := store.ReadStream(ctx, stream, 0)
		if err != nil {
			t.Fatalf("reading back %d bytes failed: %v", len(payload), err)
		}
		if len(page) != 1 || !bytes.Equal(page[0].Payload, payload) {
			t.Fatalf("%d bytes were read back as %q where %q was appended", len(payload), page[0].Payload, payload)
		}
		for index := range page[0].Payload {
			page[0].Payload[index] ^= 0xff
		}
		again, err := store.ReadStream(ctx, stream, 0)
		if err != nil {
			t.Fatalf("reading the same event a second time failed: %v", err)
		}
		if !bytes.Equal(again[0].Payload, payload) {
			t.Fatalf("after the reader wrote into the page it was handed, the same event reads %q where %q was appended", again[0].Payload, payload)
		}
	})
}
