package specs

import "github.com/frostgrove/vv/crud"

type Specification[M any] interface {
	ToPredicate(root Root[M], cb Builder) crud.Predicate
}

type SpecFunc[M any] func(root Root[M], cb Builder) crud.Predicate

func (this SpecFunc[M]) ToPredicate(root Root[M], cb Builder) crud.Predicate {
	if this == nil {
		return nil
	}
	return this(root, cb)
}

func Of[M any](f func(root Root[M], cb Builder) crud.Predicate) Specification[M] {
	return SpecFunc[M](f)
}

func Lift[M any](p crud.Predicate) Specification[M] {
	return SpecFunc[M](func(Root[M], Builder) crud.Predicate { return p })
}

func If[M any](ok bool, s Specification[M]) Specification[M] {
	if !ok {
		return nil
	}
	return s
}

type Root[M any] struct{}

func (Root[M]) Get(attribute string) Path { return Path{Name: attribute} }

type Path struct{ Name string }

type Builder struct{}

var CB Builder

func (Builder) Equal(p Path, v any) crud.Predicate    { return crud.Eq(p.Name, v) }
func (Builder) NotEqual(p Path, v any) crud.Predicate { return crud.Ne(p.Name, v) }

func (Builder) GreaterThan(p Path, v any) crud.Predicate          { return crud.Gt(p.Name, v) }
func (Builder) GreaterThanOrEqualTo(p Path, v any) crud.Predicate { return crud.Gte(p.Name, v) }
func (Builder) LessThan(p Path, v any) crud.Predicate             { return crud.Lt(p.Name, v) }
func (Builder) LessThanOrEqualTo(p Path, v any) crud.Predicate    { return crud.Lte(p.Name, v) }

func (Builder) Between(p Path, low, high any) crud.Predicate { return crud.Between(p.Name, low, high) }

func (Builder) Like(p Path, pattern string) crud.Predicate    { return crud.Like(p.Name, pattern) }
func (Builder) NotLike(p Path, pattern string) crud.Predicate { return crud.NotLike(p.Name, pattern) }
func (Builder) LikeIgnoreCase(p Path, pattern string) crud.Predicate {
	return crud.LikeIgnoreCase(p.Name, pattern)
}
func (Builder) Contains(p Path, value string) crud.Predicate {
	return crud.Contains(p.Name, value)
}
func (Builder) StartsWith(p Path, value string) crud.Predicate {
	return crud.StartsWith(p.Name, value)
}
func (Builder) EndsWith(p Path, value string) crud.Predicate {
	return crud.EndsWith(p.Name, value)
}
func (Builder) ContainsIgnoreCase(p Path, value string) crud.Predicate {
	return crud.ContainsIgnoreCase(p.Name, value)
}
func (Builder) StartsWithIgnoreCase(p Path, value string) crud.Predicate {
	return crud.StartsWithIgnoreCase(p.Name, value)
}
func (Builder) EndsWithIgnoreCase(p Path, value string) crud.Predicate {
	return crud.EndsWithIgnoreCase(p.Name, value)
}

func (Builder) IsNull(p Path) crud.Predicate    { return crud.IsNull(p.Name) }
func (Builder) IsNotNull(p Path) crud.Predicate { return crud.IsNotNull(p.Name) }

func (Builder) In(p Path, values ...any) crud.Predicate    { return crud.In(p.Name, values...) }
func (Builder) NotIn(p Path, values ...any) crud.Predicate { return crud.NotIn(p.Name, values...) }

func (Builder) EqualTo(left, right Path) crud.Predicate { return crud.EqField(left.Name, right.Name) }

func (Builder) And(ps ...crud.Predicate) crud.Predicate { return crud.And(ps...) }
func (Builder) Or(ps ...crud.Predicate) crud.Predicate  { return crud.Or(ps...) }
func (Builder) Not(p crud.Predicate) crud.Predicate     { return crud.Not(p) }

func (Builder) Conjunction() crud.Predicate { return crud.True() }
func (Builder) Disjunction() crud.Predicate { return crud.False() }

func (Builder) Raw(sql string, args ...any) crud.Predicate { return crud.Raw(sql, args...) }

type Composite[M any] struct{ inner Specification[M] }

func Where[M any](s Specification[M]) Composite[M] { return Composite[M]{inner: s} }

func (this Composite[M]) ToPredicate(root Root[M], cb Builder) crud.Predicate {
	if this.inner == nil {
		return nil
	}
	return this.inner.ToPredicate(root, cb)
}

func (this Composite[M]) And(o Specification[M]) Composite[M] {
	return combine(this.inner, o, "AND")
}

func (this Composite[M]) Or(o Specification[M]) Composite[M] {
	return combine(this.inner, o, "OR")
}

func (this Composite[M]) Not() Composite[M] {
	inner := this.inner
	return Composite[M]{inner: SpecFunc[M](func(r Root[M], cb Builder) crud.Predicate {
		p := eval(inner, r, cb)
		if p == nil {
			return nil
		}
		return cb.Not(p)
	})}
}

func combine[M any](a, b Specification[M], op string) Composite[M] {
	return Composite[M]{inner: SpecFunc[M](func(r Root[M], cb Builder) crud.Predicate {
		pa, pb := eval(a, r, cb), eval(b, r, cb)
		switch {
		case pa == nil:
			return pb
		case pb == nil:
			return pa
		case op == "OR":
			return cb.Or(pa, pb)
		default:
			return cb.And(pa, pb)
		}
	})}
}

func eval[M any](s Specification[M], r Root[M], cb Builder) crud.Predicate {
	if s == nil {
		return nil
	}
	return s.ToPredicate(r, cb)
}

func Not[M any](s Specification[M]) Composite[M] { return Where(s).Not() }

func AllOf[M any](ss ...Specification[M]) Composite[M] { return fold(ss, "AND") }

func AnyOf[M any](ss ...Specification[M]) Composite[M] { return fold(ss, "OR") }

func fold[M any](ss []Specification[M], op string) Composite[M] {
	return Composite[M]{inner: SpecFunc[M](func(r Root[M], cb Builder) crud.Predicate {
		ps := make([]crud.Predicate, 0, len(ss))
		for _, s := range ss {
			if p := eval(s, r, cb); p != nil {
				ps = append(ps, p)
			}
		}
		if len(ps) == 0 {
			return nil
		}
		if op == "OR" {
			return cb.Or(ps...)
		}
		return cb.And(ps...)
	})}
}

func Predicate[M any](s Specification[M]) crud.Predicate {
	return eval(s, Root[M]{}, CB)
}

func As[M any](s Specification[M]) crud.Option { return crud.Where(Predicate(s)) }
