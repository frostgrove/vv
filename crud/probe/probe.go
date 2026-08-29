package probe

import (
	"context"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/errs"
)

// Handler turns one classified failure into every violation the payload caused.
//
// Two rules a signature cannot carry, and both are [[D-042]]:
//
//   - **The fault is never nil when [Request.Fault] is not.** A handler that
//     found nothing, hit a cap or failed outright still hands back what the
//     driver said. Suppressing it would turn a truthful 409 into silence.
//   - **The error is advisory.** It is for the log. A caller that turned it into
//     a response would make the most failure-prone part of this design downgrade
//     a correct 409 into an opaque 500, which is the exact inversion of the
//     point — and it is genuinely failure-prone, because it re-binds values from
//     a statement that already failed.
type Handler interface {
	Enrich(ctx context.Context, request *Request) (*errs.Fault, error)
}

// Declarer is the optional interface a Handler implements to be bound to the
// model it will run against, and to refuse at declaration rather than at request
// time ([[D-021]]).
//
// It returns the bound handler rather than mutating the receiver, so one
// [Full] value declared against two models produces two handlers instead of one
// that quietly describes the second.
type Declarer interface {
	Declare(meta *crud.Meta) (Handler, error)
}

// Savepointer is the optional interface a Handler implements to say it needs the
// transaction restored before it can run, and how many savepoints it may claim
// against one transaction.
//
// The decorator asks, because a savepoint has to be taken before the write and
// a Handler only sees the failure.
type Savepointer interface {
	Savepoints() (want bool, budget int)
}

// Request is everything the repository layer knows about the write that failed.
type Request struct {
	// Op is the repository verb: "Save", "Update", "SaveAll".
	Op string
	// Fault is what the driver's failure classified to. A probe adds to it and
	// never replaces it.
	Fault *errs.Fault
	// Meta binds the model to its table.
	Meta *crud.Meta
	// Source is the datasource the repository was bound to. The probe resolves
	// its executor through crud.ExecutorFor over this, which is what makes
	// "never probe on another connection" enforceable ([[D-009]]) and keeps a
	// read that decides a write on the primary ([[D-032]]).
	Source crud.Source
	// Rows are the rows the write tried to write, in payload order.
	Rows []Row
	// Batch says the client sent a list, so every violation carries the index of
	// the row it belongs to. A one-element list is still a list.
	Batch bool
	// Upsert says the statement carried a conflict clause, so the engine
	// swallowed whatever its own clause covers and nothing may claim those.
	Upsert bool
	// Stored says the statement changed a row that was already there and wrote
	// only part of it, so the columns it did not write can be read out of that
	// row.
	//
	// An Update sets it. An upsert does not, even though it carries a key: it
	// writes every column it has, and there may be no stored row to read at all
	// — correlating to one would make a keyed Save that inserts a fresh row
	// probe nothing.
	Stored bool
	// Recovered says the caller restored the transaction after the failed write
	// — it took a savepoint before and rolled back to it. Without it, a
	// transaction on an engine that poisons cannot run another statement at all.
	Recovered bool
	// Resolve turns a table and its columns into the path the client sent, or
	// reports that it could not. It is the faults decorator's own hop handed in
	// ([[D-043]]), so this package never learns what a model field is called.
	// A nil Resolve leaves every path unresolved and marked approximate.
	Resolve func(table string, columns []string) (errs.Path, bool)
}

// Row is one row of the payload: the columns the write actually wrote, and the
// key of the row an update targeted.
type Row struct {
	// Values are keyed on column name, not field name. A column with no entry
	// was not written, and a constraint that needs one is either read from the
	// stored row or skipped.
	Values map[string]any
	// ID identifies the row an update aimed at, so the probe can exclude it from
	// its own unique terms. Absent for an insert.
	ID    any
	HasID bool
}

// Simple wraps and returns. It issues no statement and finds nothing, which is
// the honest answer wherever a second statement is not free: a batch write, a
// foreign transaction, an engine whose transaction the failure poisoned.
func Simple() Handler { return simple{} }

type simple struct{}

func (simple) Enrich(_ context.Context, request *Request) (*errs.Fault, error) {
	if request == nil {
		return nil, nil
	}
	return request.Fault, nil
}

// Declare accepts every model. Simple reads no schema, so there is nothing it
// could refuse over.
func (this simple) Declare(*crud.Meta) (Handler, error) { return this, nil }

var (
	_ Handler  = simple{}
	_ Declarer = simple{}
)
