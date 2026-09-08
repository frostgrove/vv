//go:build integration

package eventpg

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/frostgrove/vv/event"
)

func TestAWalkFromTheEmptyCursorTilesWithAWalkFromTheTail(t *testing.T) {
	const family = "eventpg.s4.tiling"
	schema := deployed(t, "eventpg_s4_tiling")
	ctx := t.Context()
	first := tunedStore(t, schema, 3)

	appendOn(t, ctx, first, aStream(family, "A-1"), 0, "one", "two", "three")
	appendOn(t, ctx, first, aStream(family, "A-2"), 0, "four")
	appendOn(t, ctx, first, aStream(family, "A-3"), 0, "five", "six")
	appendOn(t, ctx, first, aStream(family, "A-1"), 3, "seven", "eight")
	written := storedPositions(t, schema)
	if len(written) != 8 {
		t.Fatalf("the log holds %d rows where this case wrote eight", len(written))
	}

	head, cursor := readAll(t, ctx, first, "")
	if len(head) != 3 {
		t.Fatalf("the first walk of a consumer with no checkpoint answered %d envelopes where the store pages at three", len(head))
	}
	if len(cursor) != 58 {
		t.Fatalf("the cursor is %d characters where every cursor this store mints is fifty-eight: %q", len(cursor), cursor)
	}

	second := tunedStore(t, schema, 3)
	tail, ended := drainFrom(t, ctx, second, cursor)

	t.Run("concatenated, the two walks cover every position exactly once", func(t *testing.T) {
		covered := append(positionsOf(head), positionsOf(tail)...)
		if len(covered) != len(written) {
			t.Fatalf("the two walks delivered %d envelopes over a log of %d rows", len(covered), len(written))
		}
		for index, position := range covered {
			if int64(position) != written[index] {
				t.Fatalf("the two walks tile the log as %v where the rows are at %v, so a consumer resuming in another process skips or repeats", covered, written)
			}
			if index > 0 && covered[index-1] >= position {
				t.Fatalf("the two walks deliver positions %v, which do not ascend across the resume", covered)
			}
		}
		if got := payloadsOf(append(slices.Clone(head), tail...)); !slices.Equal(got,
			[]string{"one", "two", "three", "four", "five", "six", "seven", "eight"}) {
			t.Fatalf("the two walks concatenate to %v, which is not the order the log was written in", got)
		}
	})

	t.Run("the cursor names the log and carries no per-instance nonce", func(t *testing.T) {
		_, alsoEnded := drainFrom(t, ctx, first, cursor)
		if alsoEnded != ended {
			t.Fatalf("two store values over one schema ended the same walk at %q and %q, so a cursor carries something of the value that minted it",
				alsoEnded, ended)
		}
	})

	t.Run("a cursor from another schema of the same database is refused", func(t *testing.T) {
		beside := deployed(t, "eventpg_s4_tiling_beside")
		other := prepared(t, beside)
		appendOn(t, ctx, other, aStream(family, "A-1"), 0, "written in the other schema")
		_, foreign := readAll(t, ctx, other, "")

		page, resumed, err := first.ReadAll(ctx, foreign)
		classifiedAs(t, err, event.BadCursor, "a walk from a cursor minted over another schema of the same database")
		if len(page) != 0 || resumed != "" {
			t.Fatalf("the refused walk answered %d envelopes and the cursor %q", len(page), resumed)
		}
		if !errors.Is(event.CauseOf(walked(t, ctx, first, foreign)), errCursorForeign) {
			t.Fatal("a cursor of another schema was refused for some reason other than the log it names, and a *sql.DB identity would have accepted it")
		}
		if _, _, err := other.ReadAll(ctx, foreign); err != nil {
			t.Fatalf("the store that minted the cursor answered %v for it, so the refusal above is not about the schema it came from", err)
		}
	})
}

func TestEveryUnreadableCursorIsRefusedAndTheEmptyOneIsTheOrigin(t *testing.T) {
	const family = "eventpg.s4.cursors"
	schema := deployed(t, "eventpg_s4_cursors")
	ctx := t.Context()
	store := tunedStore(t, schema, 2)
	stream := aStream(family, "A-1")
	appendOn(t, ctx, store, stream, 0, "one", "two", "three")

	head, midway := readAll(t, ctx, store, "")
	if len(head) != 2 {
		t.Fatalf("the first page holds %d envelopes where the store pages at two", len(head))
	}

	beside := deployed(t, "eventpg_s4_cursors_beside")
	other := prepared(t, beside)
	appendOn(t, ctx, other, stream, 0, "written in the other schema")
	_, foreign := readAll(t, ctx, other, "")

	for _, unreadable := range []struct {
		what   string
		cursor event.Cursor
		cause  error
	}{
		{"a cursor minted over another log", foreign, errCursorForeign},
		{"a value that is not this format at all", "the checkpoint I persisted", errCursorFormat},
		{"a cursor whose tag this build no longer reads", event.Cursor("vve0" + strings.TrimPrefix(string(midway), cursorTag)), errCursorFormat},
		{"a cursor whose numbers no walk produced", mintCursor(logOf(t, store), walk{from: 0, bound: 0, reach: 5}), errCursorImpossible},
		{"a cursor of the right shape carrying rubbish", event.Cursor(cursorTag + strings.Repeat("!", 54)), errCursorFormat},
	} {
		t.Run(unreadable.what+" is refused", func(t *testing.T) {
			page, cursor, err := store.ReadAll(ctx, unreadable.cursor)
			classifiedAs(t, err, event.BadCursor, "a walk from "+unreadable.what)
			if len(page) != 0 || cursor != "" {
				t.Fatalf("the refused walk answered %d envelopes and the cursor %q, and a store that read from the start of its log for a checkpoint it could not parse re-applies every event it ever wrote",
					len(page), cursor)
			}
			refusal := walked(t, ctx, store, unreadable.cursor)
			if !errors.Is(refusal, event.ErrCursor) {
				t.Fatalf("a consumer resuming from %s reads %v", unreadable.what, refusal)
			}
			if !errors.Is(event.CauseOf(refusal), unreadable.cause) {
				t.Fatalf("the refusal carries %v where this case is about %v, so the refusal is universal rather than discriminating",
					event.CauseOf(refusal), unreadable.cause)
			}
		})
	}

	t.Run("a cursor from this log resumes where it stopped", func(t *testing.T) {
		page, _ := readAll(t, ctx, store, midway)
		if got := payloadsOf(page); !slices.Equal(got, []string{"three"}) {
			t.Fatalf("the resumed walk answered %v where the two before it were delivered already", got)
		}
	})

	t.Run("the empty cursor is the origin of the log and never a refusal", func(t *testing.T) {
		page, cursor, err := store.ReadAll(ctx, "")
		if err != nil {
			t.Fatalf("a consumer with no persisted checkpoint answered %v, and a store that refuses the empty cursor cannot be started by anybody", err)
		}
		if got := payloadsOf(page); !slices.Equal(got, []string{"one", "two"}) {
			t.Fatalf("the walk from the empty cursor answered %v where the log begins with the two events it was given", got)
		}
		if cursor == "" {
			t.Fatal("the walk from the origin answered the origin again, so a consumer would re-apply its whole backlog on every pass")
		}
	})
}
