package specs

import (
	"cmp"
	"fmt"
	"reflect"

	"github.com/frostgrove/vv/crud"
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

// EqPtr contributes an equality only when v is non-nil. It is the direct
// spelling for a pointer field from a form or partial request.
func (a Attr[M, T]) EqPtr(v *T) Specification[M] {
	if v == nil {
		return nil
	}
	return a.Eq(*v)
}

// EqOpt preserves all three states of crud.Opt: undefined contributes no
// restriction, null asks for SQL NULL and a value asks for equality.
func (a Attr[M, T]) EqOpt(v crud.Opt[T]) Specification[M] {
	if !v.IsDefined() {
		return nil
	}
	if v.IsNull() {
		return a.IsNull()
	}
	value, _ := v.Get()
	return a.Eq(value)
}
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
func (a Str[M]) Contains(s string) Specification[M] { return Lift[M](crud.Contains(a.name, s)) }
func (a Str[M]) ContainsIgnoreCase(s string) Specification[M] {
	return Lift[M](crud.ContainsIgnoreCase(a.name, s))
}
func (a Str[M]) StartsWith(s string) Specification[M] { return Lift[M](crud.StartsWith(a.name, s)) }
func (a Str[M]) StartsWithIgnoreCase(s string) Specification[M] {
	return Lift[M](crud.StartsWithIgnoreCase(a.name, s))
}
func (a Str[M]) EndsWith(s string) Specification[M] { return Lift[M](crud.EndsWith(a.name, s)) }
func (a Str[M]) EndsWithIgnoreCase(s string) Specification[M] {
	return Lift[M](crud.EndsWithIgnoreCase(a.name, s))
}

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

// ---------------------------------------------------------------------------
// relations

// Rel is the handle of one relation: its canonical path, as an identifier the
// compiler resolves rather than a string literal. The generator embeds one in
// every attribute group it writes for a relation, so
//
//	Article_.Comments.Path()          // "Comments"
//	Article_.Comments.Author.Path()   // "Comments.Author"
//
// which is what sqlrepo.RelationScope, crud.Preload and a relation policy take
// instead of a literal. Renaming the relation and regenerating then breaks the
// build at every call site.
//
// The second type parameter is the model the path lands on. It is checked
// against the relation at package initialisation, so a handle pointing at the
// wrong model fails at start-up rather than narrowing the wrong table.
//
// Path, RelPath and String all answer the same string, and the reason there are
// three is the embedding. The handle is embedded, so Path is promoted at depth
// one, while every column of the *target* model is a field of the same group at
// depth zero — and Go resolves the shallower one. A target with a column called
// Path therefore shadows the method, and Folder_.Files.Path() stops compiling
// for that one relation. String is the second chance; RelPath is the spelling
// nothing shadows.
type Rel[M any, T any] struct{ path string }

// Path returns the canonical relation path.
func (r Rel[M, T]) Path() string { return r.path }

// RelPath returns the same path under a name no column shadows.
func (r Rel[M, T]) RelPath() string { return r.path }

func (r Rel[M, T]) String() string { return r.path }

func (r *Rel[M, T]) setPath(s string) { r.path = s }

func (r *Rel[M, T]) targetType() reflect.Type {
	var z T
	return reflect.TypeOf(&z).Elem()
}

// relType answers the handle's own struct type. It exists because the handle is
// embedded: a group that embeds one promotes these methods, so the group
// satisfies relSettable too, and without this the root metamodel reads its
// first relation group as a misplaced handle.
func (r *Rel[M, T]) relType() reflect.Type { return reflect.TypeOf(*r) }

type relSettable interface {
	setPath(string)
	targetType() reflect.Type
	relType() reflect.Type
}

// ---------------------------------------------------------------------------
// binding

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
//
// An embedded Rel inside such a group is the group's own path, and it is
// checked against the relation it stands for.
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
	bindMetamodel(meta, v, "", nil)
	return out
}

// bindMetamodel walks one attribute group. rel is the relation this group was
// reached through, nil at the root — it is what a relation handle inside the
// group is checked against.
func bindMetamodel(meta *crud.Meta, v reflect.Value, prefix string, rel *crud.Relation) {
	t := v.Type()
	for i := range t.NumField() {
		sf := t.Field(i)
		if !sf.IsExported() {
			continue
		}

		// Before the tag lookup, because the handle is embedded and its field
		// name is the type name — reading "Rel" as an attribute name would
		// send us looking for a column called Comments.Rel. The type check is
		// what separates the handle from a group that merely embeds one.
		if h, ok := v.Field(i).Addr().Interface().(relSettable); ok && sf.Type == h.relType() {
			bindRel(t, sf, h, prefix, rel)
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
			if r, canonical, err := meta.RelationAt(path); err != nil {
				panic(fmt.Sprintf("specs: metamodel %s.%s: %s", t.Name(), sf.Name, err))
			} else {
				bindMetamodel(meta, v.Field(i), canonical, r)
			}
			continue
		}
		panic(fmt.Sprintf("specs: metamodel %s.%s is %s, expected specs.Attr/Ord/Str/Cmp or a nested attribute struct",
			t.Name(), sf.Name, sf.Type))
	}
}

// bindRel fills a relation handle with the path of the group it sits in, and
// refuses one whose declared target is not where that relation lands.
func bindRel(t reflect.Type, sf reflect.StructField, h relSettable, prefix string, rel *crud.Relation) {
	if rel == nil {
		panic(fmt.Sprintf("specs: metamodel %s.%s: a relation handle stands for the group it sits in, and the root model is not a relation",
			t.Name(), sf.Name))
	}
	if want, got := rel.Elem, h.targetType(); want != got {
		panic(fmt.Sprintf("specs: metamodel %s.%s: %s reaches %s, handle declares %s",
			t.Name(), sf.Name, prefix, want, got))
	}
	h.setPath(prefix)
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
