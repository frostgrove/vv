package faults

import (
	"context"
	"fmt"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/probe"
	"github.com/frostgrove/vv/errs"
)

// An Option wires the probe into the decorator.
type Option func(*settings)

type settings struct {
	byOp  map[string]probe.Handler
	src   crud.Source
	onErr func(op string, err error)
}

func (s *settings) set(op string, h probe.Handler) {
	if s.byOp == nil {
		s.byOp = map[string]probe.Handler{}
	}
	s.byOp[op] = h
}

// WithProbe wires a handler onto the verbs it is worth paying for: Save and
// Update, the two single-row writes.
//
// The bulk verbs and everything else keep the cheap answer. A batch is where the
// cost multiplies and where a client is least likely to be a form, so
// probe.Simple is the default there — use [WithProbeFor] to say otherwise.
func WithProbe(h probe.Handler) Option {
	return func(s *settings) {
		s.set("Save", h)
		s.set("Update", h)
	}
}

// WithProbeFor wires a handler onto one verb, by the name the fault's Op
// carries: "Save", "SaveAll", "Update", "UpdateAll", "Delete", "DeleteAll".
//
// Order matters against [WithProbe], which sets two verbs at once: the last
// option wins, so put the narrower one second.
func WithProbeFor(op string, h probe.Handler) Option {
	return func(s *settings) { s.set(op, h) }
}

// WithSource names the datasource the probe runs its own statement on.
//
// It is needed only where the decorator is not the innermost middleware. Where
// it is — which is the documented order — the repository underneath is
// crud.Sourced and answers for itself.
func WithSource(src crud.Source) Option {
	return func(s *settings) { s.src = src }
}

// WithProbeError hands probe failures somewhere. A probe that fails keeps the
// driver's violation and marks the answer partial ([[D-042]]), so the failure
// never reaches the client and would otherwise reach nobody at all.
func WithProbeError(fn func(op string, err error)) Option {
	return func(s *settings) { s.onErr = fn }
}

// probeCfg is one verb's handler and what the decorator has to do around the
// write for it.
type probeCfg struct {
	h          probe.Handler
	savepoints bool
	budget     int
}

// declare binds every wired handler to this model and refuses at Bind time.
//
// It panics, like sqlrepo.Define and security.ScopeField before it: a probe over a
// table the catalog does not know cannot start working later, and [[D-021]] puts
// that failure at start-up rather than at the first collision.
func (e *enricher[M, ID]) declare(next crud.Core[M, ID], s settings) {
	if len(s.byOp) == 0 {
		return
	}
	e.src = s.src
	if e.src == nil {
		// crud.SourceOf and not a type assertion on the layer directly below.
		// The assertion made the order decorators were listed in decide whether
		// the probe worked: security.Gate between faults and the repository
		// answered no, because an interface embedded in a struct promotes only
		// its own method set — so a chain that was correct in every other
		// respect refused at start-up for a reason about Go's type system
		// rather than about the wiring. The walk asks the whole chain.
		src, ok := crud.SourceOf(next)
		if !ok {
			panic(fmt.Sprintf("faults: a probe is wired for %s but nothing in the chain underneath "+
				"says which datasource it is bound to. A decorator between this one and the "+
				"repository has to forward what it wraps with Next() — crud.Base does — or "+
				"name the source with faults.WithSource", e.entity()))
		}
		e.src = src
	}
	e.probes = make(map[string]probeCfg, len(s.byOp))
	for op, h := range s.byOp {
		if d, ok := h.(probe.Declarer); ok {
			bound, err := d.Declare(e.meta)
			if err != nil {
				panic(fmt.Sprintf("faults: declaring the %s probe for %s: %v", op, e.entity(), err))
			}
			h = bound
		}
		cfg := probeCfg{h: h}
		if sp, ok := h.(probe.Savepointer); ok {
			cfg.savepoints, cfg.budget = sp.Savepoints()
		}
		e.probes[op] = cfg
	}
}

func (e *enricher[M, ID]) entity() string {
	if e.meta == nil {
		return "an unnamed model"
	}
	return e.meta.Schema.Name
}

// probed runs one write with the probe around it.
//
// The savepoint wraps the write rather than the failure, and it has to: a
// savepoint cannot be taken after the fact, and on an engine that poisons a
// transaction there is nothing left to run the probe on. This is the only part
// of the subsystem that touches the happy path, which is why it is opt-in.
func (e *enricher[M, ID]) probed(ctx context.Context, op string, pc probeCfg, req probe.Request, run func(context.Context) error) error {
	if pc.savepoints {
		sp, res := e.savepoint(ctx, pc.budget)
		switch res {
		case spTaken:
			err := run(ctx)
			if err == nil {
				// RELEASE, every time. Left unreleased, each savepoint is a
				// subtransaction, and a PostgreSQL transaction holding more than
				// 64 of them forces pg_subtrans lookups on every reader in the
				// cluster.
				return e.enrich(op, sp.Commit(ctx))
			}
			if rbErr := sp.Rollback(ctx); rbErr == nil {
				req.Recovered = true
			}
			return e.enrichProbed(ctx, op, err, pc, req, false)
		case spRefused:
			// Past the budget. The write runs unwrapped, the probe will decline
			// on an engine that poisons, and the answer says it is incomplete.
			return e.enrichProbed(ctx, op, run(ctx), pc, req, true)
		}
	}
	return e.enrichProbed(ctx, op, run(ctx), pc, req, false)
}

func (e *enricher[M, ID]) enrichProbed(ctx context.Context, op string, err error, pc probeCfg, req probe.Request, capped bool) error {
	if err == nil {
		return nil
	}
	f, ok := errs.AsFault(err)
	if !ok {
		return err // never invent a fault
	}
	req.Op, req.Fault, req.Meta, req.Source, req.Resolve = op, f, e.meta, e.src, e.resolvePath
	out, perr := pc.h.Enrich(ctx, req)
	if perr != nil {
		capped = true
		if e.onProbeErr != nil {
			e.onProbeErr(op, perr)
		}
	}
	if out == nil {
		// A handler that answered nil would have suppressed a truthful refusal.
		out = f
	}
	return e.finish(op, out, capped)
}

type spResult uint8

const (
	spNotNeeded spResult = iota
	spTaken
	spRefused
)

// savepoint decides whether this write is wrapped in one.
//
// Four things have to be true, and each of them is a rule from somewhere else.
// There has to be a transaction; vv has to own it, because ROLLBACK TO
// SAVEPOINT in the middle of somebody else's unit of work can discard work its
// owner has not finished with; the engine has to be one whose transaction a
// failed statement poisons, or there is nothing to restore; and the budget has
// to allow it.
//
// It never issues SAVEPOINT itself. crudsql's Tx.Begin already does, off a
// counter it owns, and a hand-rolled name can collide with one the seam issued
// ([[FL-009]]).
func (e *enricher[M, ID]) savepoint(ctx context.Context, budget int) (crud.Tx, spResult) {
	ex, inTx, owned := crud.OwnedExecutorFor(ctx, e.src)
	if !inTx || !owned {
		return nil, spNotNeeded
	}
	if sr, ok := e.src.Dialect().(crud.StatementRollback); ok && sr.RollsBackStatementOnly() {
		return nil, spNotNeeded
	}
	b, ok := crud.BeginnerOf(ex)
	if !ok {
		return nil, spRefused
	}
	n, ok := crud.ClaimSavepoint(ctx, e.src)
	if !ok || n > int64(budget) {
		return nil, spRefused
	}
	tx, err := b.Begin(ctx)
	if err != nil {
		return nil, spRefused
	}
	return tx, spTaken
}

// insertRequest reads the rows a Save or SaveAll was about to write.
//
// The columns are the ones the statement carries, which is what decides which
// constraints are even relevant. Values go through crud.ElemValue so a null
// Opt arrives as nil rather than as an Opt that happens to be empty — the probe
// keys and null-guards on it.
func (e *enricher[M, ID]) insertRequest(batch bool, ms ...*M) probe.Request {
	req := probe.Request{Batch: batch}
	if e.meta == nil || len(ms) == 0 {
		return req
	}
	for _, m := range ms {
		if m == nil {
			return probe.Request{Batch: batch}
		}
		hasID, err := e.meta.HasID(m)
		if err != nil {
			return probe.Request{Batch: batch}
		}
		fields := e.meta.InsertGen
		if hasID {
			fields = e.meta.Insert
		}
		vals, err := e.meta.Values(m, fields)
		if err != nil {
			return probe.Request{Batch: batch}
		}
		row := probe.Row{Values: make(map[string]any, len(fields))}
		for i, f := range fields {
			row.Values[f.Column] = crud.ElemValue(vals[i])
		}
		if hasID {
			id, err := e.meta.ID(m)
			if err != nil {
				return probe.Request{Batch: batch}
			}
			row.ID, row.HasID = crud.ElemValue(id), true
			// A keyed Save is the upsert path ([[D-011]]), so the engine
			// swallowed whatever its own conflict clause covers.
			req.Upsert = true
		}
		req.Rows = append(req.Rows, row)
	}
	return req
}

// updateRequest reads the columns an Update was about to write.
//
// Only the columns, and deliberately: [[D-010]] drops a field whose value
// already matches the stored one, so the unchanged half of a composite key has
// no value here at all. The probe reads that half out of the stored row in SQL
// rather than being handed a copy that may already be stale.
func (e *enricher[M, ID]) updateRequest(id ID, dto any) probe.Request {
	if e.meta == nil {
		return probe.Request{}
	}
	changes, err := crud.DefinedChanges(e.meta.Schema, dto)
	if err != nil {
		return probe.Request{}
	}
	row := probe.Row{Values: make(map[string]any, len(changes)), ID: id, HasID: true}
	for _, c := range changes {
		row.Values[c.Field.Column] = c.Value
	}
	return probe.Request{Rows: []probe.Row{row}, Stored: true}
}
