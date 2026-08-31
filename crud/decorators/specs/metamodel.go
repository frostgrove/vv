package specs

import (
	"cmp"
	"fmt"
	"reflect"

	"github.com/frostgrove/vv/crud"
)

type attr struct{ name string }

func (this attr) Name() string { return this.name }

func (this attr) Path() Path { return Path{Name: this.name} }

func (this *attr) setName(s string) { this.name = s }

type settable interface {
	setName(string)
	elemType() reflect.Type
}

type Attr[M any, T any] struct{ attr }

func (this *Attr[M, T]) elemType() reflect.Type {
	var z T
	return reflect.TypeOf(&z).Elem()
}

func Attribute[M any, T any](field string) Attr[M, T] {
	a := Attr[M, T]{}
	bindAttr[M](&a, field)
	return a
}

func (this Attr[M, T]) Eq(v T) Specification[M]  { return Lift[M](crud.Eq(this.name, v)) }
func (this Attr[M, T]) Ne(v T) Specification[M]  { return Lift[M](crud.Ne(this.name, v)) }
func (this Attr[M, T]) IsNull() Specification[M] { return Lift[M](crud.IsNull(this.name)) }

func (this Attr[M, T]) EqPtr(v *T) Specification[M] {
	if v == nil {
		return nil
	}
	return this.Eq(*v)
}

func (this Attr[M, T]) EqOpt(v crud.Opt[T]) Specification[M] {
	if !v.IsDefined() {
		return nil
	}
	if v.IsNull() {
		return this.IsNull()
	}
	value, _ := v.Get()
	return this.Eq(value)
}
func (this Attr[M, T]) NotNull() Specification[M] {
	return Lift[M](crud.IsNotNull(this.name))
}

func (this Attr[M, T]) In(vs ...T) Specification[M] {
	return Lift[M](crud.InAny(this.name, vs))
}

func (this Attr[M, T]) NotIn(vs ...T) Specification[M] {
	return Lift[M](crud.NotInAny(this.name, vs))
}

func (this Attr[M, T]) Asc() crud.Order  { return crud.Asc(this.name) }
func (this Attr[M, T]) Desc() crud.Order { return crud.Desc(this.name) }

type Ord[M any, T cmp.Ordered] struct{ Attr[M, T] }

func Ordered[M any, T cmp.Ordered](field string) Ord[M, T] {
	o := Ord[M, T]{}
	bindAttr[M](&o.Attr, field)
	return o
}

func (this Ord[M, T]) Gt(v T) Specification[M]  { return Lift[M](crud.Gt(this.name, v)) }
func (this Ord[M, T]) Gte(v T) Specification[M] { return Lift[M](crud.Gte(this.name, v)) }
func (this Ord[M, T]) Lt(v T) Specification[M]  { return Lift[M](crud.Lt(this.name, v)) }
func (this Ord[M, T]) Lte(v T) Specification[M] { return Lift[M](crud.Lte(this.name, v)) }
func (this Ord[M, T]) Between(low, high T) Specification[M] {
	return Lift[M](crud.Between(this.name, low, high))
}

type Str[M any] struct{ Ord[M, string] }

func Text[M any](field string) Str[M] {
	s := Str[M]{}
	bindAttr[M](&s.Attr, field)
	return s
}

func (this Str[M]) Like(pattern string) Specification[M] {
	return Lift[M](crud.Like(this.name, pattern))
}
func (this Str[M]) NotLike(pattern string) Specification[M] {
	return Lift[M](crud.NotLike(this.name, pattern))
}
func (this Str[M]) LikeIgnoreCase(pattern string) Specification[M] {
	return Lift[M](crud.LikeIgnoreCase(this.name, pattern))
}
func (this Str[M]) Contains(s string) Specification[M] { return Lift[M](crud.Contains(this.name, s)) }
func (this Str[M]) ContainsIgnoreCase(s string) Specification[M] {
	return Lift[M](crud.ContainsIgnoreCase(this.name, s))
}
func (this Str[M]) StartsWith(s string) Specification[M] {
	return Lift[M](crud.StartsWith(this.name, s))
}
func (this Str[M]) StartsWithIgnoreCase(s string) Specification[M] {
	return Lift[M](crud.StartsWithIgnoreCase(this.name, s))
}
func (this Str[M]) EndsWith(s string) Specification[M] { return Lift[M](crud.EndsWith(this.name, s)) }
func (this Str[M]) EndsWithIgnoreCase(s string) Specification[M] {
	return Lift[M](crud.EndsWithIgnoreCase(this.name, s))
}

type Cmp[M any, T any] struct{ Attr[M, T] }

func Comparable[M any, T any](field string) Cmp[M, T] {
	c := Cmp[M, T]{}
	bindAttr[M](&c.Attr, field)
	return c
}

func (this Cmp[M, T]) Gt(v T) Specification[M]  { return Lift[M](crud.Gt(this.name, v)) }
func (this Cmp[M, T]) Gte(v T) Specification[M] { return Lift[M](crud.Gte(this.name, v)) }
func (this Cmp[M, T]) Lt(v T) Specification[M]  { return Lift[M](crud.Lt(this.name, v)) }
func (this Cmp[M, T]) Lte(v T) Specification[M] { return Lift[M](crud.Lte(this.name, v)) }
func (this Cmp[M, T]) Between(low, high T) Specification[M] {
	return Lift[M](crud.Between(this.name, low, high))
}

type Rel[M any, T any] struct{ path string }

func (this Rel[M, T]) Path() string { return this.path }

func (this Rel[M, T]) RelPath() string { return this.path }

func (this Rel[M, T]) String() string { return this.path }

func (this *Rel[M, T]) setPath(s string) { this.path = s }

func (this *Rel[M, T]) targetType() reflect.Type {
	var z T
	return reflect.TypeOf(&z).Elem()
}

func (this *Rel[M, T]) relType() reflect.Type { return reflect.TypeOf(*this) }

type relSettable interface {
	setPath(string)
	targetType() reflect.Type
	relType() reflect.Type
}

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

func bindMetamodel(meta *crud.Meta, v reflect.Value, prefix string, rel *crud.Relation) {
	t := v.Type()
	for i := range t.NumField() {
		sf := t.Field(i)
		if !sf.IsExported() {
			continue
		}

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
