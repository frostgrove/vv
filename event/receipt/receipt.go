package receipt

import (
	"context"
	"time"

	"github.com/frostgrove/vv/event"
)

// The durable row, and it is the application's table. This package declares the
// shape, reads it and writes none of it.
//
// First and Last are the range the operation wrote and are both zero when it
// wrote nothing, which is a decision that yielded no changes and is a completed
// operation like any other. For a range that exists, First - 1 is the version the
// operation was decided at: recorded, never compared, and the answer to "what was
// the anchor" for an operator who asks.
//
// RecordedAt is the ledger's own database clock — statement_timestamp(), not a
// parameter — so every instant this package compares comes from one clock except
// ResolveSpec.Issued, which is the caller's and cannot be anything else. It is
// zero on the value handed to Ledger.Claim and filled on the one handed back.
type Receipt struct {
	Key         Key
	Fingerprint Fingerprint
	Stream      event.Stream
	First, Last event.Version
	Complete    bool
	RecordedAt  time.Time
}

// The application's own table, in the database the events are in, behind an
// interface this framework does not implement — the shape Park and Generations
// already have.
//
// WHERE EACH METHOD RUNS IS PART OF THIS CONTRACT. Claim and Complete run INSIDE
// the caller's transaction, through the context they are given, and that is what
// makes a receipt and its events one commit. Find and Horizon run OUTSIDE one,
// on a second connection, because the whole point of resolving is that the first
// connection is gone.
//
// Claim is INSERT ... ON CONFLICT (key) DO NOTHING and then a SELECT of the same
// key, in that order, in the caller's one transaction. The order is the whole
// mechanism: the insert is what blocks on the primary-key index, and the select
// is a second statement and therefore a second snapshot, which is what lets a
// loser at READ COMMITTED read the row the winner committed while it was
// blocked. A select placed first sees nothing and makes its caller append a
// second copy; a single statement that tries to do both shares one snapshot and
// answers the loser zero rows, which is neither a win nor a repeat. won is the
// insert's affected-row count and nothing else.
//
// Nothing here chooses an isolation level. What the level decides is how a loser
// fails, not whether it may append: at READ COMMITTED it blocks and repeats, and
// at REPEATABLE READ or SERIALIZABLE it blocks and then raises SQLSTATE 40001,
// which rolls the caller's whole unit back and whose retry repeats. PostgreSQL's
// speculative insertion makes DO NOTHING wait on a conflicting uncommitted tuple
// and then insert or not on that transaction's outcome — which is why a claim
// never has to answer "unresolved" and Resolve does.
//
// Horizon is an instant at or before the oldest row this ledger still answers
// for, and it is the ledger's own database clock: MIN(recorded_at), or
// now() - retention computed in SQL for a table that may be empty, and never a
// clock read in the ledger's process. Answering an instant older than the truth
// is conformant and costs a caller one more Unresolved; answering a newer one is
// not, because it reads a row that was swept as one that never existed. A ledger
// that answers the zero instant claims to hold everything for ever, which is what
// a table with no sweep is. The sweep is the application's; nothing here prunes.
type Ledger interface {
	Transaction(ctx context.Context) (event.Authority, error)
	Claim(ctx context.Context, receipt Receipt) (held Receipt, won bool, err error)
	Complete(ctx context.Context, receipt Receipt) error
	Find(ctx context.Context, key Key) (Receipt, bool, error)
	Horizon(ctx context.Context) (time.Time, error)
}
