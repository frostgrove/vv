package faults

import (
	"context"
	"fmt"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/probe"
	"github.com/frostgrove/vv/errs"
)

type Option func(*settings)

type settings struct {
	byOp   map[string]probe.Handler
	source crud.Source
	onErr  func(op string, err error)
}

func (this *settings) set(op string, h probe.Handler) {
	if this.byOp == nil {
		this.byOp = map[string]probe.Handler{}
	}
	this.byOp[op] = h
}

func WithProbe(h probe.Handler) Option {
	return func(s *settings) {
		s.set("Save", h)
		s.set("SaveOnly", h)
		s.set("Update", h)
	}
}

func WithProbeFor(op string, h probe.Handler) Option {
	return func(s *settings) { s.set(op, h) }
}

func WithSource(source crud.Source) Option {
	return func(s *settings) { s.source = source }
}

func WithProbeError(fn func(op string, err error)) Option {
	return func(s *settings) { s.onErr = fn }
}

type probeCfg struct {
	h          probe.Handler
	savepoints bool
	budget     int
}

func (this *enricher[M, ID]) declare(next crud.Core[M, ID], s settings) {
	if len(s.byOp) == 0 {
		return
	}
	this.source = s.source
	if this.source == nil {
		source, ok := crud.SourceOf(next)
		if !ok {
			panic(fmt.Sprintf("faults: a probe is wired for %s but nothing in the chain underneath "+
				"says which datasource it is bound to. A decorator between this one and the "+
				"repository has to forward what it wraps with Next() — crud.Base does — or "+
				"name the source with faults.WithSource", this.entity()))
		}
		this.source = source
	}
	this.probes = make(map[string]probeCfg, len(s.byOp))
	for op, h := range s.byOp {
		if d, ok := h.(probe.Declarer); ok {
			bound, err := d.Declare(this.meta)
			if err != nil {
				panic(fmt.Sprintf("faults: declaring the %s probe for %s: %v", op, this.entity(), err))
			}
			h = bound
		}
		config := probeCfg{h: h}
		if sp, ok := h.(probe.Savepointer); ok {
			config.savepoints, config.budget = sp.Savepoints()
		}
		this.probes[op] = config
	}
}

func (this *enricher[M, ID]) entity() string {
	if this.meta == nil {
		return "an unnamed model"
	}
	return this.meta.Schema.Name
}

func (this *enricher[M, ID]) probed(ctx context.Context, op string, pc probeCfg, request *probe.Request, run func(context.Context) error) error {
	if pc.savepoints {
		sp, response := this.savepoint(ctx, pc.budget)
		switch response {
		case spTaken:
			err := run(ctx)
			if err == nil {
				return this.enrich(op, sp.Commit(ctx))
			}
			if rbErr := sp.Rollback(ctx); rbErr == nil {
				request.Recovered = true
			}
			return this.enrichProbed(ctx, op, err, pc, request, false)
		case spRefused:
			return this.enrichProbed(ctx, op, run(ctx), pc, request, true)
		}
	}
	return this.enrichProbed(ctx, op, run(ctx), pc, request, false)
}

func (this *enricher[M, ID]) enrichProbed(ctx context.Context, op string, err error, pc probeCfg, request *probe.Request, capped bool) error {
	if err == nil {
		return nil
	}
	f, ok := errs.AsFault(err)
	if !ok {
		return err
	}
	request.Op, request.Fault, request.Meta, request.Source, request.Resolve = op, f, this.meta, this.source, this.resolvePath
	out, perr := pc.h.Enrich(ctx, request)
	if perr != nil {
		capped = true
		if this.onProbeErr != nil {
			this.onProbeErr(op, perr)
		}
	}
	if out == nil {
		out = f
	}
	return this.finish(op, out, capped)
}

type spResult uint8

const (
	spNotNeeded spResult = iota
	spTaken
	spRefused
)

func (this *enricher[M, ID]) savepoint(ctx context.Context, budget int) (crud.Tx, spResult) {
	ex, inTx, owned := crud.OwnedExecutorFor(ctx, this.source)
	if !inTx || !owned {
		return nil, spNotNeeded
	}
	if sr, ok := this.source.Dialect().(crud.StatementRollback); ok && sr.RollsBackStatementOnly() {
		return nil, spNotNeeded
	}
	b, ok := crud.BeginnerOf(ex)
	if !ok {
		return nil, spRefused
	}
	n, ok := crud.ClaimSavepoint(ctx, this.source)
	if !ok || n > int64(budget) {
		return nil, spRefused
	}
	tx, err := b.Begin(ctx)
	if err != nil {
		return nil, spRefused
	}
	return tx, spTaken
}

func (this *enricher[M, ID]) insertRequest(batch, upsert bool, ms ...*M) *probe.Request {
	request := &probe.Request{Batch: batch}
	if this.meta == nil || len(ms) == 0 {
		return request
	}
	for _, m := range ms {
		if m == nil {
			return &probe.Request{Batch: batch}
		}
		hasID, err := this.meta.HasID(m)
		if err != nil {
			return &probe.Request{Batch: batch}
		}
		fields := this.meta.InsertGen
		if hasID {
			fields = this.meta.Insert
		}
		vals, err := this.meta.Values(m, fields)
		if err != nil {
			return &probe.Request{Batch: batch}
		}
		row := probe.Row{Values: make(map[string]any, len(fields))}
		for i, f := range fields {
			row.Values[f.Column] = crud.ElemValue(vals[i])
		}
		if hasID && upsert {
			id, err := this.meta.ID(m)
			if err != nil {
				return &probe.Request{Batch: batch}
			}
			row.ID, row.HasID = crud.ElemValue(id), true
			request.Upsert = true
		}
		request.Rows = append(request.Rows, row)
	}
	return request
}

func (this *enricher[M, ID]) updateRequest(id ID, dataTransferObject any) *probe.Request {
	if this.meta == nil {
		return &probe.Request{}
	}
	changes, err := crud.DefinedChanges(this.meta.Schema, dataTransferObject)
	if err != nil {
		return &probe.Request{}
	}
	row := probe.Row{Values: make(map[string]any, len(changes)), ID: id, HasID: true}
	for _, c := range changes {
		row.Values[c.Field.Column] = c.Value
	}
	return &probe.Request{Rows: []probe.Row{row}, Stored: true}
}
