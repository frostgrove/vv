package jobspg

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/frostgrove/vv/jobs"
)

type TxStager struct {
	driver      *Driver
	tx          *sql.Tx
	transaction jobs.TransactionContext
}

var _ jobs.Stager = (*TxStager)(nil)

// The transaction has to be provably this driver's database, or the outbox is not
// an outbox: the invocation row and the rows the caller wrote commit
// independently, which is the failure [[D-118]] and UC-031 exist to refuse. It
// cannot be read off a *sql.Tx, so the caller hands over the *sql.DB it began on
// and the driver compares it.
//
// StageIn is the form with nothing to compare, and is the one to reach for.
func (d *Driver) Stager(db *sql.DB, tx *sql.Tx) (*TxStager, error) {
	if db == nil {
		return nil, fmt.Errorf("jobspg: %w: the database the transaction began on is required", jobs.ErrInvalid)
	}
	if db != d.db {
		return nil, fmt.Errorf("jobspg: %w: the transaction belongs to another database, so a placement in it is not an outbox", jobs.ErrUnsupported)
	}
	return d.stager(tx)
}

// Begins the transaction itself, so there is no second database to rule out. The
// stager is valid only inside the callback; the transaction commits when it
// returns nil and rolls back on any error or panic, taking the placement with it.
func (d *Driver) StageIn(ctx context.Context, effect func(*TxStager) error) error {
	if err := d.requireReady(); err != nil {
		return err
	}
	if ctx == nil || effect == nil {
		return fmt.Errorf("jobspg: %w: staging inputs are required", jobs.ErrInvalid)
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	stager, err := d.stager(tx)
	if err != nil {
		return err
	}
	if err := effect(stager); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func (d *Driver) stager(tx *sql.Tx) (*TxStager, error) {
	if err := d.requireReady(); err != nil {
		return nil, err
	}
	if tx == nil {
		return nil, fmt.Errorf("jobspg: %w: transaction is required", jobs.ErrInvalid)
	}
	raw, err := d.token()
	if err != nil {
		return nil, err
	}
	var bindingBytes [32]byte
	copy(bindingBytes[:], raw)
	binding, err := jobs.TransactionBindingFromBytes(bindingBytes)
	if err != nil {
		return nil, err
	}
	transaction, err := jobs.NewTransactionContext(d.description.ID(), binding, d.description.Durability())
	if err != nil {
		return nil, err
	}
	return &TxStager{driver: d, tx: tx, transaction: transaction}, nil
}

func (s *TxStager) Transaction() jobs.TransactionContext {
	if s == nil {
		return jobs.TransactionContext{}
	}
	return s.transaction
}

func (s *TxStager) Stage(ctx context.Context, placement jobs.Placement) (jobs.Staged, error) {
	if s == nil || s.driver == nil || s.tx == nil || s.transaction.IsZero() {
		return jobs.Staged{}, jobs.ErrInvalid
	}
	if err := s.driver.requireReady(); err != nil {
		return jobs.Staged{}, err
	}
	if placement.IsZero() || placement.Namespace().Digest() != s.driver.namespace.Digest() {
		return jobs.Staged{}, jobs.RejectPlacement(jobs.ErrInvalid)
	}
	for attempt := 0; attempt < 3; attempt++ {
		result, err := s.driver.placeInTx(ctx, s.tx, placement)
		if err == nil {
			return jobs.NewStaged(s.transaction, result)
		}
		if !errors.Is(err, errIntentConflict) && !errors.Is(err, errCandidateConflict) {
			return jobs.Staged{}, placementError(err)
		}
	}
	return jobs.Staged{}, jobs.RejectPlacement(jobs.ErrConflict)
}

func (*TxStager) String() string { return "[PostgreSQL staged job sender]" }

func (s *TxStager) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, s.String())
}
