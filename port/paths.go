package port

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/frostgrove/vv/crud"
)

func Paths[M any]() *PathBuilder[M] {
	return &PathBuilder[M]{tags: []string{"json"}}
}

type PathBuilder[M any] struct {
	tags      []string
	overrides PathMap
	except    []string
	fieldName bool
	err       error
}

func (this *PathBuilder[M]) From(tags ...string) *PathBuilder[M] {
	if len(tags) == 0 {
		return this.fail("From was given no tag to read")
	}
	for _, tag := range tags {
		if strings.TrimSpace(tag) == "" {
			return this.fail("From was given an empty tag name")
		}
	}
	this.tags = tags
	return this
}

func (this *PathBuilder[M]) Override(entries PathMap) *PathBuilder[M] {
	if len(entries) == 0 {
		return this
	}
	if this.overrides == nil {
		this.overrides = make(PathMap, len(entries))
	}
	for name, to := range entries {
		this.overrides[name] = to
	}
	return this
}

func (this *PathBuilder[M]) Except(names ...string) *PathBuilder[M] {
	this.except = append(this.except, names...)
	return this
}

func (this *PathBuilder[M]) OrFieldName() *PathBuilder[M] {
	this.fieldName = true
	return this
}

func (this *PathBuilder[M]) Build() (PathMap, error) {
	if this.err != nil {
		return nil, this.err
	}
	schema, err := crud.SchemaOf[M]()
	if err != nil {
		return nil, err
	}
	tags := wireTags(schema.Type)
	excepted := setOf(this.except)

	out := make(PathMap, len(schema.Insert))
	var underived, omitted []string
	for _, field := range schema.Insert {
		if field.Version || excepted[field.Name] {
			continue
		}
		if to, overridden := this.overrides[field.Name]; overridden {
			out[field.Name] = to
			continue
		}
		switch key, verdict := this.keyOf(field.Name, tags[field.Name]); {
		case verdict == tagNames:
			out[field.Name] = At(key)
		case verdict == tagOmits:
			omitted = append(omitted, field.Name)
		case this.fieldName:
			out[field.Name] = At(field.Name)
		default:
			underived = append(underived, field.Name)
		}
	}

	var contradicted []string
	for name, to := range this.overrides {
		if excepted[name] {
			contradicted = append(contradicted, name)
			continue
		}
		if _, derived := out[name]; !derived {
			out[name] = to
		}
	}

	if err := this.refuse(schema.Name, underived, omitted, contradicted); err != nil {
		return nil, err
	}
	return NewPathMap[M](out, this.except...)
}

func (this *PathBuilder[M]) MustBuild() PathMap {
	out, err := this.Build()
	if err != nil {
		panic(err)
	}
	return out
}

type tagVerdict int

const (
	tagSilent tagVerdict = iota
	tagOmits
	tagNames
)

func (this *PathBuilder[M]) keyOf(field string, tag reflect.StructTag) (string, tagVerdict) {
	omitted := false
	for _, name := range this.tags {
		value, tagged := tag.Lookup(name)
		if !tagged {
			continue
		}
		if value == "-" {
			omitted = true
			continue
		}
		key, _, _ := strings.Cut(value, ",")
		if key == "" {
			return field, tagNames
		}
		return key, tagNames
	}
	if omitted {
		return "", tagOmits
	}
	return "", tagSilent
}

func (this *PathBuilder[M]) refuse(model string, underived, omitted, contradicted []string) error {
	sort.Strings(underived)
	sort.Strings(omitted)
	sort.Strings(contradicted)

	sources := strings.Join(this.tags, " or ")
	var problems []string
	if len(underived) > 0 {
		problems = append(problems, fmt.Sprintf("no %s tag names %s — take the field name with OrFieldName, or name the key with Override",
			sources, strings.Join(underived, ", ")))
	}
	if len(omitted) > 0 {
		problems = append(problems, fmt.Sprintf("%s says %s is not on the wire, so no key can be right for it — drop it with Except, or name its key with Override",
			sources, strings.Join(omitted, ", ")))
	}
	if len(contradicted) > 0 {
		problems = append(problems, "an override and an exclusion for "+strings.Join(contradicted, ", ")+
			", which cannot both be what was meant")
	}
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("the inverse path map for %s cannot be derived from the model: %s",
		model, strings.Join(problems, "; "))
}

func wireTags(root reflect.Type) map[string]reflect.StructTag {
	tags := map[string]reflect.StructTag{}

	frontier := []tagFrame{{structure: structOf(root)}}
	for len(frontier) > 0 {
		var deeper []tagFrame
		for _, frame := range frontier {
			if frame.structure == nil {
				continue
			}
			for i := range frame.structure.NumField() {
				field := frame.structure.Field(i)

				if field.Tag.Get(crud.TagKey) == "-" {
					continue
				}
				if field.Anonymous {
					deeper = append(deeper, frame.into(field.Type))
				}
				if unmapped(field) {
					continue
				}
				if _, shallower := tags[field.Name]; !shallower {
					tags[field.Name] = field.Tag
				}
			}
		}
		frontier = deeper
	}
	return tags
}

func unmapped(field reflect.StructField) bool {
	if _, related := field.Tag.Lookup("rel"); related {
		return true
	}
	return !field.IsExported() && !field.Anonymous
}

type tagFrame struct {
	structure reflect.Type
	through   []reflect.Type
}

func (this tagFrame) into(embedded reflect.Type) tagFrame {
	next := structOf(embedded)
	for _, seen := range this.through {
		if seen == next {
			return tagFrame{}
		}
	}
	if next == this.structure {
		return tagFrame{}
	}
	return tagFrame{structure: next, through: append(this.through, this.structure)}
}

func structOf(t reflect.Type) reflect.Type {
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == nil || t.Kind() != reflect.Struct {
		return nil
	}
	return t
}

func (this *PathBuilder[M]) fail(problem string) *PathBuilder[M] {
	if this.err == nil {
		this.err = fmt.Errorf("port.Paths[%s]: %s", typeName[M](), problem)
	}
	return this
}
