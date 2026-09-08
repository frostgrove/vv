package eventpg

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"math"

	"github.com/frostgrove/vv/event"
)

// Fixed width: four tag characters and fifty-four of base64 over forty bytes,
// fifty-eight in all. The tag is the format's own version, so a build that
// retires this encoding refuses the cursors of the one before it rather than
// reading them as something else; the sixteen bytes are the deployed schema's
// log, so a store over another schema of the same database refuses a cursor this
// one minted and every store value over this schema accepts it.
const (
	cursorTag   = "vve1"
	cursorBytes = logBytes + 3*8
)

var (
	errCursorFormat     = errors.New("eventpg: this cursor is not one this store mints, or it is in a format this build no longer reads")
	errCursorForeign    = errors.New("eventpg: this cursor was minted over another log")
	errCursorImpossible = errors.New("eventpg: this cursor names a walk no store of this log could have produced")
)

// How far a walk has got. Every position at or below from has been delivered;
// bound, when it is not zero, is a transaction id minted after every position at
// or below reach had been drawn, so all of those are finished the moment a later
// snapshot reports a floor above it.
type walk struct {
	from  uint64
	bound uint64
	reach uint64
}

func mintCursor(log [logBytes]byte, at walk) event.Cursor {
	var held [cursorBytes]byte
	copy(held[:logBytes], log[:])
	binary.BigEndian.PutUint64(held[logBytes:], at.from)
	binary.BigEndian.PutUint64(held[logBytes+8:], at.bound)
	binary.BigEndian.PutUint64(held[logBytes+16:], at.reach)
	return event.Cursor(cursorTag + base64.RawURLEncoding.EncodeToString(held[:]))
}

// The empty cursor is the origin of the log and never a refusal. It is how a
// consumer with no persisted checkpoint starts, and a store that answered
// BadCursor for it could not be started by anybody: the failure would arrive at
// the first read of a fresh deployment.
func readCursor(cursor event.Cursor, log [logBytes]byte) (walk, error) {
	if cursor == "" {
		return walk{}, nil
	}
	text := string(cursor)
	if len(text) < len(cursorTag) || text[:len(cursorTag)] != cursorTag {
		return walk{}, errCursorFormat
	}
	held, err := base64.RawURLEncoding.DecodeString(text[len(cursorTag):])
	if err != nil || len(held) != cursorBytes {
		return walk{}, errCursorFormat
	}
	if !bytes.Equal(held[:logBytes], log[:]) {
		return walk{}, errCursorForeign
	}
	at := walk{
		from:  binary.BigEndian.Uint64(held[logBytes:]),
		bound: binary.BigEndian.Uint64(held[logBytes+8:]),
		reach: binary.BigEndian.Uint64(held[logBytes+16:]),
	}
	if !at.possible() {
		return walk{}, errCursorImpossible
	}
	return at, nil
}

// A position is a bigint, so neither number can be above what one holds; a
// cursor carrying no bound carries no reach either; and a bound is minted for a
// reach at or above what the walk had delivered when it was minted. A triple
// outside those three is one no walk produced, and walking it would skip the
// events between what it claims and what was really delivered.
func (this walk) possible() bool {
	switch {
	case this.from > math.MaxInt64 || this.reach > math.MaxInt64:
		return false
	case this.bound == 0:
		return this.reach == 0
	}
	return this.reach >= this.from
}
