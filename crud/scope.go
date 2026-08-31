package crud

import (
	"maps"
	"reflect"
	"strings"
)

type RelationScopes struct {
	paths  map[string]Predicate
	models map[reflect.Type]Predicate
}

func (this *RelationScopes) AtPath(path string, p Predicate) *RelationScopes {
	if p == nil {
		return this
	}
	out := this.clone()
	if out.paths == nil {
		out.paths = map[string]Predicate{}
	}
	out.paths[path] = both(out.paths[path], p)
	return out
}

func (this *RelationScopes) clone() *RelationScopes {
	out := &RelationScopes{}
	if this == nil {
		return out
	}
	if len(this.paths) > 0 {
		out.paths = make(map[string]Predicate, len(this.paths))
		maps.Copy(out.paths, this.paths)
	}
	if len(this.models) > 0 {
		out.models = make(map[reflect.Type]Predicate, len(this.models))
		maps.Copy(out.models, this.models)
	}
	return out
}

func (this *RelationScopes) ForModel(t reflect.Type, p Predicate) *RelationScopes {
	if p == nil || t == nil {
		return this
	}
	out := this.clone()
	if out.models == nil {
		out.models = map[reflect.Type]Predicate{}
	}
	out.models[t] = both(out.models[t], p)
	return out
}

func (this *RelationScopes) At(path string, target *Meta) Predicate {
	if this == nil || target == nil {
		return nil
	}
	return both(this.paths[path], this.models[target.Type])
}

func (this *RelationScopes) Empty() bool {
	return this == nil || (len(this.paths) == 0 && len(this.models) == 0)
}

func (this *RelationScopes) Resolve(root *Meta) (*RelationScopes, error) {
	if this.Empty() {
		return this, nil
	}
	if root == nil {
		return nil, &SchemaError{Reason: "relation scopes need a root model"}
	}
	out := &RelationScopes{}
	for path, p := range this.paths {
		rel, canonical, err := root.RelationAt(path)
		if err != nil {
			return nil, err
		}
		target, err := rel.Target()
		if err != nil {
			return nil, err
		}
		if IsTautologyFor(target, p) {
			return nil, &SchemaError{Model: root.Name, Field: path, Reason: "relation scope must narrow rows"}
		}
		out = out.AtPath(canonical, p)
	}
	for typ, p := range this.models {
		schema, err := SchemaOfType(typ)
		if err != nil {
			return nil, err
		}
		if IsTautologyFor(&Meta{Schema: schema}, p) {
			return nil, &SchemaError{Model: root.Name, Field: typ.String(), Reason: "relation scope must narrow rows"}
		}
		out = out.ForModel(typ, p)
	}
	return out, nil
}

func MergeRelationScopes(a, b *RelationScopes) *RelationScopes {
	if a.Empty() {
		return b
	}
	if b.Empty() {
		return a
	}
	out := &RelationScopes{
		paths:  make(map[string]Predicate, len(a.paths)+len(b.paths)),
		models: make(map[reflect.Type]Predicate, len(a.models)+len(b.models)),
	}
	for k, p := range a.paths {
		out.paths[k] = p
	}
	for k, p := range b.paths {
		out.paths[k] = both(out.paths[k], p)
	}
	for k, p := range a.models {
		out.models[k] = p
	}
	for k, p := range b.models {
		out.models[k] = both(out.models[k], p)
	}
	return out
}

func both(a, b Predicate) Predicate {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	return And(a, b)
}

func (this *RelationScopes) under(prefix string) *RelationScopes {
	if this == nil {
		return nil
	}
	if len(this.paths) == 0 {
		return this
	}
	out := &RelationScopes{models: this.models}
	for path, p := range this.paths {
		rest, ok := strings.CutPrefix(path, prefix+".")
		if !ok {
			continue
		}
		if out.paths == nil {
			out.paths = map[string]Predicate{}
		}
		out.paths[rest] = p
	}
	return out
}

func joinPath(prefix, seg string) string {
	if prefix == "" {
		return seg
	}
	return prefix + "." + seg
}
