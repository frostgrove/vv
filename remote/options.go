package remote

import (
	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/query"
)

type OptionError struct {
	Option string
	Reason string
}

func (this *OptionError) Error() string {
	return "remote: " + this.Option + " cannot cross a transport: " + this.Reason
}

func ToRequest(options ...crud.Option) (*query.Request, error) {
	return requestOf(crud.Build(options...))
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
	if o.PreloadRows < 0 && len(o.Preloads) > 0 {
		return nil, &OptionError{"crud.PreloadRows", "a preload row cap cannot be negative"}
	}

	request := &query.Request{
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
		request.Select = query.Strings(o.Fields)
	}
	request.Sort = sortsOf(o.Sort)

	if p := o.Predicate(); p != nil {
		doc, err := crud.MarshalPredicate(p)
		if err != nil {
			return nil, err
		}

		if !crud.IsTautology(p) && string(doc) != "{}" {
			request.Filter = query.RawFilter(string(doc))
		}
	}

	for _, pre := range o.Preloads {
		if pre.MaxRows < 0 {
			return nil, &OptionError{"crud.PreloadCap", "a preload row cap cannot be negative"}
		}
		maxRows := pre.MaxRows
		if o.PreloadRows > 0 && (maxRows == 0 || o.PreloadRows < maxRows) {
			maxRows = o.PreloadRows
		}
		p := query.Preload{Path: pre.Path, MaxRows: maxRows}
		if len(pre.Opts) > 0 {
			nestedOptions, err := crud.BuildPreloadOptions(pre.Path, pre.Opts...)
			if err != nil {
				return nil, &OptionError{
					Option: "crud.PreloadWhere",
					Reason: err.Error(),
				}
			}
			nested, err := requestOf(nestedOptions)
			if err != nil {
				return nil, err
			}
			p.Filter, p.Sort = nested.Filter, nested.Sort

			if nestedOptions.PreloadRows > 0 && (p.MaxRows == 0 || nestedOptions.PreloadRows < p.MaxRows) {
				p.MaxRows = nestedOptions.PreloadRows
			}
		}
		request.Preload = append(request.Preload, p)
	}
	return request, nil
}

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
