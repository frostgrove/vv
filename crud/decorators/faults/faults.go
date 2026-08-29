// Package faults enriches a classified driver error with what only the
// repository layer knows: which verb was running, which entity it was running
// against, and which model field a column names.
//
// The adapters classify — a refused statement comes back an *errs.Fault
// carrying a code, a kind and one violation marked OriginState with whatever
// the driver named ([[FL-014]]). What they cannot fill is the path, because a
// column is meaningless without the table it belongs to, and an adapter has no
// crud.Meta. That is this decorator's one hop of [[D-043]]'s chain.
//
// # Order
//
// It is the innermost middleware — last in the crud.Chain list, so it wraps the
// repository directly:
//
//	users := Users.Bind(db, security.Gate(policy), faults.Enrich[User, int64]())
//
// Two reasons, and both are load-bearing. Every driver error is enriched before
// anything above can see it, so a service layer's own wrapping does not have to
// know about faults. And the gate's refusals pass through untouched: a 403 is
// not a driver error and there is nothing here to add to it.
//
// # What it does not do
//
// It never invents a fault. An error that is not one is returned exactly as it
// arrived — a decorator that manufactured faults would turn every closed pool
// into a structured 500 that looked classified.
//
// It never invents a path either. A column from another table, an unknown
// column, or no column at all leaves the path nil and marks the violation
// approximate. A column name in `field` would be a live [[D-044]] breach: the
// path is the one thing that is rendered.
package faults

import (
	"context"
	"strings"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/errs"
)

// Enrich builds the middleware. Both type parameters have to be written at the
// call site because nothing else in the signature carries them:
//
//	faults.Enrich[User, int64]()
//
// With no options it does exactly what it always did. [WithProbe] is what turns
// one violation into every violation the payload caused:
//
//	faults.Enrich[User, int64](faults.WithProbe(probe.Full(cat)))
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
	// src is the datasource the probe runs its own statement on, and nil when no
	// probe is wired.
	source     crud.Source
	probes     map[string]probeCfg
	onProbeErr func(op string, err error)
}

// Next hands back the Core this enricher wraps, so a chain with faults in the
// middle stays walkable ([[crud.Nexter]]).
func (this *enricher[M, ID]) Next() crud.Core[M, ID] { return this.Core }

// enrich is the whole of this package. Everything below it is one line per verb.
//
// The fault is copied rather than written to. A *Fault is a value two
// goroutines may render at once and [[D-042]] treats it as immutable; the
// adapter that produced it may also have handed the same pointer to a caller
// who already wrapped it.
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

// finish is the last hop: the verb, the entity, the column-to-field translation
// and the order the body renders in.
//
// partial is set by the caller when something was cut out before the fault got
// here — a savepoint budget refused, a probe that failed. A capped answer says
// so rather than listing four violations in a way that implies there are four
// ([[D-042]]).
func (this *enricher[M, ID]) finish(op string, f *errs.Fault, partial bool) error {
	g := *f
	g.Violations = make([]errs.Violation, len(f.Violations))
	copy(g.Violations, f.Violations)

	// Only when empty. A service layer that already said which command this was
	// knows better than the verb does.
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
	// One order for the fault and for the body. crud/http/crudhttp sorts what it
	// renders, so without this a consumer reading Fault.Violations would read a
	// different order from the one it was about to serialise.
	errs.SortViolations(g.Violations)

	// The copy has to keep wrapping what the original wrapped, or errors.Is
	// stops finding crud.ErrConflict one layer above the adapter that attached
	// it ([[D-038]]). Fault.Unwrap is the only reader of that list and it is
	// unexported, so the copy carries it by being a copy.
	return &g
}

// resolve is the hop: constraint / table / column -> model field.
//
// Through crud.Meta and never crud.Schema. Schema is cached per type and
// table-independent, so it cannot tell two databases' `users` apart, and a
// process holds several ([[UC-012]], [[D-043]]).
func (this *enricher[M, ID]) resolve(v *errs.Violation) {
	if len(v.Path) > 0 || this.meta == nil {
		return // already translated by a layer closer to the driver
	}
	if v.Source.Table == "" || len(v.Source.Columns) == 0 {
		return // nothing to translate; not knowing is not being wrong
	}
	p, ok := this.resolvePath(v.Source.Table, v.Source.Columns)
	if !ok {
		v.Approximate = true
		return
	}
	v.Path = p
}

// resolvePath is the hop on its own, so the probe can compose a row index in
// front of it without learning what a model field is called ([[D-043]]). It is
// handed in as probe.Request.Resolve.
func (this *enricher[M, ID]) resolvePath(table string, columns []string) (errs.Path, bool) {
	if this.meta == nil {
		return nil, false
	}
	// Folded rather than compared byte for byte: PostgreSQL lowercases an
	// unquoted identifier, so a model declared `Users` and a driver reporting
	// `users` are one table.
	if !strings.EqualFold(table, this.meta.Table) {
		// The right column name on the wrong table. Two tables in one database
		// have a `name`, and translating this one would name a field of a model
		// that had nothing to do with the write.
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
	// A composite key gets one violation at the deepest common ancestor of the
	// fields it spans, which at this hop is the empty path unless they share a
	// prefix. One error and one message, and the message can say what is
	// actually true — "this slug is taken in this workspace". The per-column
	// form is what a form-binding UI wants and it says two things that are each
	// false on their own, so it is a policy nothing asks for yet.
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

// ---------------------------------------------------------------------------
// the seam, verb by verb
//
// Every method of crud.Core that can fail is here. [[D-030]] makes that an
// obligation rather than a courtesy, and TestEveryVerbIsDecorated reads the
// interface's own method set so a verb added later reddens rather than
// silently skipping enrichment.

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
	err := this.probed(ctx, "Save", pc, this.insertRequest(false, m), func(ctx context.Context) error {
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
	return this.probed(ctx, "SaveOnly", pc, this.insertRequest(false, m),
		func(ctx context.Context) error { return this.Core.SaveOnly(ctx, m) })
}

// SaveScoped explicitly preserves the internal conditional-save capability for
// a security gate wrapped outside faults. It is intentionally a narrow forward:
// probing a speculative duplicate-key write would execute a different statement
// from the one whose snapshot security approved. Driver faults from the actual
// statement are still enriched as Save faults.
func (this *enricher[M, ID]) SaveScoped(ctx context.Context, m *M, save *crud.ScopedSave[M]) error {
	err, ok := crud.SaveScopedOf(this.Core, ctx, m, save)
	if !ok {
		return &crud.SchemaError{Model: this.meta.Name, Reason: "inner core cannot perform a scoped Save atomically"}
	}
	return this.enrich("Save", err)
}

// SaveScopedOnly forwards security's internal write-only capability without
// probing a speculative conditional write.
func (this *enricher[M, ID]) SaveScopedOnly(ctx context.Context, m *M, save *crud.ScopedSave[M]) error {
	err, ok := crud.SaveScopedOnlyOf(this.Core, ctx, m, save)
	if !ok {
		return &crud.SchemaError{Model: this.meta.Name, Reason: "inner core cannot perform a scoped SaveOnly atomically"}
	}
	return this.enrich("SaveOnly", err)
}

func (this *enricher[M, ID]) SaveAll(ctx context.Context, ms []*M) error {
	pc, ok := this.probes["SaveAll"]
	if !ok {
		return this.enrich("SaveAll", this.Core.SaveAll(ctx, ms))
	}
	return this.probed(ctx, "SaveAll", pc, this.insertRequest(true, ms...),
		func(ctx context.Context) error { return this.Core.SaveAll(ctx, ms) })
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
