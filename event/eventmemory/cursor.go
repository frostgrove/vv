package eventmemory

import (
	"errors"
	"strconv"
	"strings"

	"github.com/frostgrove/vv/event"
)

const cursorSeparator = ":"

var (
	errCursorForeign    = errors.New("eventmemory: this cursor was minted over another log")
	errCursorUnreadable = errors.New("eventmemory: this cursor is not one this store mints")
)

// The log names itself in every cursor it mints, and the name is the log's
// rather than a store value's: a restart resumes through a store value that did
// not exist when the cursor was written, and two store values over one log are
// one store.
func (this *Log) mintCursor(at event.Position) event.Cursor {
	return event.Cursor(this.fingerprint + cursorSeparator + strconv.FormatUint(uint64(at), 10))
}

func (this *Log) readCursor(cursor event.Cursor) (event.Position, error) {
	if cursor == "" {
		return 0, nil
	}
	minter, at, separated := strings.Cut(string(cursor), cursorSeparator)
	if !separated {
		return 0, errCursorUnreadable
	}
	if minter != this.fingerprint {
		return 0, errCursorForeign
	}
	position, err := strconv.ParseUint(at, 10, 64)
	if err != nil {
		return 0, errCursorUnreadable
	}
	return event.Position(position), nil
}
