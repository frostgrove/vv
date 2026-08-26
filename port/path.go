package port

import "github.com/frostgrove/vv/errs"

// Fields is the service's own hop of the path chain: the model's field name to
// the path the command uses for it. A service defined its command shape, so it
// is the only layer that can translate that hop ([[D-043]]).
//
// The head step is what is looked up, and the rest of the path rides along —
// a violation at Address.Line1 whose head maps to ["shipping"] comes out as
// ["shipping","Line1"].
//
// An undeclared head passes through rather than declining, and that is the one
// judgement in this type. A declining hop poisons errs.Chain: everything after
// it is dropped and the violation is marked approximate, which would take a
// path the raw-body index resolves today and make it worse. A hand-written
// Fields is partial by nature, so an undeclared head is an ordinary gap.
//
// Strictness belongs to [PathMap], which is generated, total by construction
// and checked against the model at package initialisation — so for it an
// undeclared head is not a gap and declining is the honest answer ([[D-050]]).
// The two also differ under a leading index: PathMap translates the first named
// step past one, Fields returns the path unchanged, because a hand-written map
// may declare a key called "3" and silently ignoring it would be worse than not
// looking.
type Fields map[string]errs.Path

// Resolve implements errs.Resolver.
func (f Fields) Resolve(p errs.Path) (errs.Path, bool) {
	if len(f) == 0 || len(p) == 0 || p[0].IsIndex {
		return p, true
	}
	to, ok := f[p[0].Name]
	if !ok {
		return p, true
	}
	// A fresh slice. The declared path is shared by every request that hits this
	// field: appending a tail onto it writes into whatever spare capacity it
	// has, so two concurrent resolutions would rewrite each other's last step —
	// a corrupted field path under load, and only under load.
	out := make(errs.Path, 0, len(to)+len(p)-1)
	out = append(out, to...)
	out = append(out, p[1:]...)
	return out, true
}

// Hops answers the path-translation hops this layer contributes, in order: the
// service's, then the mapper's when it declares one.
//
// A binding wires these ahead of the raw-body fallback, so a declared mapping
// always beats a guess ([[D-043]]). An empty result is the ordinary case and
// means the binding keeps its shared default renderer.
func Hops[M any, ID comparable, U any, In any](svc Service[M, ID, U], mapper Mapper[In, M]) []errs.Resolver {
	var out []errs.Resolver
	if svc != nil {
		if r := svc.Paths(); r != nil {
			out = append(out, r)
		}
	}
	if r, ok := any(mapper).(errs.Resolver); ok && r != nil {
		out = append(out, r)
	}
	return out
}
