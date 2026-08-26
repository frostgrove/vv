package porthttp

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"

	"github.com/frostgrove/vv/errs"
)

// maxBodyDepth and maxBodyLeaves bound the walk. A body is attacker-supplied
// and the index is built while answering an error, which is the worst moment to
// discover that a 3000-deep array is a stack overflow.
const (
	maxBodyDepth  = 32
	maxBodyLeaves = 4096
)

// BodyResolver is the path hop for handlers nobody generated a mapper for: it
// walks the raw request body into a leaf-path index and matches a model field
// name against the keys the client actually sent.
//
// It is the fallback, not the mechanism. A generated port.PathMap ([[D-043]],
// [[D-050]]) is wired ahead of it with WithResolvers and wins every time,
// because it knows the mapping instead of recognising it — and the renderer
// runs this hop only over a path the declared ones left unchanged, so a guess
// cannot overturn a declaration.
//
// Three limits, stated rather than discovered:
//
//   - JSON only. Fiber's binder dispatches on Content-Type and accepts XML and
//     form encodings, and a form body has no nesting to index. A non-JSON body
//     declines, and the path degrades to the model field name.
//   - A name that folds to more than one leaf declines. Value matching is what
//     would separate them, and no producer fills errs.Detail.Value today — the
//     only source is PostgreSQL's localised Detail, which [[D-039]] refuses to
//     read. Until one does, two `email` keys at two nestings are two candidates
//     and this layer does not pick.
//   - It declines rather than guessing, always. errs.Chain drops what a
//     declining hop returned, so a decline cannot ship a guess even by
//     accident, and the renderer marks the violation approximate.
//
// A body it cannot index still produces a hop, and that hop declines
// everything. The distinction matters: a body that was retained and could not
// be read is a path this layer *knows* it did not translate, and saying so is
// the whole of [[D-043]]. Only "no body was retained at all" answers nil, and
// errs.Chain skips a nil hop — there is nothing to be approximate about.
func BodyResolver(raw []byte) errs.Resolver {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	idx := &bodyIndex{exact: map[string]bool{}, byName: map[string][]errs.Path{}}
	idx.index(raw)
	return idx
}

// bodyResolverFrom builds the hop from whatever the binding retained, or nil
// when it retained nothing.
func bodyResolverFrom(ctx context.Context) errs.Resolver {
	return BodyResolver(BodyFrom(ctx))
}

type bodyIndex struct {
	exact  map[string]bool
	byName map[string][]errs.Path
	leaves int
}

// index reads the body, or leaves the maps empty — which is the declining
// state, and the one a form or XML body lands in.
func (b *bodyIndex) index(raw []byte) {
	raw = bytes.TrimSpace(raw)
	if raw[0] != '{' && raw[0] != '[' {
		return
	}
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return
	}
	b.walk(doc, nil, 0)
}

// Resolve implements errs.Resolver.
func (b *bodyIndex) Resolve(p errs.Path) (errs.Path, bool) {
	// An empty path is a complete answer rather than an unresolved one: a
	// violation that names no field — a composite key at its common ancestor, a
	// fault with nothing to attribute — must not be marked approximate for
	// having nothing to translate.
	if len(p) == 0 {
		return p, true
	}
	// A path the client's own body already contains is left alone. Without
	// this, a violation that arrived already translated — a validation bridge's
	// ["user","email"] — would be re-matched on its last step and could collide
	// with a same-named key elsewhere in the payload.
	if b.exact[p.Pointer()] {
		return p, true
	}
	last := p[len(p)-1]
	if last.IsIndex {
		return p, false
	}
	got := b.byName[fold(last.Name)]
	if len(got) != 1 {
		return p, false
	}
	return got[0], true
}

func (b *bodyIndex) walk(v any, at errs.Path, depth int) {
	if depth > maxBodyDepth || b.leaves >= maxBodyLeaves {
		return
	}
	switch t := v.(type) {
	case map[string]any:
		for k, sub := range t {
			b.walk(sub, append(at, errs.Named(k)), depth+1)
		}
	case []any:
		for i, sub := range t {
			b.walk(sub, append(at, errs.Indexed(i)), depth+1)
		}
	default:
		if len(at) == 0 {
			return
		}
		b.record(at)
	}
}

// record files a leaf under both lookups. The path is copied because walk
// appends into a shared backing array.
func (b *bodyIndex) record(at errs.Path) {
	p := make(errs.Path, len(at))
	copy(p, at)
	b.leaves++
	b.exact[p.Pointer()] = true
	last := p[len(p)-1]
	if last.IsIndex {
		return
	}
	key := fold(last.Name)
	b.byName[key] = append(b.byName[key], p)
}

// fold matches the way crud.Schema resolves a field reference: case- and
// separator-insensitive, so a client sending orgId, org_id or OrgID all reach
// the column the model calls OrgID.
func fold(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r == '_' || r == '-' || r == ' ' {
			continue
		}
		b.WriteRune(toLower(r))
	}
	return b.String()
}

func toLower(r rune) rune {
	if r >= 'A' && r <= 'Z' {
		return r + ('a' - 'A')
	}
	return r
}
