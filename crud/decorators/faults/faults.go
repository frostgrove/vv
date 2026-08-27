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
func Enrich[M any, ID comparable](opts ...Option) crud.Middleware[M, ID] {
	var s settings
	for _, o := range opts {
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
	src        crud.Source
	probes     map[string]probeCfg
	onProbeErr func(op string, err error)
}

// Next hands back the Core this enricher wraps, so a chain with faults in the
// middle stays walkable ([[crud.Nexter]]).
func (e *enricher[M, ID]) Next() crud.Core[M, ID] { return e.Core }

// enrich is the whole of this package. Everything below it is one line per verb.
//
// The fault is copied rather than written to. A *Fault is a value two
// goroutines may render at once and [[D-042]] treats it as immutable; the
// adapter that produced it may also have handed the same pointer to a caller
// who already wrapped it.
func (e *enricher[M, ID]) enrich(op string, err error) error {
	if err == nil {
		return nil
	}
	f, ok := errs.AsFault(err)
	if !ok {
		return err
	}
	return e.finish(op, f, false)
}

// finish is the last hop: the verb, the entity, the column-to-field translation
// and the order the body renders in.
//
// partial is set by the caller when something was cut out before the fault got
// here — a savepoint budget refused, a probe that failed. A capped answer says
// so rather than listing four violations in a way that implies there are four
// ([[D-042]]).
func (e *enricher[M, ID]) finish(op string, f *errs.Fault, partial bool) error {
	g := *f
	g.Violations = make([]errs.Violation, len(f.Violations))
	copy(g.Violations, f.Violations)

	// Only when empty. A service layer that already said which command this was
	// knows better than the verb does.
	if g.Op == "" {
		g.Op = op
	}
	if g.Entity == "" && e.meta != nil {
		g.Entity = e.meta.Schema.Name
	}
	if partial {
		g.Partial = true
	}
	for i := range g.Violations {
		e.resolve(&g.Violations[i])
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
func (e *enricher[M, ID]) resolve(v *errs.Violation) {
	if len(v.Path) > 0 || e.meta == nil {
		return // already translated by a layer closer to the driver
	}
	if v.Source.Table == "" || len(v.Source.Columns) == 0 {
		return // nothing to translate; not knowing is not being wrong
	}
	p, ok := e.resolvePath(v.Source.Table, v.Source.Columns)
	if !ok {
		v.Approximate = true
		return
	}
	v.Path = p
}

// resolvePath is the hop on its own, so the probe can compose a row index in
// front of it without learning what a model field is called ([[D-043]]). It is
// handed in as probe.Request.Resolve.
func (e *enricher[M, ID]) resolvePath(table string, columns []string) (errs.Path, bool) {
	if e.meta == nil {
		return nil, false
	}
	// Folded rather than compared byte for byte: PostgreSQL lowercases an
	// unquoted identifier, so a model declared `Users` and a driver reporting
	// `users` are one table.
	if !strings.EqualFold(table, e.meta.Table) {
		// The right column name on the wrong table. Two tables in one database
		// have a `name`, and translating this one would name a field of a model
		// that had nothing to do with the write.
		return nil, false
	}
	paths := make([]errs.Path, 0, len(columns))
	for _, col := range columns {
		f := e.meta.Schema.Field(col)
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

func (e *enricher[M, ID]) GetByID(ctx context.Context, id ID, opts ...crud.Option) (M, error) {
	m, err := e.Core.GetByID(ctx, id, opts...)
	return m, e.enrich("GetByID", err)
}

func (e *enricher[M, ID]) Get(ctx context.Context, opts ...crud.Option) (crud.PaginatedResponse[M], error) {
	p, err := e.Core.Get(ctx, opts...)
	return p, e.enrich("Get", err)
}

func (e *enricher[M, ID]) GetAll(ctx context.Context, opts ...crud.Option) ([]M, error) {
	ms, err := e.Core.GetAll(ctx, opts...)
	return ms, e.enrich("GetAll", err)
}

func (e *enricher[M, ID]) First(ctx context.Context, opts ...crud.Option) (M, error) {
	m, err := e.Core.First(ctx, opts...)
	return m, e.enrich("First", err)
}

func (e *enricher[M, ID]) Save(ctx context.Context, m *M) (M, error) {
	var saved M
	pc, ok := e.probes["Save"]
	if !ok {
		saved, err := e.Core.Save(ctx, m)
		return saved, e.enrich("Save", err)
	}
	err := e.probed(ctx, "Save", pc, e.insertRequest(false, m), func(ctx context.Context) error {
		var err error
		saved, err = e.Core.Save(ctx, m)
		return err
	})
	return saved, err
}

func (e *enricher[M, ID]) SaveOnly(ctx context.Context, m *M) error {
	pc, ok := e.probes["SaveOnly"]
	if !ok {
		return e.enrich("SaveOnly", e.Core.SaveOnly(ctx, m))
	}
	return e.probed(ctx, "SaveOnly", pc, e.insertRequest(false, m),
		func(ctx context.Context) error { return e.Core.SaveOnly(ctx, m) })
}

// SaveScoped explicitly preserves the internal conditional-save capability for
// a security gate wrapped outside faults. It is intentionally a narrow forward:
// probing a speculative duplicate-key write would execute a different statement
// from the one whose snapshot security approved. Driver faults from the actual
// statement are still enriched as Save faults.
func (e *enricher[M, ID]) SaveScoped(ctx context.Context, m *M, save crud.ScopedSave[M]) error {
	err, ok := crud.SaveScopedOf(e.Core, ctx, m, save)
	if !ok {
		return &crud.SchemaError{Model: e.meta.Name, Reason: "inner core cannot perform a scoped Save atomically"}
	}
	return e.enrich("Save", err)
}

// SaveScopedOnly forwards security's internal write-only capability without
// probing a speculative conditional write.
func (e *enricher[M, ID]) SaveScopedOnly(ctx context.Context, m *M, save crud.ScopedSave[M]) error {
	err, ok := crud.SaveScopedOnlyOf(e.Core, ctx, m, save)
	if !ok {
		return &crud.SchemaError{Model: e.meta.Name, Reason: "inner core cannot perform a scoped SaveOnly atomically"}
	}
	return e.enrich("SaveOnly", err)
}

func (e *enricher[M, ID]) SaveAll(ctx context.Context, ms []*M) error {
	pc, ok := e.probes["SaveAll"]
	if !ok {
		return e.enrich("SaveAll", e.Core.SaveAll(ctx, ms))
	}
	return e.probed(ctx, "SaveAll", pc, e.insertRequest(true, ms...),
		func(ctx context.Context) error { return e.Core.SaveAll(ctx, ms) })
}

func (e *enricher[M, ID]) Update(ctx context.Context, id ID, dto any, opts ...crud.Option) (M, error) {
	pc, ok := e.probes["Update"]
	if !ok {
		m, err := e.Core.Update(ctx, id, dto, opts...)
		return m, e.enrich("Update", err)
	}
	var m M
	err := e.probed(ctx, "Update", pc, e.updateRequest(id, dto), func(ctx context.Context) error {
		var err error
		m, err = e.Core.Update(ctx, id, dto, opts...)
		return err
	})
	return m, err
}

func (e *enricher[M, ID]) UpdateAll(ctx context.Context, dto any, opts ...crud.Option) (int64, error) {
	n, err := e.Core.UpdateAll(ctx, dto, opts...)
	return n, e.enrich("UpdateAll", err)
}

func (e *enricher[M, ID]) Aggregate(ctx context.Context, opts ...crud.Option) ([]crud.AggregateRow, error) {
	rows, err := e.Core.Aggregate(ctx, opts...)
	return rows, e.enrich("Aggregate", err)
}

func (e *enricher[M, ID]) Delete(ctx context.Context, ids ...ID) (int64, error) {
	n, err := e.Core.Delete(ctx, ids...)
	return n, e.enrich("Delete", err)
}

func (e *enricher[M, ID]) DeleteAll(ctx context.Context, opts ...crud.Option) (int64, error) {
	n, err := e.Core.DeleteAll(ctx, opts...)
	return n, e.enrich("DeleteAll", err)
}

func (e *enricher[M, ID]) Count(ctx context.Context, opts ...crud.Option) (int64, error) {
	n, err := e.Core.Count(ctx, opts...)
	return n, e.enrich("Count", err)
}

func (e *enricher[M, ID]) Exists(ctx context.Context, opts ...crud.Option) (bool, error) {
	ok, err := e.Core.Exists(ctx, opts...)
	return ok, e.enrich("Exists", err)
}

func (e *enricher[M, ID]) Tx(ctx context.Context, fn func(context.Context) error) error {
	return e.enrich("Tx", e.Core.Tx(ctx, fn))
}
