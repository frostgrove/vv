package eventpg

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"sync/atomic"
	"time"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/event"
)

var (
	errCheckpointMoved   = errors.New("eventpg: the checkpoint row is not at the advance this save follows")
	errAdvanceAhead      = errors.New("eventpg: this save presents an advance above what the schema's own bigint holds, so no row is at it")
	errCursorEmpty       = errors.New("eventpg: the empty cursor is the origin of the log rather than a point to resume past")
	errCursorTooLong     = errors.New("eventpg: this cursor is longer than the ceiling the kernel publishes and the column this schema keeps one in")
	errProjectionUnnamed = errors.New("eventpg: a checkpoint is a row of one projection's and this one names none")
	errProjectionTooLong = errors.New("eventpg: this projection name is longer than the kernel's identifier bound and the column this schema keys a row by")
	errProgressAhead     = errors.New("eventpg: this progress carries a number above what the schema's own bigint holds")
	errCheckpointOutside = errors.New("eventpg: a checkpoint row this read scanned is outside what the deployed schema promises")
)

type CheckpointSpec struct {
	DB     *sql.DB
	Source crud.Source

	Schema           Schema
	SchemaManagement SchemaManagement
}

// The fourth table of the same schema, and a resource of its own: a Store and a
// Checkpoints over one schema are two resources at one schema version, so a
// deployment migrates once and each verifies at its own Prepare. It opens,
// commits and rolls back nothing, and issues every statement on a connection it
// checked out or on the caller's transaction.
type Checkpoints struct {
	db         *sql.DB
	source     crud.Source
	schema     Schema
	management SchemaManagement
	backing    event.Backing
	closed     atomic.Bool
	state      atomic.Pointer[readiness]
}

var _ event.Checkpoints = (*Checkpoints)(nil)

// Performs no I/O, starts nothing and reads no environment.
func NewCheckpoints(spec CheckpointSpec) (*Checkpoints, error) {
	if spec.DB == nil {
		return nil, fmt.Errorf("%w: CheckpointSpec.DB is nil, and a checkpoint store over no database is one that refuses everything at its first call", ErrSpec)
	}
	if spec.Source == nil {
		return nil, fmt.Errorf("%w: CheckpointSpec.Source is nil, and a checkpoint store that cannot see the caller's transaction saves outside it silently", ErrSpec)
	}
	if !crud.SameDataSource(crud.KeyOf(spec.Source), spec.DB) {
		return nil, fmt.Errorf("%w: CheckpointSpec.Source reads another data source than CheckpointSpec.DB, and atomic across two handles is a word with no meaning", ErrSpec)
	}
	if !spec.SchemaManagement.Valid() {
		return nil, fmt.Errorf("%w: CheckpointSpec.SchemaManagement is %s, and the three it may be are %s, %s and %s",
			ErrSpec, spec.SchemaManagement, UnsetSchemaManagement, VerifySchema, ManageSchema)
	}
	schema, err := spec.Schema.Resolved()
	if err != nil {
		return nil, err
	}
	held, err := event.NewBacking(backing{db: spec.DB, schema: schema.Name})
	if err != nil {
		return nil, err
	}
	management := spec.SchemaManagement
	if management == UnsetSchemaManagement {
		management = VerifySchema
	}
	return &Checkpoints{db: spec.DB, source: spec.Source, schema: schema, management: management, backing: held}, nil
}

func (this *Checkpoints) Capabilities() event.CheckpointCapabilities {
	return event.CheckpointCapabilities{Transactions: event.Supported, Persistence: event.Supported}
}

func (this *Checkpoints) Backing() event.Backing { return this.backing }

func (this *Checkpoints) Schema() Schema { return this.schema }

func (this *Checkpoints) SchemaManagement() SchemaManagement { return this.management }

// It closes no *sql.DB: the pool was opened by the composition root and is
// shared with every other store, repository and subsystem over it.
func (this *Checkpoints) Close() error {
	this.closed.Store(true)
	return nil
}

// The same two steps a store takes, over the same schema and the same advisory
// lock: a Store and a Checkpoints both managing one schema in one process
// serialise on it, and the second finds the list idempotent and the assertion
// satisfied.
func (this *Checkpoints) Prepare(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if this.closed.Load() {
		return event.ErrClosed
	}
	this.state.Store(nil)
	if this.management == ManageSchema {
		if err := migrateSchema(ctx, this.db, this.schema); err != nil {
			return err
		}
	}
	held, err := verifySchema(ctx, this.db, this.schema)
	if err != nil {
		return err
	}
	this.state.Store(&held)
	return nil
}

func (this *Checkpoints) Check(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if this.closed.Load() {
		return event.ErrClosed
	}
	held := this.state.Load()
	if held == nil {
		return ErrNotReady
	}
	return checkSchema(ctx, this.db, this.schema, *held)
}

func (this *Checkpoints) Transaction(ctx context.Context) (event.Authority, error) {
	tx, err := boundTx(ctx, this.source)
	if err != nil || tx == nil {
		return event.Authority{}, err
	}
	return event.NewAuthority(this.backing, tx)
}

// The order every operating door opens in, and it is the store's own: a
// cancellation travels as itself, a closed resource says so, one that has not
// verified its schema refuses as policy, and an ambient executor that is not a
// transaction refuses before a statement is built.
func (this *Checkpoints) opened(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if this.closed.Load() {
		return event.Failure(event.Closed, nil)
	}
	if this.state.Load() == nil {
		return event.Failure(event.Refused, ErrNotReady)
	}
	if _, err := this.Transaction(ctx); err != nil {
		return event.Failure(event.Refused, err)
	}
	return nil
}

func (this *Checkpoints) Load(ctx context.Context, projection string) (event.Checkpoint, error) {
	if err := this.opened(ctx); err != nil {
		return event.Checkpoint{}, err
	}
	var held event.Checkpoint
	var found bool
	failed := this.on(ctx, func(on run) error {
		held, found = event.Checkpoint{}, false
		rows, err := on.on.QueryContext(ctx, loadStatement(this.schema.Name), projection)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		if !rows.Next() {
			return rows.Err()
		}
		var cursor []byte
		var advance, highest, applied, quarantined int64
		var updatedAt time.Time
		if err := rows.Scan(&cursor, &advance, &highest, &applied, &quarantined, &updatedAt); err != nil {
			return err
		}
		held = event.Checkpoint{
			Projection: projection,
			Cursor:     event.Cursor(cursor),
			Advance:    uint64(advance),
			Progress: event.Progress{
				Highest:     event.Position(highest),
				Applied:     uint64(applied),
				Quarantined: uint64(quarantined),
				At:          updatedAt,
			},
		}
		found = true
		if err := promisedCheckpoint(advance, highest, applied, quarantined, cursor); err != nil {
			return err
		}
		return rows.Err()
	})
	if failed != nil {
		return event.Checkpoint{}, event.Failure(event.Unclassified, causeOf(failed))
	}
	if !found {
		return event.Checkpoint{}, nil
	}
	return held, nil
}

// The row this schema promises, checked before the answer is handed over rather
// than trusted: a dropped constraint, a restored dump or a foreign writer is how
// a row outside it arrives, and an advance of zero read as absence would resume
// a consumer at the origin of the log against a live read model.
func promisedCheckpoint(advance, highest, applied, quarantined int64, cursor []byte) error {
	switch {
	case advance <= 0:
		return fmt.Errorf("%w: its advance is %d", errCheckpointOutside, advance)
	case highest < 0 || applied < 0 || quarantined < 0:
		return fmt.Errorf("%w: its progress is %d/%d/%d", errCheckpointOutside, highest, applied, quarantined)
	case len(cursor) == 0:
		return fmt.Errorf("%w: it carries no cursor at advance %d", errCheckpointOutside, advance)
	case len(cursor) > event.MaxCursorBytes:
		return fmt.Errorf("%w: its cursor is %d bytes against the kernel's ceiling of %d", errCheckpointOutside, len(cursor), event.MaxCursorBytes)
	}
	return nil
}

// One statement issued, on the append's own shape and for the same reasons:
// atomicity is the statement's rather than a transaction's, admission is a
// predicate PostgreSQL evaluates against a row it has locked, and a lost race is
// a row count of zero rather than an error. Which of the two it is belongs to the
// advance, and the seven parameters are the same seven either way. updated_at is
// bound rather than taken from statement_timestamp(), which is the one departure
// from the append: the append's instant is a property of the write and belongs to
// the database, and Progress.At is a property of the consumer's own progress and
// must round-trip.
func (this *Checkpoints) Save(ctx context.Context, checkpoint event.Checkpoint) error {
	if err := this.opened(ctx); err != nil {
		return err
	}
	if err := refusable(checkpoint); err != nil {
		return err
	}
	if checkpoint.Advance > math.MaxInt64 {
		return event.Failure(event.Conflict, errAdvanceAhead)
	}

	var attempted run
	var affected int64
	var counting error
	failed := this.on(ctx, func(on run) error {
		attempted = on
		result, err := on.on.ExecContext(ctx, saveStatement(this.schema.Name, checkpoint.Advance),
			checkpoint.Projection, []byte(checkpoint.Cursor), int64(checkpoint.Advance),
			int64(checkpoint.Progress.Highest), int64(checkpoint.Progress.Applied),
			int64(checkpoint.Progress.Quarantined), checkpoint.Progress.At)
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
	case 1:
		return nil
	case 0:
		return event.Failure(event.Conflict, errCheckpointMoved)
	}
	return event.Failure(event.Unclassified, fmt.Errorf(
		"eventpg: the save of %q wrote %d rows where a checkpoint is one, so this store cannot say what it left behind",
		checkpoint.Projection, affected))
}

// What the column would refuse, refused before the statement is issued: a check
// constraint violation reaches the classifier with the backend alive, is read as
// a write that certainly did not land, and becomes a backend failure a consumer
// retries without limit — so a refusal this store owes an answer to must not
// arrive as a retryable one.
func refusable(checkpoint event.Checkpoint) error {
	switch {
	case checkpoint.Projection == "":
		return event.Failure(event.Refused, errProjectionUnnamed)
	case len(checkpoint.Projection) > event.MaxNameBytes:
		return event.Failure(event.Refused, errProjectionTooLong)
	case checkpoint.Cursor == "":
		return event.Failure(event.Refused, errCursorEmpty)
	case len(checkpoint.Cursor) > event.MaxCursorBytes:
		return event.Failure(event.Refused, errCursorTooLong)
	case checkpoint.Progress.Highest > math.MaxInt64 ||
		checkpoint.Progress.Applied > math.MaxInt64 ||
		checkpoint.Progress.Quarantined > math.MaxInt64:
		return event.Failure(event.Refused, errProgressAhead)
	}
	return nil
}

// Not fenced, and an absent name is not a refusal: a running consumer's next
// save finds no row at its advance, is refused and stops, which is the correct
// and visible outcome of retiring a live one.
func (this *Checkpoints) Forget(ctx context.Context, projection string) error {
	if err := this.opened(ctx); err != nil {
		return err
	}
	if projection == "" {
		return event.Failure(event.Refused, errProjectionUnnamed)
	}
	var attempted run
	failed := this.on(ctx, func(on run) error {
		attempted = on
		_, err := on.on.ExecContext(ctx, forgetStatement(this.schema.Name), projection)
		return err
	})
	if failed != nil {
		return event.Failure(outcomeOf(failed, attempted.on != nil, attempted.joined), causeOf(failed))
	}
	return nil
}

func (this *Checkpoints) on(ctx context.Context, use func(run) error) error {
	return onExecutor(ctx, this.db, this.source, this.schema.Name, use)
}

func loadStatement(schema string) string {
	return "SELECT cursor, advance, highest, applied, quarantined, updated_at\n" +
		"  FROM " + quoteIdentifier(schema) + "." + checkpointsTable + "\n" +
		" WHERE projection = $1"
}

// Two statements and exactly one is issued, because a save is one decision and
// not two. A single INSERT … ON CONFLICT asks whether the row is there against
// the statement's own READ COMMITTED snapshot and asks which row it collides
// with against the live index: a DELETE committing between those two moments
// turns a save at an arbitrary advance into a plain insert, and the row an
// operator retired comes back at an advance no first save ever created.
//
// Split, neither half can do that. A save above advance 1 moves a row and an
// UPDATE matches nothing where the row has gone. A save at advance 1 creates one
// and DO NOTHING never overwrites one, because the advance column is positive by
// constraint and no live row can be one a first save follows. Both keep every
// property the append's statement has: one statement, admission a predicate
// PostgreSQL evaluates against a row it has locked, and a lost race a row count
// of zero rather than an error.
func saveStatement(schema string, advance uint64) string {
	table := quoteIdentifier(schema) + "." + checkpointsTable
	if advance == 1 {
		return "INSERT INTO " + table + " (projection, cursor, advance, highest, applied, quarantined, updated_at)\n" +
			"VALUES ($1::text, $2::bytea, $3::bigint, $4::bigint, $5::bigint, $6::bigint, $7::timestamptz)\n" +
			"ON CONFLICT (projection) DO NOTHING"
	}
	return "UPDATE " + table + "\n" +
		"   SET cursor = $2::bytea, advance = $3::bigint, highest = $4::bigint,\n" +
		"       applied = $5::bigint, quarantined = $6::bigint, updated_at = $7::timestamptz\n" +
		" WHERE projection = $1::text AND advance = $3::bigint - 1"
}

func forgetStatement(schema string) string {
	return "DELETE FROM " + quoteIdentifier(schema) + "." + checkpointsTable + " WHERE projection = $1"
}
