package faults

import (
	"context"
	"strings"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/errs"
)

func Enrich[M any, ID comparable](options ...Option) crud.Middleware[M, ID] {
	var s settings
	for _, o := range options {
		o(&s)
	}
	return func(next crud.Core[M, ID]) crud.Core[M, ID] {
		e := &enricher[M, ID]{Core: next, meta: next.Meta(), onProbeErr: s.onErr}
		e.declare(next, s)
		return e
	}
}

type enricher[M any, ID comparable] struct {
	crud.Core[M, ID]
	meta *crud.Meta

	source     crud.Source
	probes     map[string]probeCfg
	onProbeErr func(op string, err error)
}

func (this *enricher[M, ID]) Next() crud.Core[M, ID] { return this.Core }

func (this *enricher[M, ID]) SupportsRestore() bool {
	return this != nil && crud.SupportsRestore(this.Core)
}

func (this *enricher[M, ID]) enrich(op string, err error) error {
	if err == nil {
		return nil
	}
	f, ok := errs.AsFault(err)
	if !ok {
		return err
	}
	return this.finish(op, f, false)
}

func (this *enricher[M, ID]) finish(op string, f *errs.Fault, partial bool) error {
	g := *f
	g.Violations = make([]errs.Violation, len(f.Violations))
	copy(g.Violations, f.Violations)

	if g.Op == "" {
		g.Op = op
	}
	if g.Entity == "" && this.meta != nil {
		g.Entity = this.meta.Schema.Name
	}
	if partial {
		g.Partial = true
	}
	for i := range g.Violations {
		this.resolve(&g.Violations[i])
	}

	errs.SortViolations(g.Violations)

	return &g
}

func (this *enricher[M, ID]) resolve(v *errs.Violation) {
	if this.meta == nil {
		return
	}
	ref := this.meta.TableReference()
	if ref.Schema != "" && v.Origin == errs.OriginState {
		hasAttribution := len(v.Path) > 0 || v.Source.Schema != "" || v.Source.Table != "" ||
			len(v.Source.Columns) > 0 || v.Source.Constraint != ""
		if hasAttribution && (v.Source.Schema != ref.Schema || v.Source.Table != ref.Name) {
			v.Path = nil
			v.Approximate = true
			return
		}
	}
	if len(v.Path) > 0 {
		return
	}
	if v.Source.Table == "" || len(v.Source.Columns) == 0 {
		return
	}
	p, ok := this.resolvePath(v.Source.Table, v.Source.Columns)
	if !ok {
		v.Approximate = true
		return
	}
	v.Path = p
}

func (this *enricher[M, ID]) resolvePath(table string, columns []string) (errs.Path, bool) {
	if this.meta == nil {
		return nil, false
	}
	ref := this.meta.TableReference()

	match := table == ref.Name
	if ref.Schema == "" {
		match = strings.EqualFold(table, ref.Name)
	}
	if !match {
		return nil, false
	}
	paths := make([]errs.Path, 0, len(columns))
	for _, col := range columns {
		f := this.meta.Schema.Field(col)
		if f == nil {
			return nil, false
		}
		paths = append(paths, errs.Path{errs.Named(f.Name)})
	}

	return commonPrefix(paths), true
}

func commonPrefix(ps []errs.Path) errs.Path {
	if len(ps) == 0 {
		return nil
	}
	out := ps[0]
	for _, p := range ps[1:] {
		n := 0
		for n < len(out) && n < len(p) && out[n] == p[n] {
			n++
		}
		out = out[:n]
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (this *enricher[M, ID]) GetByID(ctx context.Context, id ID, options ...crud.Option) (M, error) {
	m, err := this.Core.GetByID(ctx, id, options...)
	return m, this.enrich("GetByID", err)
}

func (this *enricher[M, ID]) Get(ctx context.Context, options ...crud.Option) (crud.PaginatedResponse[M], error) {
	p, err := this.Core.Get(ctx, options...)
	return p, this.enrich("Get", err)
}

func (this *enricher[M, ID]) GetAll(ctx context.Context, options ...crud.Option) ([]M, error) {
	ms, err := this.Core.GetAll(ctx, options...)
	return ms, this.enrich("GetAll", err)
}

func (this *enricher[M, ID]) First(ctx context.Context, options ...crud.Option) (M, error) {
	m, err := this.Core.First(ctx, options...)
	return m, this.enrich("First", err)
}

func (this *enricher[M, ID]) Save(ctx context.Context, m *M) (M, error) {
	var saved M
	pc, ok := this.probes["Save"]
	if !ok {
		saved, err := this.Core.Save(ctx, m)
		return saved, this.enrich("Save", err)
	}
	err := this.probed(ctx, "Save", pc, this.insertRequest(false, true, m), func(ctx context.Context) error {
		var err error
		saved, err = this.Core.Save(ctx, m)
		return err
	})
	return saved, err
}

func (this *enricher[M, ID]) SaveOnly(ctx context.Context, m *M) error {
	pc, ok := this.probes["SaveOnly"]
	if !ok {
		return this.enrich("SaveOnly", this.Core.SaveOnly(ctx, m))
	}
	return this.probed(ctx, "SaveOnly", pc, this.insertRequest(false, true, m),
		func(ctx context.Context) error { return this.Core.SaveOnly(ctx, m) })
}

func (this *enricher[M, ID]) SaveScoped(ctx context.Context, m *M, save *crud.ScopedSave[M]) error {
	err, ok := crud.SaveScopedOf(this.Core, ctx, m, save)
	if !ok {
		return &crud.SchemaError{Model: this.meta.Name, Reason: "inner core cannot perform a scoped Save atomically"}
	}
	return this.enrich("Save", err)
}

func (this *enricher[M, ID]) SaveScopedOnly(ctx context.Context, m *M, save *crud.ScopedSave[M]) error {
	err, ok := crud.SaveScopedOnlyOf(this.Core, ctx, m, save)
	if !ok {
		return &crud.SchemaError{Model: this.meta.Name, Reason: "inner core cannot perform a scoped SaveOnly atomically"}
	}
	return this.enrich("SaveOnly", err)
}

func (this *enricher[M, ID]) DeleteScoped(ctx context.Context, deletion *crud.ScopedDelete[ID]) (int64, error) {
	n, err, ok := crud.DeleteScopedOf(this.Core, ctx, deletion)
	if !ok {
		return 0, &crud.SchemaError{Model: this.meta.Name, Reason: "inner core cannot perform a scoped Delete atomically"}
	}
	return n, this.enrich("Delete", err)
}

func (this *enricher[M, ID]) Restore(ctx context.Context, ids ...ID) (int64, error) {
	n, err, ok := crud.RestoreOf(this.Core, ctx, ids...)
	if !ok {
		return 0, crud.ErrNoTombstone
	}
	return n, this.enrich("Restore", err)
}

func (this *enricher[M, ID]) RestoreScoped(ctx context.Context, restore *crud.ScopedRestore[ID]) (int64, error) {
	n, err, ok := crud.RestoreScopedOf(this.Core, ctx, restore)
	if !ok {
		return 0, &crud.SchemaError{Model: this.meta.Name, Reason: "inner core cannot perform a scoped Restore atomically"}
	}
	return n, this.enrich("Restore", err)
}

func (this *enricher[M, ID]) LoadTombstones(ctx context.Context, ids []ID, scope crud.Predicate, relations *crud.RelationScopes) ([]M, error) {
	rows, err, ok := crud.LoadTombstonesOf(this.Core, ctx, ids, scope, relations)
	if !ok {
		return nil, &crud.SchemaError{Model: this.meta.Name, Reason: "inner core cannot load tombstones for Restore inspection"}
	}
	return rows, this.enrich("Restore", err)
}

func (this *enricher[M, ID]) SaveAll(ctx context.Context, ms []*M) error {
	pc, ok := this.probes["SaveAll"]
	if !ok {
		return this.enrich("SaveAll", this.Core.SaveAll(ctx, ms))
	}
	return this.probed(ctx, "SaveAll", pc, this.insertRequest(true, true, ms...),
		func(ctx context.Context) error { return this.Core.SaveAll(ctx, ms) })
}

func (this *enricher[M, ID]) InsertBatch(ctx context.Context, ms []*M, options ...crud.BatchOption) error {
	run := func(ctx context.Context) error {
		err, ok := crud.InsertBatchOf(this.Core, ctx, ms, options...)
		if !ok {
			return crud.ErrNoBatchInsertSupport
		}
		return err
	}
	pc, ok := this.probes["InsertBatch"]
	if !ok {
		return this.enrich("InsertBatch", run(ctx))
	}
	return this.probed(ctx, "InsertBatch", pc, this.insertRequest(true, false, ms...), run)
}

func (this *enricher[M, ID]) Update(ctx context.Context, id ID, dataTransferObject any, options ...crud.Option) (M, error) {
	pc, ok := this.probes["Update"]
	if !ok {
		m, err := this.Core.Update(ctx, id, dataTransferObject, options...)
		return m, this.enrich("Update", err)
	}
	var m M
	err := this.probed(ctx, "Update", pc, this.updateRequest(id, dataTransferObject), func(ctx context.Context) error {
		var err error
		m, err = this.Core.Update(ctx, id, dataTransferObject, options...)
		return err
	})
	return m, err
}

func (this *enricher[M, ID]) UpdateAll(ctx context.Context, dataTransferObject any, options ...crud.Option) (int64, error) {
	n, err := this.Core.UpdateAll(ctx, dataTransferObject, options...)
	return n, this.enrich("UpdateAll", err)
}

func (this *enricher[M, ID]) Aggregate(ctx context.Context, options ...crud.Option) ([]crud.AggregateRow, error) {
	rows, err := this.Core.Aggregate(ctx, options...)
	return rows, this.enrich("Aggregate", err)
}

func (this *enricher[M, ID]) Delete(ctx context.Context, ids ...ID) (int64, error) {
	n, err := this.Core.Delete(ctx, ids...)
	return n, this.enrich("Delete", err)
}

func (this *enricher[M, ID]) DeleteAll(ctx context.Context, options ...crud.Option) (int64, error) {
	n, err := this.Core.DeleteAll(ctx, options...)
	return n, this.enrich("DeleteAll", err)
}

func (this *enricher[M, ID]) Count(ctx context.Context, options ...crud.Option) (int64, error) {
	n, err := this.Core.Count(ctx, options...)
	return n, this.enrich("Count", err)
}

func (this *enricher[M, ID]) Exists(ctx context.Context, options ...crud.Option) (bool, error) {
	ok, err := this.Core.Exists(ctx, options...)
	return ok, this.enrich("Exists", err)
}

func (this *enricher[M, ID]) Tx(ctx context.Context, fn func(context.Context) error) error {
	return this.enrich("Tx", this.Core.Tx(ctx, fn))
}
