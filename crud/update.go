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
	planPlain planKind = iota // T   — always applied
	planPtr                   // *T  — applied when non-nil
	planOpt                   // Opt[T] — applied when defined; null writes NULL
)

type planField struct {
	Name   string
	Index  []int
	Kind   planKind
	Target *Field
}

// Change is one column the update actually has to write.
type Change struct {
	Field *Field
	Value any // nil means SQL NULL
}

// UpdatePlan is the precomputed mapping from an update DTO to model columns.
// It is built once — at Define time — so a broken DTO fails at start-up rather
// than on the first request, and the hot path does no tag parsing.
type UpdatePlan struct {
	DTO    reflect.Type
	Schema *Schema
	Fields []planField
}

// IncludesField reports whether this DTO plan can write field. Blueprint-level
// lifecycle declarations use it to freeze an untagged legacy SoftDelete column
// without mutating the process-wide Schema shared by a raw repository view.
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

var planCache sync.Map // planKey -> planResult

type planResult struct {
	p   *UpdatePlan
	err error
}

// PlanFor builds (and caches) the update plan mapping U onto s.
//
// Every exported field of U must name a field of the model. Tag a DTO field
// `db:"-"` to keep it out of the mapping, or `db:"OtherName"` to point it at a
// differently named model field.
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

// modelElem strips Opt and pointer wrappers off a model field type.
func modelElem(t reflect.Type) reflect.Type { return ElemType(t) }

// read pulls a DTO field out of v, reporting whether it was provided.
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

// Changes diffs the DTO against the loaded model and returns only the columns
// that must be written. model must be a *M of the plan's schema.
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

// Writes lists every column the DTO defines. It is Changes without a row to
// diff against, which is what a filtered update has: many rows, no single
// "before" state to compare with. The two rules that matter survive — an
// undefined field is still never written, and a null Opt still writes NULL —
// only the "this value is already there" shortcut is gone, so a filtered update
// writes the columns it was given to every row the predicate matches.
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

// Defined lists the DTO fields that were provided, regardless of whether their
// value differs from the stored row. A security decorator uses it to reject
// attempts to touch immutable columns.
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

// Covers answers the model columns this plan can write.
//
// It exists so "does the DTO cover every writable column" is a question the
// runtime answers rather than a second resolution rule somebody keeps in step
// by hand: the plan already resolved every DTO field to a model field, tags,
// embedding and all.
func (this *UpdatePlan) Covers() []*Field {
	out := make([]*Field, 0, len(this.Fields))
	for _, pf := range this.Fields {
		out = append(out, pf.Target)
	}
	return out
}

// Apply writes the changes into a model in place. The repository uses it on
// dialects without RETURNING when no refresh round trip is needed.
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
			// Rebuild as an explicit null rather than undefined.
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

// EqualValues compares two field values the way the update planner does: nil is
// SQL NULL, time.Time compares by instant, everything else by == or DeepEqual.
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

// DefinedFields reports which model fields an update DTO provides. It is the
// reflection-free entry point for decorators that only hold a Meta.
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

// DefinedChanges is DefinedFields with the values as well as the names. It is
// the entry point for a decorator that holds only a Meta and has to know what
// an update would have written — the probe binds those values, and a name on
// its own binds nothing.
//
// It is Writes and not Changes: a decorator has no loaded row to diff against,
// and the probe does not need one. The unchanged half of a composite key is
// read from the stored row in SQL rather than carried here ([[D-010]]).
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
