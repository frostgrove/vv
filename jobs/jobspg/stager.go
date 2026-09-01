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

func (d *Driver) Stager(tx *sql.Tx) (*TxStager, error) {
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
