package eventpg

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/frostgrove/vv/event"
)

const (
	streamArguments = 4
	recordArguments = 3
)

var (
	errStreamMoved = errors.New("eventpg: the stream is not at the version this append was decided at")
	errStreamAhead = errors.New("eventpg: this append was decided at a version above what the schema's own bigint holds, so no stream is at it")
)

// Every step of the order this opens in is a refusal that leaves the caller's
// transaction untouched, and only then is anything issued.
func (this *Store) Append(ctx context.Context, req event.AppendRequest) error {
	if _, err := this.opened(ctx); err != nil {
		return err
	}
	if len(req.Records) == 0 {
		return nil
	}
	if req.Expected > math.MaxInt64 {
		return event.Failure(event.Conflict, errStreamAhead)
	}

	arguments := make([]any, 0, streamArguments+recordArguments*len(req.Records))
	arguments = append(arguments, req.Stream.Family, string(req.Stream.Key), int64(req.Expected), int64(len(req.Records)))
	for _, record := range req.Records {
		arguments = append(arguments, record.Type, int32(record.Revision), payloadOf(record.Payload))
	}
	statement := this.appendStatement(len(req.Records))

	var attempted run
	var affected int64
	var counting error
	failed := this.onExecutor(ctx, func(on run) error {
		attempted = on
		result, err := on.on.ExecContext(ctx, statement, arguments...)
		if err != nil {
			return err
		}
		affected, counting = result.RowsAffected()
		return nil
	})
	if failed != nil {
		return event.Failure(outcomeOf(failed, attempted.on != nil, attempted.joined), causeOf(failed))
	}
	if counting != nil {
		return event.Failure(event.Unclassified, counting)
	}
	switch affected {
	case int64(len(req.Records)):
		return nil
	case 0:
		return event.Failure(event.Conflict, errStreamMoved)
	}
	return event.Failure(event.Unclassified, fmt.Errorf(
		"eventpg: the append wrote %d rows where the batch holds %d, so this store cannot say what it left behind",
		affected, len(req.Records)))
}

// A payload of no bytes is a fact with nothing to carry and not the absence of
// one. A nil slice binds as NULL, which the column refuses, so it is normalised
// here rather than turned into a write that fails at the last moment.
func payloadOf(payload []byte) []byte {
	if payload == nil {
		return []byte{}
	}
	return payload
}

// The whole append, and every property this store rests on is a property of its
// being one statement rather than one transaction.
//
// The CTE advances streams.version WHERE it is still the version the caller
// decided at: a conditional row update PostgreSQL evaluates against the row it
// has locked, never a version read into Go and compared there, which is correct
// only against a snapshot nobody else can move. A fresh stream takes the
// speculative insertion, so two writers at version 0 do not race into a unique
// violation — the second blocks, takes the DO UPDATE path, fails the predicate
// and writes nothing. The outer INSERT selects from the CTE, so it writes one
// row per record when the CTE admitted and none at all when it did not, and a
// lost race is a row count of zero rather than an error.
//
// A single statement is atomic in PostgreSQL whether or not it runs inside an
// explicit transaction, so the version advance and the event rows commit or roll
// back together on the pool exactly as they do inside the caller's transaction.
// That is why this store opens, commits and rolls back nothing.
//
// The records are a generated VALUES list and not unnest of three arrays,
// because binding a Go slice as a PostgreSQL array is a driver extension:
// database/sql's own converter refuses it, so the unnest form would serve the
// driver this module's fixtures register and fail at run time on the other
// mainstream one. What it costs is a statement text per batch size, which is
// why nothing here is cached. ORDER BY record.ord is what makes the positions of
// one batch ascend in the order the caller listed its changes; neither a VALUES
// scan nor an unnest promises that order on its own. statement_timestamp() is
// one instant for the whole batch, read from the database rather than from a
// process, which is why this store takes no clock.
func (this *Store) appendStatement(count int) string {
	schema := quoteIdentifier(this.schema.Name)
	return "WITH admitted AS (\n" +
		"\tINSERT INTO " + schema + "." + streamsTable + " AS s (family, key, version)\n" +
		"\tSELECT $1, $2, $3::bigint + $4::bigint\n" +
		"\t WHERE $3::bigint = 0\n" +
		"\t    OR EXISTS (SELECT 1 FROM " + schema + "." + streamsTable + " WHERE family = $1 AND key = $2)\n" +
		"\tON CONFLICT (family, key) DO UPDATE SET version = s.version + $4::bigint\n" +
		"\t WHERE s.version = $3::bigint\n" +
		"\tRETURNING version\n" +
		")\n" +
		"INSERT INTO " + schema + "." + eventsTable + " (family, key, version, type, revision, payload, recorded_at)\n" +
		"SELECT $1, $2, $3::bigint + record.ord, record.type, record.revision, record.payload, statement_timestamp()\n" +
		"  FROM admitted,\n" +
		"       (VALUES " + recordRows(count) + ") AS record(type, revision, payload, ord)\n" +
		" ORDER BY record.ord"
}

func recordRows(count int) string {
	var rows strings.Builder
	for index := range count {
		if index > 0 {
			rows.WriteString(",\n               ")
		}
		rows.WriteString(recordRow(index))
	}
	return rows.String()
}

// The first row carries the type of every column, because a parameter
// PostgreSQL cannot infer a type for is a statement it refuses to plan.
func recordRow(index int) string {
	base := streamArguments + recordArguments*index
	first, second, third := strconv.Itoa(base+1), strconv.Itoa(base+2), strconv.Itoa(base+3)
	ordinal := strconv.Itoa(index + 1)
	if index == 0 {
		return "($" + first + "::text, $" + second + "::int, $" + third + "::bytea, " + ordinal + "::bigint)"
	}
	return "($" + first + ", $" + second + ", $" + third + ", " + ordinal + ")"
}
