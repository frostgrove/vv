package porthttp

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"

	"github.com/frostgrove/vv/errs"
)

const (
	maxBodyDepth  = 32
	maxBodyLeaves = 4096
)

func BodyResolver(raw []byte) errs.Resolver {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	idx := &bodyIndex{exact: map[string]bool{}, byName: map[string][]errs.Path{}}
	idx.index(raw)
	return idx
}

func bodyResolverFrom(ctx context.Context) errs.Resolver {
	return BodyResolver(BodyFrom(ctx))
}

type bodyIndex struct {
	exact  map[string]bool
	byName map[string][]errs.Path
	leaves int
}

func (this *bodyIndex) index(raw []byte) {
	raw = bytes.TrimSpace(raw)
	if raw[0] != '{' && raw[0] != '[' {
		return
	}
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return
	}
	this.walk(doc, nil, 0)
}

func (this *bodyIndex) Resolve(p errs.Path) (errs.Path, bool) {
	if len(p) == 0 {
		return p, true
	}

	if this.exact[p.Pointer()] {
		return p, true
	}
	last := p[len(p)-1]
	if last.IsIndex {
		return p, false
	}
	got := this.byName[fold(last.Name)]
	if len(got) != 1 {
		return p, false
	}
	return got[0], true
}

func (this *bodyIndex) walk(v any, at errs.Path, depth int) {
	if depth > maxBodyDepth || this.leaves >= maxBodyLeaves {
		return
	}
	switch t := v.(type) {
	case map[string]any:
		for k, sub := range t {
			this.walk(sub, append(at, errs.Named(k)), depth+1)
		}
	case []any:
		for i, sub := range t {
			this.walk(sub, append(at, errs.Indexed(i)), depth+1)
		}
	default:
		if len(at) == 0 {
			return
		}
		this.record(at)
	}
}

func (this *bodyIndex) record(at errs.Path) {
	p := make(errs.Path, len(at))
	copy(p, at)
	this.leaves++
	this.exact[p.Pointer()] = true
	last := p[len(p)-1]
	if last.IsIndex {
		return
	}
	key := fold(last.Name)
	this.byName[key] = append(this.byName[key], p)
}

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
