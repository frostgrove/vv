package eventpg

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/frostgrove/vv/event"
)

const mintStatement = "SELECT pg_current_xact_id()::text"

var (
	errRowOutsideSchema = errors.New("eventpg: a row this read scanned is outside what the deployed schema promises")
	errNoRow            = errors.New("eventpg: a statement that answers exactly one row answered none")
)

type storedEvent struct {
	position   int64
	family     string
	key        string
	version    int64
	name       string
	revision   int64
	payload    []byte
	recordedAt time.Time
}

func (this storedEvent) envelope() event.Envelope {
	return event.Envelope{
		Stream:     event.Stream{Family: this.family, Key: event.Key(this.key)},
		Version:    event.Version(this.version),
		Position:   event.Position(this.position),
		Type:       this.name,
		Revision:   int(this.revision),
		Payload:    this.payload,
		RecordedAt: this.recordedAt,
	}
}

// A version above what a bigint holds is a version no row carries, so the page
// after it is the end of the stream rather than a statement bound to a negative
// number.
func (this *Store) ReadStream(ctx context.Context, stream event.Stream, after event.Version) ([]event.Envelope, error) {
	if _, err := this.opened(ctx); err != nil {
		return nil, err
	}
	if after > math.MaxInt64 {
		return nil, nil
	}
	var page []event.Envelope
	failed := this.onExecutor(ctx, func(on run) error {
		page = nil
		rows, err := on.on.QueryContext(ctx, streamStatement(this.schema.Name, this.limits.StreamPage),
			stream.Family, string(stream.Key), int64(after))
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			held := storedEvent{family: stream.Family, key: string(stream.Key)}
			if err := rows.Scan(&held.position, &held.version, &held.name, &held.revision, &held.payload, &held.recordedAt); err != nil {
				return err
			}
			if err := this.promised(held); err != nil {
				return err
			}
			page = append(page, held.envelope())
		}
		return rows.Err()
	})
	if failed != nil {
		return nil, event.Failure(event.Unclassified, causeOf(failed))
	}
	return page, nil
}

// The watermark walk, and it is written out because the argument is not
// recoverable from the code and a refactor that keeps the code correct can still
// break it. `position` comes from an identity column, so a value is drawn when a
// row is inserted and not when it commits: positions have gaps a rollback burnt,
// and a writer holding a low one can still be running while a higher one has
// committed. A store that answered `position > cursor` would deliver the higher
// one and never the lower, and nothing downstream could see that it had.
//
// A cursor is therefore three numbers rather than one — everything at or below
// from is delivered, and bound is a transaction id minted after every position at
// or below reach had been drawn. The eight steps:
//
//  1. Read the cursor. The empty one is the origin and never a refusal.
//  2. One statement, one snapshot for both halves, returning the cluster's floor
//     even on an empty page.
//  3. The gap up to reach is settled when the floor has passed the bound, because
//     every transaction that could have drawn a position below it has finished.
//  4. Deliver while the next position follows the last delivered one, or the
//     whole gap before it lies at or below what is settled. Stop at the first gap
//     that is neither.
//  5. What the next bound is minted for is the highest position the QUERY
//     returned, never the highest delivered: read the other way, a walk that
//     stops at a gap in the head of its page records a reach it has already
//     passed, settlement can never cover anything, and the walk stalls on a burnt
//     gap for good.
//  6. Nothing left undelivered discharges the bound, and so does a walk that has
//     delivered as far as the reach it was minted for: a bound that can settle
//     nothing above what the cursor already carries is worth no round trip.
//  7. A bound is minted only when there is no useful outstanding one, and never
//     while a transaction of this backing is bound. Neither of the two places
//     such a bound could come from is one this store uses. On the caller's own
//     transaction it assigns an id and settles nothing: the floor a snapshot of
//     that transaction reports never passes an id the transaction itself holds —
//     and a transaction that has written holds the floor at or below its own id,
//     so for that one no bound settles anything wherever it was minted. On a
//     second connection checked out while the caller holds one it would settle,
//     and that is how a pool at its limit deadlocks. A walk inside a transaction
//     therefore stops at the first gap it meets and stays there until the same
//     cursor is walked outside one, which is where draining the log belongs.
//  8. Only when nothing was delivered and a bound was just minted, read the page
//     and the floor again — one statement, one snapshot, exactly as step 2 — and
//     settle over the rows that second snapshot returned. Settling a fresh floor
//     against the rows of the first would declare a gap burnt while the row that
//     fills it committed between the two, and the walk would pass a committed
//     position for good. What the bound was minted for stays the FIRST page's
//     highest position: a row the second snapshot adds may have been drawn after
//     the mint. In a database nobody else is writing the bound has already
//     settled by then, so the walk continues in the same call and an ordinary gap
//     costs two more statements rather than an empty page.
func (this *Store) ReadAll(ctx context.Context, after event.Cursor) ([]event.Envelope, event.Cursor, error) {
	held, err := this.opened(ctx)
	if err != nil {
		return nil, "", err
	}
	at, unreadable := readCursor(after, held.log)
	if unreadable != nil {
		return nil, "", event.Failure(event.BadCursor, unreadable)
	}
	var page []event.Envelope
	var next walk
	failed := this.onExecutor(ctx, func(on run) error {
		page, next = nil, walk{}
		floor, fetched, err := this.fetch(ctx, on, at.from)
		if err != nil {
			return err
		}
		count := deliverable(fetched, at.from, at.settledAt(floor))
		if count < len(fetched) && !on.joined && at.spent(floor, reached(fetched, count, at.from)) {
			bound, err := number(ctx, on.on, mintStatement)
			if err != nil {
				return err
			}
			at.bound, at.reach = bound, uint64(fetched[len(fetched)-1].position)
			if count == 0 {
				floor, fetched, err = this.fetch(ctx, on, at.from)
				if err != nil {
					return err
				}
				count = deliverable(fetched, at.from, at.settledAt(floor))
			}
		}
		page = make([]event.Envelope, 0, count)
		for _, row := range fetched[:count] {
			page = append(page, row.envelope())
		}
		next = walk{from: reached(fetched, count, at.from)}
		if count < len(fetched) && next.from < at.reach {
			next.bound, next.reach = at.bound, at.reach
		}
		return nil
	})
	if failed != nil {
		return nil, "", event.Failure(event.Unclassified, causeOf(failed))
	}
	return page, mintCursor(held.log, next), nil
}

func (this *Store) fetch(ctx context.Context, on run, from uint64) (uint64, []storedEvent, error) {
	rows, err := on.on.QueryContext(ctx, logStatement(this.schema.Name, this.limits.MaxRead), int64(from))
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = rows.Close() }()
	var floor uint64
	var fetched []storedEvent
	for rows.Next() {
		var horizon string
		var position, version, revision sql.Null[int64]
		var family, key, name sql.Null[string]
		var recordedAt sql.Null[time.Time]
		var payload []byte
		if err := rows.Scan(&horizon, &position, &family, &key, &version, &name, &revision, &payload, &recordedAt); err != nil {
			return 0, nil, err
		}
		if floor, err = strconv.ParseUint(horizon, 10, 64); err != nil {
			return 0, nil, err
		}
		if !position.Valid {
			continue
		}
		held := storedEvent{
			position: position.V, family: family.V, key: key.V, version: version.V,
			name: name.V, revision: revision.V, payload: payload, recordedAt: recordedAt.V,
		}
		if err := this.promised(held); err != nil {
			return 0, nil, err
		}
		fetched = append(fetched, held)
	}
	return floor, fetched, rows.Err()
}

func deliverable(fetched []storedEvent, from, settled uint64) int {
	standing := from
	for index, row := range fetched {
		if uint64(row.position) != standing+1 && uint64(row.position) > settled+1 {
			return index
		}
		standing = uint64(row.position)
	}
	return len(fetched)
}

func reached(fetched []storedEvent, count int, from uint64) uint64 {
	if count == 0 {
		return from
	}
	return uint64(fetched[count-1].position)
}

func (this walk) settledAt(floor uint64) uint64 {
	if this.bound > 0 && floor > this.bound {
		return this.reach
	}
	return this.from
}

// A bound the cursor already carries is worth keeping unless this call has
// consumed it or the walk has passed what it was minted for. Minting a fresh one
// on every pass over a gap a long transaction holds open would move the
// settlement bar out by one poll each time and burn a transaction id per poll,
// and the walk would never settle at all.
func (this walk) spent(floor, standing uint64) bool {
	return this.bound == 0 || floor > this.bound || standing >= this.reach
}

// A store's rows are checked before an envelope is built out of them: a dropped
// constraint, a restored dump and another writer sharing the table are three
// ways a row arrives that the deployed schema would not have taken, and the
// kernel re-checks what reaches a fold but nothing re-checks what reaches a
// global consumer. The offending values are named nowhere — a refusal that
// printed them would print the key, the type name and the payload that every
// other rendering rule in this framework exists to keep out.
func (this *Store) promised(row storedEvent) error {
	switch {
	case row.position <= 0:
		return outside("position")
	case row.version <= 0:
		return outside("version")
	case row.revision <= 0 || row.revision > math.MaxInt32:
		return outside("revision")
	case len(row.family) == 0 || len(row.family) > event.MaxNameBytes:
		return outside("family")
	case len(row.key) == 0 || len(row.key) > this.limits.MaxKey:
		return outside("key")
	case len(row.name) == 0 || len(row.name) > event.MaxNameBytes:
		return outside("type")
	case len(row.payload) > this.limits.MaxPayload:
		return outside("payload")
	}
	return nil
}

func outside(column string) error {
	return fmt.Errorf("%w, and the column is %s", errRowOutsideSchema, column)
}

func number(ctx context.Context, on executor, statement string) (uint64, error) {
	rows, err := on.QueryContext(ctx, statement)
	if err != nil {
		return 0, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return 0, err
		}
		return 0, fmt.Errorf("%w: %s", errNoRow, statement)
	}
	var value string
	if err := rows.Scan(&value); err != nil {
		return 0, err
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	return strconv.ParseUint(value, 10, 64)
}

// The page size is the LIMIT, so a short page is the end of the stream by
// construction rather than by a number two places agree about. It rides the
// unique index over (family, key, version).
func streamStatement(schema string, page int) string {
	return "SELECT position, version, type, revision, payload, recorded_at\n" +
		"  FROM " + quoteIdentifier(schema) + "." + eventsTable + "\n" +
		" WHERE family = $1 AND key = $2 AND version > $3\n" +
		" ORDER BY version\n" +
		" LIMIT " + strconv.Itoa(page)
}

// The LEFT JOIN LATERAL is what carries a horizon on an empty page: the left
// side is one row whatever the right side holds, so a walk that finds nothing
// above its cursor still learns the cluster's floor and can settle a gap it saw
// earlier. The floor is read as text because xid8 is not a type database/sql's
// scan contract covers and the two mainstream drivers hand it back differently.
func logStatement(schema string, page int) string {
	return "SELECT h.floor_xid, e.position, e.family, e.key, e.version, e.type, e.revision, e.payload, e.recorded_at\n" +
		"  FROM (SELECT pg_snapshot_xmin(pg_current_snapshot())::text AS floor_xid) h\n" +
		"  LEFT JOIN LATERAL (\n" +
		"\t\tSELECT position, family, key, version, type, revision, payload, recorded_at\n" +
		"\t\t  FROM " + quoteIdentifier(schema) + "." + eventsTable + "\n" +
		"\t\t WHERE position > $1\n" +
		"\t\t ORDER BY position\n" +
		"\t\t LIMIT " + strconv.Itoa(page) + "\n" +
		"       ) e ON true"
}
