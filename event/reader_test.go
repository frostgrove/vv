package event

import (
	"context"
	"errors"
	"strings"
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

// A store whose cursor is whatever the case wants it to be. The page it answers
// is the real store's, so the cursor is the only thing under test.
type mintingLog struct {
	Log
	mint func(page []Envelope, cursor Cursor) Cursor
}

func (this *mintingLog) ReadAll(ctx context.Context, after Cursor) ([]Envelope, Cursor, error) {
	page, cursor, err := this.Log.ReadAll(ctx, after)
	if err != nil {
		return nil, "", err
	}
	if this.mint != nil {
		cursor = this.mint(page, cursor)
	}
	return page, cursor, nil
}

func logOfSeveralPages(t *testing.T) *recordingStore {
	t.Helper()
	store := newRecordingStore(t)
	repo, declared := bindAccounts(t, store)
	acme := accountID{tenant: "acme", number: "A-17"}
	ctx := context.Background()
	_, at, err := repo.Load(ctx, acme)
	if err != nil {
		t.Fatalf("the stream the cases below walk was refused with %v", err)
	}
	if _, _, err := repo.Append(ctx, at,
		declared.opened.New(acme, opened{Owner: "acme"}),
		declared.credited.New(acme, creditedV2{Minor: 1, Reason: "one"}),
		declared.credited.New(acme, creditedV2{Minor: 2, Reason: "two"}),
		declared.credited.New(acme, creditedV2{Minor: 4, Reason: "four"}),
	); err != nil {
		t.Fatalf("the events the cases below walk could not be written: %v", err)
	}
	store.forget()
	return store
}

// A checkpoint store has a column of exactly this width, so a log that mints a
// cursor over it is caught where store honesty is already checked rather than by
// a constraint violation two subsystems away.
func TestAReaderRefusesACursorOverTheCeiling(t *testing.T) {
	ctx := context.Background()
	log := &mintingLog{Log: ReadOnly(logOfSeveralPages(t))}
	reader, err := Read(log, "")
	if err != nil {
		t.Fatalf("a reader over an honest store was refused with %v", err)
	}
	if more, err := reader.Next(ctx); err != nil || !more {
		t.Fatalf("the ordinary store's own cursor answered %v and more=%v, so the rows below pass against a reader that refuses every page", err, more)
	}
	settled := reader.Cursor()
	if settled == "" {
		t.Fatal("the control read left no cursor, so there is nothing for the refusals below to leave unchanged")
	}

	log.mint = func([]Envelope, Cursor) Cursor { return Cursor(strings.Repeat("c", MaxCursorBytes+1)) }
	if _, err := reader.Next(ctx); !errors.Is(err, ErrBackend) {
		t.Fatalf("a cursor of %d bytes answered %v where the kernel publishes a ceiling of %d", MaxCursorBytes+1, err, MaxCursorBytes)
	}
	if reader.Cursor() != settled {
		t.Fatal("the reader took the cursor it refused, so the next read resumes from a value no checkpoint store can hold")
	}

	log.mint = func([]Envelope, Cursor) Cursor { return Cursor(strings.Repeat("c", MaxCursorBytes)) }
	if _, err := reader.Next(ctx); err != nil {
		t.Fatalf("a cursor of exactly %d bytes answered %v, and the ceiling is the last value a store may mint rather than the first it may not", MaxCursorBytes, err)
	}
	if len(reader.Cursor()) != MaxCursorBytes {
		t.Fatalf("a cursor at exactly the ceiling was accepted and the reader carries %d bytes", len(reader.Cursor()))
	}
}

// The empty cursor is the origin of the log, in every store that ships. Beside a
// real page it is therefore a store that told a consumer to read the head again:
// the same page for ever, no error, no halt, and a destination written once a
// pass while its checkpoint advanced.
func TestAReaderRefusesAnEmptyCursorBesideANonEmptyPage(t *testing.T) {
	ctx := context.Background()
	log := &mintingLog{Log: ReadOnly(logOfSeveralPages(t))}
	reader, err := Read(log, "")
	if err != nil {
		t.Fatalf("a reader over an honest store was refused with %v", err)
	}
	if more, err := reader.Next(ctx); err != nil || !more {
		t.Fatalf("the ordinary store's own cursor answered %v and more=%v, so the rows below pass against a reader that refuses every page", err, more)
	}
	settled := reader.Cursor()

	log.mint = func([]Envelope, Cursor) Cursor { return "" }
	if _, err := reader.Next(ctx); !errors.Is(err, ErrBackend) {
		t.Fatalf("a real page answered beside the empty cursor answered %v", err)
	}
	if reader.Cursor() != settled {
		t.Fatal("the reader took the empty cursor, so its next read is the head of the log rather than the page after the one it holds")
	}

	log.mint = nil
	more, err := reader.Next(ctx)
	if err != nil || !more {
		t.Fatalf("the page the refusal was about answered %v and more=%v on the read that followed", err, more)
	}
	if page := reader.Events(); len(page) != 1 || page[0].Version != 4 {
		t.Fatalf("the read after the refusal answered %d envelopes beginning at version %d, so the reader did not resume where it was left", len(page), page[0].Version)
	}

	fresh := &mintingLog{Log: ReadOnly(newRecordingStore(t)), mint: func([]Envelope, Cursor) Cursor { return "" }}
	origin, err := Read(fresh, "")
	if err != nil {
		t.Fatalf("a reader over an empty store was refused with %v", err)
	}
	if more, err := origin.Next(ctx); err != nil || more {
		t.Fatalf("an empty page beside the empty cursor answered %v and more=%v; that is a fresh log read from the origin and nothing was delivered to resume past", err, more)
	}
}
