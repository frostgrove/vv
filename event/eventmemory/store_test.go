package eventmemory_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frostgrove/vv/event"
	"github.com/frostgrove/vv/event/eventmemory"
)

func newLog(t testing.TB, spec eventmemory.LogSpec) *eventmemory.Log {
	t.Helper()
	log, err := eventmemory.NewLog(spec)
	if err != nil {
		t.Fatalf("a log over %+v was refused: %v", spec, err)
	}
	return log
}

func newStore(t testing.TB, spec eventmemory.Spec) *eventmemory.Store {
	t.Helper()
	store, err := eventmemory.New(spec)
	if err != nil {
		t.Fatalf("a store over %+v was refused: %v", spec, err)
	}
	return store
}

func openStore(t testing.TB) (*eventmemory.Log, *eventmemory.Store) {
	t.Helper()
	log := newLog(t, eventmemory.LogSpec{})
	return log, newStore(t, eventmemory.Spec{Log: log})
}

func streamOf(family, key string) event.Stream {
	return event.Stream{Family: family, Key: event.Key(key)}
}

// The type and the revision are what say which fact a record is, and a suite
// that writes one of each cannot see a store that drops both. They vary with the
// record's place in its batch and with the payload, so a store agreeing with a
// constant agrees with nothing.
var kinds = []struct {
	name     string
	revision int
}{
	{"accounts.credited", 1},
	{"accounts.debited", 2},
	{"accounts.frozen", 7},
}

func kindOf(offset int, payload string) (string, int) {
	kind := kinds[(offset+len(payload))%len(kinds)]
	return kind.name, kind.revision
}

func records(payloads ...string) []event.Record {
	held := make([]event.Record, 0, len(payloads))
	for offset, payload := range payloads {
		name, revision := kindOf(offset, payload)
		held = append(held, event.Record{Type: name, Revision: revision, Payload: []byte(payload)})
	}
	return held
}

func appendTo(t *testing.T, ctx context.Context, store *eventmemory.Store, stream event.Stream, expected event.Version, payloads ...string) {
	t.Helper()
	request := event.AppendRequest{Stream: stream, Expected: expected, Records: records(payloads...)}
	if err := store.Append(ctx, request); err != nil {
		t.Fatalf("appending %v to %v at version %d was refused: %v", payloads, stream, expected, err)
	}
}

func readStream(t *testing.T, ctx context.Context, store *eventmemory.Store, stream event.Stream, after event.Version) []event.Envelope {
	t.Helper()
	page, err := store.ReadStream(ctx, stream, after)
	if err != nil {
		t.Fatalf("reading %v after version %d failed: %v", stream, after, err)
	}
	return page
}

func readAll(t *testing.T, ctx context.Context, store *eventmemory.Store, after event.Cursor) ([]event.Envelope, event.Cursor) {
	t.Helper()
	page, next, err := store.ReadAll(ctx, after)
	if err != nil {
		t.Fatalf("reading the log from a cursor failed: %v", err)
	}
	return page, next
}

func drainStream(t *testing.T, ctx context.Context, store *eventmemory.Store, stream event.Stream) []event.Envelope {
	t.Helper()
	var held []event.Envelope
	for len(held) < drainCeiling {
		page := readStream(t, ctx, store, stream, event.Version(len(held)))
		if len(page) == 0 {
			return held
		}
		held = append(held, page...)
	}
	t.Fatalf("reading %v page by page never reached the end", stream)
	return nil
}

func drainLog(t *testing.T, ctx context.Context, store *eventmemory.Store) []event.Envelope {
	t.Helper()
	var held []event.Envelope
	cursor := event.Cursor("")
	for len(held) < drainCeiling {
		page, next := readAll(t, ctx, store, cursor)
		if len(page) == 0 {
			return held
		}
		held = append(held, page...)
		cursor = next
	}
	t.Fatalf("reading the log page by page never reached the end")
	return nil
}

const drainCeiling = 1 << 16

func ticking(start time.Time, step time.Duration) func() time.Time {
	var ticks atomic.Int64
	return func() time.Time {
		return start.Add(step * time.Duration(ticks.Add(1)-1))
	}
}

func payloadsOf(page []event.Envelope) []string {
	held := make([]string, 0, len(page))
	for _, envelope := range page {
		held = append(held, string(envelope.Payload))
	}
	return held
}

func versionsOf(page []event.Envelope) []event.Version {
	held := make([]event.Version, 0, len(page))
	for _, envelope := range page {
		held = append(held, envelope.Version)
	}
	return held
}

func positionsOf(page []event.Envelope) []event.Position {
	held := make([]event.Position, 0, len(page))
	for _, envelope := range page {
		held = append(held, envelope.Position)
	}
	return held
}

// A store's classification is opaque outside the kernel until the caller seam
// maps it, so the ground truth is the kernel's own rendering of that outcome.
func classifiedAs(t *testing.T, err error, outcome event.Outcome, doing string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s was admitted, and it is what the store is supposed to refuse", doing)
	}
	if err.Error() != event.Failure(outcome, nil).Error() {
		t.Fatalf("%s reported %q rather than a failure the store classified %v", doing, err, outcome)
	}
}

func TestTwoStoreValuesOverOneLog(t *testing.T) {
	ctx := context.Background()
	log := newLog(t, eventmemory.LogSpec{MaxPayload: 4096, MaxKey: 64})
	first := newStore(t, eventmemory.Spec{Log: log})
	second := newStore(t, eventmemory.Spec{Log: log, MaxBatch: 8, StreamPage: 4, MaxRead: 4})
	stream := streamOf("accounts.account", "acme/A-17")

	t.Run("the two answer one backing and a store over another log does not", func(t *testing.T) {
		if !first.Backing().Equal(second.Backing()) {
			t.Fatalf("two stores over one log answer different backings, so a token minted through one is refused by the other")
		}
		_, apart := openStore(t)
		if first.Backing().Equal(apart.Backing()) {
			t.Fatalf("a store over another log answers this log's backing, so every backing comparison passes and none of them means anything")
		}
	})

	t.Run("neither store owns the two numbers the data depends on", func(t *testing.T) {
		if first.Limits().MaxPayload != second.Limits().MaxPayload || first.Limits().MaxKey != second.Limits().MaxKey {
			t.Fatalf("the two stores publish %d/%d and %d/%d for payload and key, so a payload writable through one is unreadable through the other",
				first.Limits().MaxPayload, first.Limits().MaxKey, second.Limits().MaxPayload, second.Limits().MaxKey)
		}
		if first.Limits().MaxPayload != 4096 || first.Limits().MaxKey != 64 {
			t.Fatalf("the log's own numbers are %d and %d rather than the 4096 and 64 it was constructed with",
				first.Limits().MaxPayload, first.Limits().MaxKey)
		}
		if first.Limits().MaxBatch == second.Limits().MaxBatch {
			t.Fatalf("the two stores answer one batch bound, so the operational numbers are shared as well and a per-request store cannot choose its own")
		}
	})

	t.Run("a stream written through one is read through the other", func(t *testing.T) {
		appendTo(t, ctx, first, stream, 0, "credited 10")
		page := readStream(t, ctx, second, stream, 0)
		if got := payloadsOf(page); len(got) != 1 || got[0] != "credited 10" {
			t.Fatalf("the second store read %v of a stream the first wrote, so the two are not one store", got)
		}
	})

	t.Run("a cursor minted by one resumes through the other and a foreign one is refused", func(t *testing.T) {
		page, next := readAll(t, ctx, first, "")
		if len(page) != 1 {
			t.Fatalf("the first store read %d events of the one it wrote", len(page))
		}
		appendTo(t, ctx, second, stream, 1, "credited 20")
		resumed, _ := readAll(t, ctx, second, next)
		if got := payloadsOf(resumed); len(got) != 1 || got[0] != "credited 20" {
			t.Fatalf("resuming through the second store read %v rather than the one event written after the cursor", got)
		}

		_, elsewhere := openStore(t)
		_, _, err := elsewhere.ReadAll(ctx, next)
		classifiedAs(t, err, event.BadCursor, "resuming another log's read from this log's cursor")
	})
}

func TestCheckAnswersWhileTheStoreIsOpenAndAfterItIsClosed(t *testing.T) {
	ctx := context.Background()
	log, store := openStore(t)
	sibling := newStore(t, eventmemory.Spec{Log: log})

	if err := store.Check(ctx); err != nil {
		t.Fatalf("an open store answers %v to a readiness question", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("closing the store answered %v", err)
	}
	if err := store.Check(ctx); !errors.Is(err, event.ErrClosed) {
		t.Fatalf("a closed store answers %v to a readiness question rather than saying it is closed", err)
	}
	_ = store.Close()
	if err := store.Check(ctx); !errors.Is(err, event.ErrClosed) {
		t.Fatalf("a store closed twice answers %v, so the second close decided something", err)
	}
	if err := sibling.Check(ctx); err != nil {
		t.Fatalf("closing one store over a log answered %v through another store over the same log, so a close reached what it did not open", err)
	}
}

func TestCloseIsIdempotentAndDecidesNothing(t *testing.T) {
	ctx := context.Background()
	log, store := openStore(t)
	sibling := newStore(t, eventmemory.Spec{Log: log})
	committed, staged := streamOf("accounts.account", "acme/committed"), streamOf("accounts.account", "acme/staged")

	appendTo(t, ctx, store, committed, 0, "before the close")
	tx, err := store.Begin(ctx)
	if err != nil {
		t.Fatalf("beginning a transaction was refused: %v", err)
	}
	appendTo(t, eventmemory.WithTransaction(ctx, tx), store, staged, 0, "staged across the close")

	if err := store.Close(); err != nil {
		t.Fatalf("the first close answered %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("the second close answered %v, and two closes answering differently is what a deferred close beside an explicit one produces", err)
	}

	t.Run("every operation after the close refuses and says it refused rather than tried", func(t *testing.T) {
		err := store.Append(ctx, event.AppendRequest{Stream: committed, Expected: 1, Records: records("after the close")})
		classifiedAs(t, err, event.Closed, "appending to a closed store")

		_, err = store.ReadStream(ctx, committed, 0)
		classifiedAs(t, err, event.Closed, "reading a stream from a closed store")

		_, _, err = store.ReadAll(ctx, "")
		classifiedAs(t, err, event.Closed, "reading the log of a closed store")

		if _, err := store.Begin(ctx); !errors.Is(err, event.ErrClosed) {
			t.Fatalf("a closed store began a transaction, answering %v", err)
		}
	})

	t.Run("the staged work is invisible and the committed work is not", func(t *testing.T) {
		if page := readStream(t, ctx, sibling, staged, 0); len(page) != 0 {
			t.Fatalf("another reader sees %v of a transaction the close neither committed nor rolled back", payloadsOf(page))
		}
		if got := payloadsOf(readStream(t, ctx, sibling, committed, 0)); len(got) != 1 || got[0] != "before the close" {
			t.Fatalf("another reader sees %v of the work committed before the close, so the invisibility above proves nothing", got)
		}
	})

	t.Run("the caller's transaction still decides what happened to its own work", func(t *testing.T) {
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("committing a transaction begun before the close answered %v, so the close decided it", err)
		}
		if got := payloadsOf(readStream(t, ctx, sibling, staged, 0)); len(got) != 1 || got[0] != "staged across the close" {
			t.Fatalf("after the caller committed, another reader sees %v", got)
		}
	})
}

func TestAStoreIsBuiltOverEveryLogTheKernelWouldAdmit(t *testing.T) {
	t.Run("a log at any payload bound builds a store with no operational number set", func(t *testing.T) {
		for _, maxPayload := range []int{0, 1, 4096, event.MaxPayloadBytes / 2, event.MaxPayloadBytes} {
			log := newLog(t, eventmemory.LogSpec{MaxPayload: maxPayload})
			limits := newStore(t, eventmemory.Spec{Log: log}).Limits()
			page := event.MaxResidentBytes / limits.MaxPayload
			if limits.StreamPage > page || limits.MaxRead > page {
				t.Fatalf("a store defaulted to pages of %d and %d at MaxPayload %d, where one read may hold %d envelopes",
					limits.StreamPage, limits.MaxRead, limits.MaxPayload, page)
			}
			if limits.StreamPage < 1 || limits.MaxRead < 1 {
				t.Fatalf("a store defaulted to pages of %d and %d, so it publishes a read that returns nothing", limits.StreamPage, limits.MaxRead)
			}
		}
	})

	t.Run("the page the kernel's rule admits is admitted and the next one is refused", func(t *testing.T) {
		log := newLog(t, eventmemory.LogSpec{MaxPayload: event.MaxPayloadBytes})
		page := event.MaxResidentBytes / event.MaxPayloadBytes

		limits := newStore(t, eventmemory.Spec{Log: log, StreamPage: page, MaxRead: page}).Limits()
		if limits.StreamPage != page || limits.MaxRead != page {
			t.Fatalf("a store asked for pages of %d published %d and %d", page, limits.StreamPage, limits.MaxRead)
		}
		for _, over := range []eventmemory.Spec{
			{Log: log, StreamPage: page + 1},
			{Log: log, MaxRead: page + 1},
		} {
			_, err := eventmemory.New(over)
			if !errors.Is(err, event.ErrWrongStore) {
				t.Fatalf("a store over %+v answered %v, where a page holding more than one read may is not a store the kernel would admit", over, err)
			}
		}
	})
}
