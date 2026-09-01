package jobspg

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/frostgrove/vv/jobs"
)

type TxFencer struct {
	driver *Driver
	tx     *sql.Tx
}

var _ jobs.LeaseFence = (*TxFencer)(nil)

func (d *Driver) Fencer(tx *sql.Tx) (*TxFencer, error) {
	if err := d.requireReady(); err != nil {
		return nil, err
	}
	if tx == nil {
		return nil, fmt.Errorf("jobspg: %w: transaction is required", jobs.ErrInvalid)
	}
	return &TxFencer{driver: d, tx: tx}, nil
}

func (fencer *TxFencer) Fence(ctx context.Context, lease jobs.LeaseRef) error {
	if fencer == nil || fencer.driver == nil || fencer.tx == nil || lease.IsZero() {
		return jobs.ErrInvalid
	}
	if err := fencer.driver.requireReady(); err != nil {
		return err
	}
	if lease.Backend() != fencer.driver.description.ID() {
		return jobs.ErrLeaseLost
	}
	held, err := fencer.driver.repo.fenceLease(ctx, fencer.tx, fencer.driver.namespace, lease)
	if err != nil {
		return err
	}
	if !held {
		return jobs.ErrLeaseLost
	}
	return nil
}
