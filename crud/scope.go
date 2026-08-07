package crud

import (
	"reflect"
	"strings"
)

// RelationScopes carries a repository's permanent narrowing across relation
// boundaries.
//
// A WHERE clause only ever constrains the statement's own FROM. The moment a
// query reaches a second table — a batched preload, the correlated EXISTS that
// a nested filter opens, the scalar subquery a nested sort opens — that second
// table is read with no narrowing at all, and a scope that exists to hide rows
// stops hiding them. RelationScopes is what a repository hands to the SQL
// writer and to the preloader so the narrowing travels with the hop.
//
// Two ways to declare one, because there are two different questions:
//
//   - by path, for "whenever *this* repository reaches Comments, narrow them" —
//     the answer belongs to the repository, since another repository over the
//     same table may be allowed to see more.
//   - by model, for "wherever these rows are reached at all". A repository
//     registers its own model here, which is what makes a self-relation
//     (Node.Children []Node) obey the scope at any depth.
//
// The zero value and a nil *RelationScopes both mean "nothing is narrowed";
// every method is nil-safe.
type RelationScopes struct {
	paths  map[string]Predicate
	models map[reflect.Type]Predicate
}

// AtPath narrows a relation path, spelled canonically ("Comments",
// "Comments.Author"). It is relative to the model the scopes belong to.
func (rs *RelationScopes) AtPath(path string, p Predicate) *RelationScopes {
	if p == nil {
		return rs
	}
	if rs == nil {
		rs = &RelationScopes{}
	}
	if rs.paths == nil {
		rs.paths = map[string]Predicate{}
	}
	rs.paths[path] = p
	return rs
}

// ForModel narrows a model wherever a hop lands on it.
func (rs *RelationScopes) ForModel(t reflect.Type, p Predicate) *RelationScopes {
	if p == nil || t == nil {
		return rs
	}
	if rs == nil {
		rs = &RelationScopes{}
	}
	if rs.models == nil {
		rs.models = map[reflect.Type]Predicate{}
	}
	rs.models[t] = p
	return rs
}

// At returns the narrowing that applies to a hop that arrived at target by the
// canonical path. A path declaration wins over a model one, so a repository can
// say something more specific about one route than about the model in general.
func (rs *RelationScopes) At(path string, target *Meta) Predicate {
	if rs == nil || target == nil {
		return nil
	}
	if p, ok := rs.paths[path]; ok {
		return p
	}
	return rs.models[target.Type]
}

// Empty reports whether anything is declared at all, so callers can skip the
// bookkeeping entirely.
func (rs *RelationScopes) Empty() bool {
	return rs == nil || (len(rs.paths) == 0 && len(rs.models) == 0)
}

// MergeRelationScopes combines a repository's permanent narrowings with the ones
// a single query carries — a per-request scope from an access-control decorator,
// which cannot be baked into the blueprint because it depends on who is asking.
//
// Where both declare the same path or the same model the two are ANDed. A
// narrowing composes with a narrowing and never replaces one, for the same
// reason Where ANDs: whoever declared the first one is entitled to assume it
// still holds.
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

// under re-roots the path declarations at prefix, for a statement whose own
// FROM is already that far down the path. Model declarations are unaffected —
// they were never relative to anything.
func (rs *RelationScopes) under(prefix string) *RelationScopes {
	if rs == nil {
		return nil
	}
	if len(rs.paths) == 0 {
		return rs
	}
	out := &RelationScopes{models: rs.models}
	for path, p := range rs.paths {
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

// joinPath appends a segment to a canonical relation path.
func joinPath(prefix, seg string) string {
	if prefix == "" {
		return seg
	}
	return prefix + "." + seg
}
