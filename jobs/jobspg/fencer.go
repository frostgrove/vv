package jobspg

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/adapter/crudsql"
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

func (d *Driver) InFencedTx(
	ctx context.Context,
	controller jobs.AttemptController,
	before func(context.Context) error,
	effect func(context.Context) error,
) error {
	if err := d.requireReady(); err != nil {
		return err
	}
	if ctx == nil || controller == nil || effect == nil {
		return fmt.Errorf("jobspg: %w: fenced transaction inputs are required", jobs.ErrInvalid)
	}
	if d.source == nil {
		return fmt.Errorf("jobspg: %w: CRUD source is not configured", jobs.ErrUnsupported)
	}
	return crud.InNewTx(ctx, d.source, func(txContext context.Context) error {
		if before != nil {
			if err := before(txContext); err != nil {
				return err
			}
		}
		tx, ok := crudsql.TransactionFor(txContext, d.source)
		if !ok {
			return unsupportedCRUDTransaction()
		}
		fencer, err := d.Fencer(tx)
		if err != nil {
			return err
		}
		if err := controller.Guard(txContext, fencer); err != nil {
			return err
		}
		return effect(txContext)
	})
}

func unsupportedCRUDTransaction() error {
	return fmt.Errorf("jobspg: %w: ambient CRUD transaction is not extractable as database/sql", jobs.ErrUnsupported)
}
