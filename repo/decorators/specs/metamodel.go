package specs

import (
	"cmp"
	"fmt"
	"reflect"

	"github.com/shardit-io/qq/crud"
)

// The typed metamodel is the Go answer to JPA's generated User_ class: one
// declaration per model, validated at package initialisation, after which every
// query reads like `User_.Email.Eq("a@b.c")` and the compiler checks the value
// type for you.
//
//	type UserAttrs struct {
//	    ID    specs.Ord[User, int64]
//	    Email specs.Str[User]
//	    Age   specs.Ord[User, int]
//	    Bio   specs.Attr[User, string]
//	}
//
//	var User_ = specs.Metamodel[User, UserAttrs]()

type attr struct{ name string }

// Name returns the model field this attribute points at.
func (a attr) Name() string { return a.name }

// Path returns the criteria path, for use with a Builder.
func (a attr) Path() Path { return Path{Name: a.name} }

func (a *attr) setName(s string) { a.name = s }

type settable interface {
	setName(string)
	elemType() reflect.Type
}

// Attr is an attribute of M holding values of type T: equality, membership and
// nullability.
type Attr[M any, T any] struct{ attr }

func (a *Attr[M, T]) elemType() reflect.Type {
	var z T
	return reflect.TypeOf(&z).Elem()
}

// Attribute declares an attribute by Go field name, panicking if M has no such
// field or if its type is not T.
func Attribute[M any, T any](field string) Attr[M, T] {
	a := Attr[M, T]{}
	bindAttr[M](&a, field)
	return a
}

func (a Attr[M, T]) Eq(v T) Specification[M]  { return Lift[M](crud.Eq(a.name, v)) }
func (a Attr[M, T]) Ne(v T) Specification[M]  { return Lift[M](crud.Ne(a.name, v)) }
func (a Attr[M, T]) IsNull() Specification[M] { return Lift[M](crud.IsNull(a.name)) }
func (a Attr[M, T]) NotNull() Specification[M] {
	return Lift[M](crud.IsNotNull(a.name))
}

func (a Attr[M, T]) In(vs ...T) Specification[M] {
	return Lift[M](crud.InAny(a.name, vs))
}

func (a Attr[M, T]) NotIn(vs ...T) Specification[M] {
	return Lift[M](crud.NotInAny(a.name, vs))
}

// Asc and Desc build sort terms for the same attribute.
func (a Attr[M, T]) Asc() crud.Order  { return crud.Asc(a.name) }
func (a Attr[M, T]) Desc() crud.Order { return crud.Desc(a.name) }

// Ord is an attribute whose values are ordered, adding range comparisons.
type Ord[M any, T cmp.Ordered] struct{ Attr[M, T] }

// Ordered declares an ordered attribute.
func Ordered[M any, T cmp.Ordered](field string) Ord[M, T] {
	o := Ord[M, T]{}
	bindAttr[M](&o.Attr, field)
	return o
}

func (a Ord[M, T]) Gt(v T) Specification[M]  { return Lift[M](crud.Gt(a.name, v)) }
func (a Ord[M, T]) Gte(v T) Specification[M] { return Lift[M](crud.Gte(a.name, v)) }
func (a Ord[M, T]) Lt(v T) Specification[M]  { return Lift[M](crud.Lt(a.name, v)) }
func (a Ord[M, T]) Lte(v T) Specification[M] { return Lift[M](crud.Lte(a.name, v)) }
func (a Ord[M, T]) Between(low, high T) Specification[M] {
	return Lift[M](crud.Between(a.name, low, high))
}

// Str is a text attribute, adding pattern matching.
type Str[M any] struct{ Ord[M, string] }

// Text declares a string attribute.
func Text[M any](field string) Str[M] {
	s := Str[M]{}
	bindAttr[M](&s.Attr, field)
	return s
}

func (a Str[M]) Like(pattern string) Specification[M] { return Lift[M](crud.Like(a.name, pattern)) }
func (a Str[M]) NotLike(pattern string) Specification[M] {
	return Lift[M](crud.NotLike(a.name, pattern))
}
func (a Str[M]) LikeIgnoreCase(pattern string) Specification[M] {
	return Lift[M](crud.LikeIgnoreCase(a.name, pattern))
}
func (a Str[M]) Contains(s string) Specification[M]   { return Lift[M](crud.Contains(a.name, s)) }
func (a Str[M]) StartsWith(s string) Specification[M] { return Lift[M](crud.StartsWith(a.name, s)) }
func (a Str[M]) EndsWith(s string) Specification[M]   { return Lift[M](crud.EndsWith(a.name, s)) }

// Cmp is a shorthand for time.Time and other ordered-by-comparison types that
// cmp.Ordered does not cover; it exposes range operators without the
// constraint.
type Cmp[M any, T any] struct{ Attr[M, T] }

// Comparable declares an attribute with range operators for a type that is not
// cmp.Ordered, such as time.Time.
func Comparable[M any, T any](field string) Cmp[M, T] {
	c := Cmp[M, T]{}
	bindAttr[M](&c.Attr, field)
	return c
}

func (a Cmp[M, T]) Gt(v T) Specification[M]  { return Lift[M](crud.Gt(a.name, v)) }
func (a Cmp[M, T]) Gte(v T) Specification[M] { return Lift[M](crud.Gte(a.name, v)) }
func (a Cmp[M, T]) Lt(v T) Specification[M]  { return Lift[M](crud.Lt(a.name, v)) }
func (a Cmp[M, T]) Lte(v T) Specification[M] { return Lift[M](crud.Lte(a.name, v)) }
func (a Cmp[M, T]) Between(low, high T) Specification[M] {
	return Lift[M](crud.Between(a.name, low, high))
}

// Metamodel fills a struct of attribute declarations from the model schema.
// Each field is matched by name (override with `attr:"FieldName"`, skip with
// `attr:"-"`) and its value type is checked against the model. A mismatch
// panics, so a renamed column is caught at start-up rather than at query time.
//
// A field that is itself a struct of attributes describes a relation, and its
// names are prefixed with the path — which is how `Article_.Author.Name.Eq(…)`
// ends up filtering articles by their author's name. The type parameter of the
// nested attributes stays the root model, because the predicate they build
// still selects root rows.
func Metamodel[M any, A any]() A {
	var out A
	meta, err := crud.NewMeta[M]("")
	if err != nil {
		panic(err)
	}
	v := reflect.ValueOf(&out).Elem()
	if v.Kind() != reflect.Struct {
		panic(fmt.Sprintf("specs: metamodel %s must be a struct", v.Type()))
	}
	bindMetamodel(meta, v, "")
	return out
}

func bindMetamodel(meta *crud.Meta, v reflect.Value, prefix string) {
	t := v.Type()
	for i := range t.NumField() {
		sf := t.Field(i)
		if !sf.IsExported() {
			continue
		}
		name := sf.Name
		if tag, ok := sf.Tag.Lookup("attr"); ok {
			if tag == "-" {
				continue
			}
			name = tag
		}
		path := name
		if prefix != "" {
			path = prefix + "." + name
		}

		if s, ok := v.Field(i).Addr().Interface().(settable); ok {
			f, canonical, err := meta.FieldAt(path)
			if err != nil {
				panic(fmt.Sprintf("specs: metamodel %s.%s: %s", t.Name(), sf.Name, err))
			}
			if want, got := crud.ElemType(f.Type), s.elemType(); want != got {
				panic(fmt.Sprintf("specs: metamodel %s.%s: %s is %s, attribute declares %s",
					t.Name(), sf.Name, canonical, want, got))
			}
			s.setName(canonical)
			continue
		}

		if sf.Type.Kind() == reflect.Struct {
			// A nested attribute struct names a relation.
			if _, canonical, err := meta.RelationAt(path); err != nil {
				panic(fmt.Sprintf("specs: metamodel %s.%s: %s", t.Name(), sf.Name, err))
			} else {
				bindMetamodel(meta, v.Field(i), canonical)
			}
			continue
		}
		panic(fmt.Sprintf("specs: metamodel %s.%s is %s, expected specs.Attr/Ord/Str/Cmp or a nested attribute struct",
			t.Name(), sf.Name, sf.Type))
	}
}

// bindAttr validates a standalone attribute declaration against the model.
func bindAttr[M any](s settable, field string) {
	schema := crud.MustSchemaOf[M]()
	f := schema.Field(field)
	if f == nil {
		panic(fmt.Sprintf("specs: model %s has no field %s", schema.Name, field))
	}
	if want, got := crud.ElemType(f.Type), s.elemType(); want != got {
		panic(fmt.Sprintf("specs: %s.%s is %s, attribute declares %s", schema.Name, f.Name, want, got))
	}
	s.setName(f.Name)
}
