package crud

import (
	"reflect"
	"sync"
	"time"
	"unsafe"

	"github.com/frostgrove/vv/utils"
)

type planKind uint8

const (
	planPlain planKind = iota
	planPtr
	planOpt
)

type planField struct {
	Name   string
	Index  []int
	Kind   planKind
	Target *Field
}

type Change struct {
	Field *Field
	Value any
}

type UpdatePlan struct {
	DTO    reflect.Type
	Schema *Schema
	Fields []planField
}

func (this *UpdatePlan) IncludesField(field *Field) bool {
	if this == nil || field == nil {
		return false
	}
	for _, planned := range this.Fields {
		if planned.Target == field {
			return true
		}
	}
	return false
}

type planKey struct{ dataTransferObject, model reflect.Type }

var planCache sync.Map

type planResult struct {
	p   *UpdatePlan
	err error
}

func PlanFor[U any](s *Schema) (*UpdatePlan, error) {
	var zero U
	t := reflect.TypeOf(&zero).Elem()
	key := planKey{t, s.Type}
	if v, ok := planCache.Load(key); ok {
		r := v.(planResult)
		return r.p, r.err
	}
	p, err := buildPlan(t, s)
	planCache.Store(key, planResult{p, err})
	return p, err
}

func buildPlan(t reflect.Type, s *Schema) (*UpdatePlan, error) {
	if t.Kind() != reflect.Struct {
		return nil, &SchemaError{Model: t.String(), Reason: "update DTO must be a struct"}
	}
	p := &UpdatePlan{DTO: t, Schema: s}
	if err := collectPlanFields(p, t, nil, nil); err != nil {
		return nil, err
	}
	return p, nil
}

func collectPlanFields(p *UpdatePlan, t reflect.Type, prefix []int, seen []reflect.Type) error {
	for i := range t.NumField() {
		sf := t.Field(i)
		tag, hasTag := sf.Tag.Lookup(TagKey)
		if tag == "-" {
			continue
		}
		idx := append(append([]int{}, prefix...), i)
		if sf.Anonymous && !hasTag && sf.Type.Kind() == reflect.Struct && !isOptType(sf.Type) && !isScalarStruct(sf.Type) {
			for _, st := range seen {
				if st == sf.Type {
					return &SchemaError{Model: p.DTO.String(), Field: sf.Name, Reason: "recursive embedding"}
				}
			}
			if err := collectPlanFields(p, sf.Type, idx, append(seen, sf.Type)); err != nil {
				return err
			}
			continue
		}
		if !sf.IsExported() {
			continue
		}
		ref, _ := parseTag(tag)
		if ref == "" {
			ref = sf.Name
		}
		target := p.Schema.Field(ref)
		if target == nil {
			return &SchemaError{
				Model:  p.DTO.String(),
				Field:  sf.Name,
				Reason: "no field " + ref + " on model " + p.Schema.Name + ` (tag it db:"-" to ignore)`,
			}
		}
		switch {
		case target.PK:
			return &SchemaError{Model: p.DTO.String(), Field: sf.Name, Reason: "the primary key cannot be updated"}
		case target.Generated:
			return &SchemaError{Model: p.DTO.String(), Field: sf.Name, Reason: "model field " + target.Name + " is `generated` and never written"}
		case target.Tombstone:
			return &SchemaError{Model: p.DTO.String(), Field: sf.Name, Reason: "model field " + target.Name + " is the `tombstone` column and is changed only by delete/restore"}
		case target.ServerOwned:
			return &SchemaError{Model: p.DTO.String(), Field: sf.Name, Reason: "model field " + target.Name + " is `serverowned`"}
		case target.Immutable:
			return &SchemaError{Model: p.DTO.String(), Field: sf.Name, Reason: "model field " + target.Name + " is `immutable`"}
		case target.Version:
			return &SchemaError{Model: p.DTO.String(), Field: sf.Name, Reason: "model field " + target.Name + " is the `version` column and is advanced by the repository"}
		}

		pf := planField{Name: sf.Name, Index: idx, Target: target}
		var elem reflect.Type
		switch {
		case isOptType(sf.Type):
			pf.Kind = planOpt
			elem = OptElem(sf.Type)
		case sf.Type.Kind() == reflect.Pointer:
			pf.Kind = planPtr
			elem = sf.Type.Elem()
		default:
			pf.Kind = planPlain
			elem = sf.Type
		}
		if want := modelElem(target.Type); want != elem {
			return &SchemaError{
				Model:  p.DTO.String(),
				Field:  sf.Name,
				Reason: "type mismatch: DTO carries " + elem.String() + ", model field " + target.Name + " is " + want.String(),
			}
		}
		p.Fields = append(p.Fields, pf)
	}
	return nil
}

func modelElem(t reflect.Type) reflect.Type { return ElemType(t) }

func (this planField) read(v reflect.Value) (val any, defined bool) {
	fv := v.FieldByIndex(this.Index)
	switch this.Kind {
	case planPtr:
		if fv.IsNil() {
			return nil, false
		}
		return fv.Elem().Interface(), true
	case planOpt:
		value, defined, null, _ := utils.Inspect(fv.Interface())
		if !defined {
			return nil, false
		}
		if null {
			return nil, true
		}
		return value, true
	default:
		return fv.Interface(), true
	}
}

func (this *UpdatePlan) dtoValue(dataTransferObject any) (reflect.Value, error) {
	v := reflect.ValueOf(dataTransferObject)
	if !v.IsValid() || v.Type() != this.DTO {
		got := "nil"
		if v.IsValid() {
			got = v.Type().String()
		}
		return reflect.Value{}, &SchemaError{Model: this.DTO.String(), Reason: "update called with " + got}
	}
	return v, nil
}

func (this *UpdatePlan) Changes(dataTransferObject any, model any) ([]Change, error) {
	v, err := this.dtoValue(dataTransferObject)
	if err != nil {
		return nil, err
	}
	mv := reflect.ValueOf(model)
	if !mv.IsValid() || mv.Kind() != reflect.Pointer || mv.Type().Elem() != this.Schema.Type {
		return nil, &SchemaError{Model: this.Schema.Name, Reason: "Changes needs a pointer to the model"}
	}
	base := mv.UnsafePointer()

	changes := make([]Change, 0, len(this.Fields))
	for _, pf := range this.Fields {
		val, defined := pf.read(v)
		if !defined {
			continue
		}
		if valuesEqual(pf.Target.comparableOf(base), val) {
			continue
		}
		changes = append(changes, Change{Field: pf.Target, Value: val})
	}
	return changes, nil
}

func (this *UpdatePlan) Writes(dataTransferObject any) ([]Change, error) {
	v, err := this.dtoValue(dataTransferObject)
	if err != nil {
		return nil, err
	}
	changes := make([]Change, 0, len(this.Fields))
	for _, pf := range this.Fields {
		val, defined := pf.read(v)
		if !defined {
			continue
		}
		changes = append(changes, Change{Field: pf.Target, Value: val})
	}
	return changes, nil
}

func (this *UpdatePlan) Defined(dataTransferObject any) ([]string, error) {
	v, err := this.dtoValue(dataTransferObject)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, pf := range this.Fields {
		if _, ok := pf.read(v); ok {
			out = append(out, pf.Target.Name)
		}
	}
	return out, nil
}

func (this *UpdatePlan) Covers() []*Field {
	out := make([]*Field, 0, len(this.Fields))
	for _, pf := range this.Fields {
		out = append(out, pf.Target)
	}
	return out
}

func (this *UpdatePlan) Apply(changes []Change, model any) {
	mv := reflect.ValueOf(model)
	base := mv.UnsafePointer()
	for _, ch := range changes {
		destination := reflect.NewAt(ch.Field.Type, unsafe.Add(base, ch.Field.Offset)).Elem()
		setFieldValue(destination, ch.Field, ch.Value)
	}
}

func setFieldValue(destination reflect.Value, f *Field, val any) {
	if val == nil {
		destination.SetZero()
		if f.Optional {
			if s, ok := destination.Addr().Interface().(interface{ Scan(any) error }); ok {
				_ = s.Scan(nil)
			}
		}
		return
	}
	rv := reflect.ValueOf(val)
	switch {
	case f.Optional:
		if s, ok := destination.Addr().Interface().(interface{ Scan(any) error }); ok {
			_ = s.Scan(val)
		}
	case f.Type.Kind() == reflect.Pointer:
		p := reflect.New(f.Type.Elem())
		p.Elem().Set(rv)
		destination.Set(p)
	default:
		destination.Set(rv)
	}
}

func EqualValues(a, b any) bool { return valuesEqual(a, b) }

func valuesEqual(a, b any) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	if at, ok := a.(time.Time); ok {
		if bt, ok := b.(time.Time); ok {
			return at.Equal(bt)
		}
	}
	ta, tb := reflect.TypeOf(a), reflect.TypeOf(b)
	if ta != tb {
		return false
	}
	if ta.Comparable() {
		return a == b
	}
	return reflect.DeepEqual(a, b)
}

func DefinedFields(s *Schema, dataTransferObject any) ([]string, error) {
	t := reflect.TypeOf(dataTransferObject)
	if t == nil {
		return nil, &SchemaError{Model: s.Name, Reason: "nil update DTO"}
	}
	key := planKey{t, s.Type}
	if v, ok := planCache.Load(key); ok {
		r := v.(planResult)
		if r.err != nil {
			return nil, r.err
		}
		return r.p.Defined(dataTransferObject)
	}
	p, err := buildPlan(t, s)
	planCache.Store(key, planResult{p, err})
	if err != nil {
		return nil, err
	}
	return p.Defined(dataTransferObject)
}

func DefinedChanges(s *Schema, dataTransferObject any) ([]Change, error) {
	t := reflect.TypeOf(dataTransferObject)
	if t == nil {
		return nil, &SchemaError{Model: s.Name, Reason: "nil update DTO"}
	}
	key := planKey{t, s.Type}
	if v, ok := planCache.Load(key); ok {
		r := v.(planResult)
		if r.err != nil {
			return nil, r.err
		}
		return r.p.Writes(dataTransferObject)
	}
	p, err := buildPlan(t, s)
	planCache.Store(key, planResult{p, err})
	if err != nil {
		return nil, err
	}
	return p.Writes(dataTransferObject)
}
