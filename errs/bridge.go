package errs

import "strings"

type FieldViolation interface {
	Namespace() string
	Tag() string
	Param() string
	Value() any
}

func FromFieldViolations[T FieldViolation](root string, vs ...T) []Violation {
	if len(vs) == 0 {
		return nil
	}
	out := make([]Violation, 0, len(vs))
	for _, fv := range vs {
		ns := fv.Namespace()
		if root != "" {
			ns = strings.TrimPrefix(ns, root+".")
		}
		v := Violation{
			Path:   ParsePath(ns),
			Code:   Code(fv.Tag()),
			Origin: OriginInput,
		}
		if p := fv.Param(); p != "" {
			v.Params = P{"param": p}
		}
		if val := fv.Value(); val != nil {
			if v.Params == nil {
				v.Params = P{}
			}
			v.Params["value"] = val
		}
		out = append(out, v)
	}
	return out
}
