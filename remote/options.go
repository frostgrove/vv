package remote

import (
	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/query"
)

// An OptionError reports a repository option that has no spelling in the wire
// DSL and therefore cannot be asked of a service in another process.
//
// It is a mistake at the call site rather than a failure of the request, so it
// is a plain error and not a fault: nothing was sent, no status came back, and
// there is nothing for a client to retry or a form to display.
type OptionError struct {
	Option string
	Reason string
}

func (e *OptionError) Error() string {
	return "remote: " + e.Option + " cannot cross a transport: " + e.Reason
}

// ToRequest turns repository options into the query document a service reads,
// so a filter written in Go asks the same question of a remote resource as of a
// local one.
//
// Three options are refused rather than dropped, and the reason is the same one
// crud.MarshalPredicate gives: a narrowing that goes missing between a caller
// and a server produces more rows than were asked for, over a 200, with nothing
// in the response to say so.
//
//   - crud.NarrowRelations, because a relation scope is what follows a preload
//     and a nested filter into the tables they open. It is how an access-control
//     decorator hides rows that Where alone cannot reach, and a dropped one is
//     a leak rather than a slow query. Stacking a security gate over a remote
//     resource has to fail here.
//   - crud.Aggregate and crud.GroupBy, because no binding exposes an aggregate
//     route. There is nothing on the other end to ask.
//   - crud.ForUpdate, because a row lock belongs to a transaction, and the call
//     that would have opened it is not in this process.
//
// Two are accepted and cannot be honoured, and both are named here rather than
// left to be discovered:
//
//   - crud.PrimaryOnly says a read must not be served by a replica. The DSL has
//     no word for it and the document decoder refuses an invented key, so it
//     travels as nothing. It is accepted because the security gate sets it on
//     nearly every call, and refusing it would break the very composition the
//     relation-scope refusal exists to protect. Where a replica lags, the remote
//     service is the layer that has to be configured.
//   - crud.Unsorted drops a default sort. An empty sort in the document means
//     "the service decides", not "no order", so the answer comes back sorted.
//     The rows are the same rows.
//
// The line is that an option which changes *which* rows come back is refused,
// and one which changes only their order or their freshness is documented.
func ToRequest(opts ...crud.Option) (*query.Request, error) {
	return requestOf(crud.Build(opts...))
}

func requestOf(o *crud.Options) (*query.Request, error) {
	if !o.RelScopes.Empty() {
		return nil, &OptionError{"crud.NarrowRelations",
			"a relation scope narrows the tables a preload and a nested filter open, and no filter document reaches them"}
	}
	if len(o.Agg.Aggregations) > 0 || len(o.Agg.GroupBy) > 0 {
		return nil, &OptionError{"crud.Aggregate",
			"no binding exposes an aggregate route, so there is nothing on the other end to ask"}
	}
	if o.ForUpdate {
		return nil, &OptionError{"crud.ForUpdate",
			"a row lock belongs to a transaction, and the transaction is not in this process"}
	}

	req := &query.Request{
		Page:      o.Page,
		Limit:     o.Limit,
		Offset:    o.Offset,
		After:     o.After,
		Before:    o.Before,
		Unpaged:   o.Unpaged,
		SkipTotal: o.NoTotal,
		Distinct:  o.Distinct,
	}
	if len(o.Fields) > 0 {
		req.Select = query.Strings(o.Fields)
	}
	req.Sort = sortsOf(o.Sort)

	if p := o.Predicate(); p != nil {
		doc, err := crud.MarshalPredicate(p)
		if err != nil {
			return nil, err
		}
		// The DSL spells a tautology as {}. On the wire an absent filter has
		// that meaning, while an explicit {} is deliberately rejected.
		if string(doc) != "{}" {
			req.Filter = query.RawFilter(string(doc))
		}
	}

	for _, pre := range o.Preloads {
		p := query.Preload{Path: pre.Path, MaxRows: pre.MaxRows}
		if len(pre.Opts) > 0 {
			// A narrowed preload is its own little query, and everything above
			// applies to it too — including the refusals, which is why this
			// goes back through requestOf rather than reading the fields it
			// happens to need.
			nestedOptions := crud.Build(pre.Opts...)
			if unsupportedPreloadOptions(nestedOptions) {
				return nil, &OptionError{
					Option: "crud.PreloadWhere",
					Reason: "a remote preload carries only Where, OrderBy, and PreloadRows; the other nested options have no wire spelling",
				}
			}
			nested, err := requestOf(nestedOptions)
			if err != nil {
				return nil, err
			}
			p.Filter, p.Sort = nested.Filter, nested.Sort
			// PreloadRows is also a valid way to cap a narrowed preload. Its
			// scope is this relation, so carry the stricter of the two spellings
			// in the one wire field rather than silently losing it.
			if nestedOptions.PreloadRows > 0 && (p.MaxRows == 0 || nestedOptions.PreloadRows < p.MaxRows) {
				p.MaxRows = nestedOptions.PreloadRows
			}
		}
		req.Preload = append(req.Preload, p)
	}
	return req, nil
}

// unsupportedPreloadOptions keeps the wire contract fail-closed. A preload is
// its own batched relation query, but query.Preload deliberately exposes only
// the three controls whose meaning survives that query: narrowing, ordering,
// and its row-cap refusal. In particular, local pagination is itself refused
// for a preload; accepting it remotely and dropping it would turn that visible
// error into a successful, differently shaped relation.
func unsupportedPreloadOptions(o *crud.Options) bool {
	return len(o.Fields) > 0 || len(o.Preloads) > 0 || o.Page != 0 || o.Limit != 0 || o.Offset != 0 ||
		o.RelScopes != nil || len(o.Agg.Aggregations) > 0 || len(o.Agg.GroupBy) > 0 || o.After != "" || o.Before != "" ||
		o.Primary || o.Unpaged || o.NoSort || o.NoTotal || o.ForUpdate || o.Distinct
}

// sortsOf carries the ordering across, nulls placement included. crud spells it
// as two booleans so that "unset" is a state; the document spells it as a word,
// and an empty one means the database's own habit.
func sortsOf(orders []crud.Order) query.Sorts {
	if len(orders) == 0 {
		return nil
	}
	out := make(query.Sorts, 0, len(orders))
	for _, o := range orders {
		s := query.Sort{Field: o.Field, Desc: o.Desc}
		if o.NullsSet {
			s.Nulls = "first"
			if o.NullsLast {
				s.Nulls = "last"
			}
		}
		out = append(out, s)
	}
	return out
}
