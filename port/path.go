package port

import "github.com/frostgrove/vv/errs"

type Fields map[string]errs.Path

func (this Fields) Resolve(p errs.Path) (errs.Path, bool) {
	if len(this) == 0 || len(p) == 0 || p[0].IsIndex {
		return p, true
	}
	to, ok := this[p[0].Name]
	if !ok {
		return p, true
	}

	out := make(errs.Path, 0, len(to)+len(p)-1)
	out = append(out, to...)
	out = append(out, p[1:]...)
	return out, true
}

func Hops[M any, ID comparable, U any, In any](service Service[M, ID, U], mapper Mapper[In, M]) []errs.Resolver {
	var out []errs.Resolver
	if service != nil {
		if r := service.Paths(); r != nil {
			out = append(out, r)
		}
	}
	if r, ok := any(mapper).(errs.Resolver); ok && r != nil {
		out = append(out, r)
	}
	return out
}
