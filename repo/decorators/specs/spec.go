// Package specs is the JPA Specifications / Criteria API port.
//
// The shapes map one to one onto their Java counterparts:
//
//	Specification<T>            -> specs.Specification[M]
//	Root<T>, CriteriaBuilder    -> specs.Root[M], specs.Builder
//	Specification.where(a).and(b).or(c).not()
//	                            -> specs.Where(a).And(b).Or(c).Not()
//	JpaSpecificationExecutor<T> -> specs.Executor(repo)
//	User_ metamodel             -> specs.Metamodel[User, UserMeta]()
//
// A specification is a pure function from (root, builder) to a predicate, so it
// composes, it is unit-testable without a database, and it never sees SQL.
package specs

import "github.com/shardit-io/go-rx-crud/crud"

// Specification is a reusable, composable fragment of a WHERE clause — the
// analogue of javax.persistence Specification<T>.
type Specification[M any] interface {
	ToPredicate(root Root[M], cb Builder) crud.Predicate
}

// SpecFunc adapts a plain function into a Specification. It is what a Java
// lambda would be.
type SpecFunc[M any] func(root Root[M], cb Builder) crud.Predicate

func (f SpecFunc[M]) ToPredicate(root Root[M], cb Builder) crud.Predicate {
	if f == nil {
		return nil
	}
	return f(root, cb)
}

// Of adapts a function into a Specification:
//
//	func HasEmail(email string) specs.Specification[User] {
//	    return specs.Of[User](func(r specs.Root[User], cb specs.Builder) crud.Predicate {
//	        return cb.Equal(r.Get("Email"), email)
//	    })
//	}
func Of[M any](f func(root Root[M], cb Builder) crud.Predicate) Specification[M] {
	return SpecFunc[M](f)
}

// Lift turns a bare predicate into a Specification.
func Lift[M any](p crud.Predicate) Specification[M] {
	return SpecFunc[M](func(Root[M], Builder) crud.Predicate { return p })
}

// Root resolves model attributes, like JPA's Root<T>. Attributes are addressed
// by Go field name; a column name also works.
type Root[M any] struct{}

// Get returns the path of an attribute.
func (Root[M]) Get(attribute string) Path { return Path{Name: attribute} }

// Path names one attribute of the root.
type Path struct{ Name string }

// Builder is the CriteriaBuilder: it turns paths and values into predicates.
// The zero value is ready to use; CB is a shared instance.
type Builder struct{}

// CB is the shared CriteriaBuilder instance.
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

func (Builder) IsNull(p Path) crud.Predicate    { return crud.IsNull(p.Name) }
func (Builder) IsNotNull(p Path) crud.Predicate { return crud.IsNotNull(p.Name) }

func (Builder) In(p Path, values ...any) crud.Predicate    { return crud.In(p.Name, values...) }
func (Builder) NotIn(p Path, values ...any) crud.Predicate { return crud.NotIn(p.Name, values...) }

func (Builder) EqualTo(left, right Path) crud.Predicate { return crud.EqField(left.Name, right.Name) }

func (Builder) And(ps ...crud.Predicate) crud.Predicate { return crud.And(ps...) }
func (Builder) Or(ps ...crud.Predicate) crud.Predicate  { return crud.Or(ps...) }
func (Builder) Not(p crud.Predicate) crud.Predicate     { return crud.Not(p) }

// Conjunction is the always-true predicate, Disjunction the always-false one.
func (Builder) Conjunction() crud.Predicate { return crud.True() }
func (Builder) Disjunction() crud.Predicate { return crud.False() }

// Raw is the escape hatch; use ? markers for binds.
func (Builder) Raw(sql string, args ...any) crud.Predicate { return crud.Raw(sql, args...) }

// ---------------------------------------------------------------------------
// composition — Specification.where(...).and(...).or(...).not()

// Composite is a Specification that can be combined with others.
type Composite[M any] struct{ inner Specification[M] }

// Where starts a composition. A nil specification means "no restriction",
// exactly like Specification.where(null) in Spring Data.
func Where[M any](s Specification[M]) Composite[M] { return Composite[M]{inner: s} }

func (c Composite[M]) ToPredicate(root Root[M], cb Builder) crud.Predicate {
	if c.inner == nil {
		return nil
	}
	return c.inner.ToPredicate(root, cb)
}

// And restricts further.
func (c Composite[M]) And(o Specification[M]) Composite[M] {
	return combine(c.inner, o, "AND")
}

// Or widens.
func (c Composite[M]) Or(o Specification[M]) Composite[M] {
	return combine(c.inner, o, "OR")
}

// Not negates the whole composition so far.
func (c Composite[M]) Not() Composite[M] {
	inner := c.inner
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

// Not negates a specification.
func Not[M any](s Specification[M]) Composite[M] { return Where(s).Not() }

// AllOf ANDs every specification; an empty list means "no restriction".
func AllOf[M any](ss ...Specification[M]) Composite[M] { return fold(ss, "AND") }

// AnyOf ORs every specification; an empty list means "no restriction".
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

// ---------------------------------------------------------------------------
// bridging into the repository

// Predicate renders a specification into the core predicate AST.
func Predicate[M any](s Specification[M]) crud.Predicate {
	return eval(s, Root[M]{}, CB)
}

// As turns a specification into a query option, so specifications work with the
// plain repository too:
//
//	users.Get(ctx, specs.As(activeAdults), crud.Page(2))
func As[M any](s Specification[M]) crud.Option { return crud.Where(Predicate(s)) }
