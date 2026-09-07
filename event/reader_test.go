package event

import (
	"context"
	"errors"
	"testing"
)

// The consumer's half: pages of the store's own size, a cursor it may persist,
// and a page that stays the consumer's after the read that follows it. A page
// the store cannot be resumed from is refused before a consumer checkpoints past
// an event it never saw.
func TestAConsumerReadsThroughPagesTheStorePublished(t *testing.T) {
	store := newRecordingStore(t)
	repo, declared := bindAccounts(t, store)
	acme := accountID{tenant: "acme", number: "A-17"}
	ctx := context.Background()

	_, at, err := repo.Load(ctx, acme)
	if err != nil {
		t.Fatalf("the stream this case reads back was refused with %v", err)
	}
	written := []Change[account]{
		declared.opened.New(acme, opened{Owner: "acme"}),
		declared.credited.New(acme, creditedV2{Minor: 1, Reason: "one"}),
		declared.credited.New(acme, creditedV2{Minor: 2, Reason: "two"}),
		declared.credited.New(acme, creditedV2{Minor: 4, Reason: "four"}),
	}
	if _, _, err := repo.Append(ctx, at, written...); err != nil {
		t.Fatalf("the events this case reads back could not be written: %v", err)
	}

	reader, err := Read(ReadOnly(store), "")
	if err != nil {
		t.Fatalf("a reader over an honest store was refused with %v", err)
	}
	read, pages, held := 0, 0, []Envelope(nil)
	for {
		more, err := reader.Next(ctx)
		if err != nil {
			t.Fatalf("a page after %d events answered %v", read, err)
		}
		if !more {
			break
		}
		if pages == 0 {
			held = reader.Events()
		}
		for _, envelope := range reader.Events() {
			read++
			if envelope.Version != Version(read) {
				t.Fatalf("the %d event of the walk is version %d, so the concatenation of the pages is not the stream", read, envelope.Version)
			}
		}
		pages++
	}
	if read != len(written) || pages < 2 {
		t.Fatalf("a walk over %d events in pages of %d read %d of them in %d pages", len(written), store.limits.MaxRead, read, pages)
	}
	if len(held) != store.limits.MaxRead || held[0].Version != 1 {
		t.Fatalf("the first page holds %d envelopes and begins at version %d after the reads that followed it, and a page a consumer fanned out to workers must survive them",
			len(held), held[0].Version)
	}
	if reader.Cursor() == "" {
		t.Fatal("a drained reader carries no cursor, and a cursor is the only resume point a consumer may persist")
	}

	for _, one := range []struct {
		what  string
		whole func([]Envelope) []Envelope
	}{
		{"a page longer than the bound the store publishes", func(page []Envelope) []Envelope {
			if len(page) == 0 {
				return page
			}
			over := page[len(page)-1]
			over.Position++
			return append(page, over)
		}},
		{"a page whose positions do not ascend", func(page []Envelope) []Envelope {
			if len(page) < 2 {
				return page
			}
			page[0], page[len(page)-1] = page[len(page)-1], page[0]
			return page
		}},
	} {
		store.whole = one.whole
		reader, err := Read(ReadOnly(store), "")
		if err != nil {
			t.Fatalf("%s: the reader was refused before it read anything: %v", one.what, err)
		}
		if _, err := reader.Next(ctx); !errors.Is(err, ErrBackend) {
			t.Fatalf("%s answered %v, so a consumer checkpoints past an event it never saw", one.what, err)
		}
	}
}
