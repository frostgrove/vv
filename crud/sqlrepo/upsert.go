package sqlrepo

import (
	"context"
	"fmt"

	"github.com/frostgrove/vv/crud"
)

func (this *repository[M, ID, U]) needsConditionalUpsert(m *M, operation string) (bool, error) {
	if m == nil {
		return false, &crud.SchemaError{Model: this.meta.Name, Reason: operation + " called with a nil model"}
	}
	hasID, err := this.meta.HasID(m)
	if err != nil {
		return false, err
	}
	if !hasID {
		return false, nil
	}
	return this.writeScope != nil || !this.upsertTargetsPK, nil
}

func (this *repository[M, ID, U]) inWriteTx(ctx context.Context, fn func(context.Context) error) error {
	if _, bound := crud.ExecutorFor(ctx, this.source); bound {
		return fn(ctx)
	}
	return crud.InNewTx(ctx, this.source, fn)
}

func (this *repository[M, ID, U]) conditionalSave(ctx context.Context, m *M, guard crud.Predicate, bumpVersion bool) (M, error) {
	var zero M
	var saved M
	err := this.inWriteTx(ctx, func(tx context.Context) error {
		if err := this.upsertByPrimaryKey(tx, m, guard, bumpVersion); err != nil {
			return err
		}
		id, err := this.meta.ID(m)
		if err != nil {
			return err
		}
		return this.refreshByID(tx, &saved, id, this.writeScope, this.bp.relScopes)
	})
	if err != nil {
		return zero, err
	}
	return saved, nil
}

// The row this writes has to be reached by its primary key and never by any other unique
// index, and the caller's scope has to hold for the update half without narrowing the insert
// half. No dialect offers that as one statement, so the sequence is update, probe, insert
// inside one transaction. The probe exists because an engine that counts changed rows rather
// than matched ones reports zero for an update that found its row and rewrote it with the
// same values. A concurrent insert of the same key between the probe and the insert surfaces
// as the engine's duplicate-key error, which is the honest answer: a locking read would take
// a gap lock that two inserts into the same gap deadlock on rather than serialise.
func (this *repository[M, ID, U]) upsertByPrimaryKey(ctx context.Context, m *M, guard crud.Predicate, bumpVersion bool) error {
	if m == nil {
		return &crud.SchemaError{Model: this.meta.Name, Reason: "Save called with a nil model"}
	}
	if err := this.checkBindBudget(len(this.meta.Insert), "Save"); err != nil {
		return err
	}
	id, err := this.meta.ID(m)
	if err != nil {
		return err
	}
	reachable := crud.And(crud.Eq(this.meta.PK.Name, id), this.writeScope)
	target := crud.And(reachable, guard)

	written, err := this.updateFullRow(ctx, m, target, bumpVersion)
	if err != nil {
		return err
	}
	if written > 0 {
		return nil
	}
	if this.updateCanMissAChangelessRow() {
		present, err := this.rowIsPresent(ctx, target)
		if err != nil {
			return err
		}
		if present {
			return nil
		}
	}
	if guard != nil {
		outdated, err := this.rowIsPresent(ctx, reachable)
		if err != nil {
			return err
		}
		if outdated {
			return crud.ErrStaleVersion
		}
	}
	return this.insertFullRow(ctx, m)
}

func (this *repository[M, ID, U]) updateCanMissAChangelessRow() bool {
	return len(this.meta.Update) == 0 || crud.UpdateCountsChangedRowsOnly(this.d)
}

func (this *repository[M, ID, U]) updateFullRow(ctx context.Context, m *M, within crud.Predicate, bumpVersion bool) (int64, error) {
	if len(this.meta.Update) == 0 {
		return 0, nil
	}
	values, err := this.meta.Values(m, this.meta.Update)
	if err != nil {
		return 0, err
	}
	b := crud.NewSQL(this.d, this.meta).RelationScopes(this.bp.relScopes).Raw("UPDATE ").Table().Raw(" SET ")
	for i, f := range this.meta.Update {
		if i > 0 {
			b.Raw(", ")
		}
		b.Ident(f.Column).Raw(" = ").Bind(values[i])
	}
	if bumpVersion && this.meta.Version != nil {
		b.Raw(", ").Ident(this.meta.Version.Column).Raw(" = ").Ident(this.meta.Version.Column).Raw(" + 1")
	}
	q, args, err := b.Where(within).Done()
	if err != nil {
		return 0, err
	}
	response, err := this.exec(ctx).Exec(ctx, q, args...)
	if err != nil {
		return 0, err
	}
	return response.RowsAffected, nil
}

func (this *repository[M, ID, U]) rowIsPresent(ctx context.Context, within crud.Predicate) (bool, error) {
	q, args, err := crud.NewSQL(this.d, this.meta).RelationScopes(this.bp.relScopes).
		Raw("SELECT 1 FROM ").Table().Where(within).Raw(" LIMIT 1").Done()
	if err != nil {
		return false, err
	}
	rows, err := this.exec(ctx).Query(ctx, q, args...)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	if rows.Next() {
		return true, rows.Err()
	}
	return false, rows.Err()
}

func (this *repository[M, ID, U]) insertFullRow(ctx context.Context, m *M) error {
	args, err := this.meta.Values(m, this.meta.Insert)
	if err != nil {
		return err
	}
	_, err = this.exec(ctx).Exec(ctx, this.insertFull, args...)
	return err
}

func (this *repository[M, ID, U]) checkBindBudget(bound int, operation string) error {
	limit := crud.BindLimit(this.d)
	if bound <= limit {
		return nil
	}
	return &crud.SchemaError{Model: this.meta.Name, Reason: fmt.Sprintf(
		"%s needs %d bound values, but dialect %q permits at most %d; use a narrower persistence model or a driver bulk capability",
		operation, bound, this.d.Name(), limit)}
}
