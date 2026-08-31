package port

import (
	"context"

	"github.com/frostgrove/vv/errs"
)

const MaxViolations = 100

type ViolationOptions struct {
	Resolvers []errs.Resolver

	Fallback errs.Resolver

	Messages errs.MessageSource

	Codes *errs.Codes

	Max int
}

func Violations(ctx context.Context, f *errs.Fault, o *ViolationOptions) []errs.Violation {
	if f == nil {
		return nil
	}
	if o == nil {
		o = &ViolationOptions{}
	}
	vs := make([]errs.Violation, 0, max(len(f.Violations), 1))
	vs = append(vs, f.Violations...)
	if len(vs) == 0 {
		code := f.Code
		if code == "" {
			code = CodeForKind(f.Kind)
		}
		vs = append(vs, errs.Violation{Code: code, Message: f.Message})
	}

	declared := errs.Chain(o.Resolvers...)
	for i := range vs {
		p, ok := declared.Resolve(vs[i].Path)

		if ok && o.Fallback != nil && samePath(p, vs[i].Path) {
			p, ok = o.Fallback.Resolve(p)
		}
		vs[i].Path = p
		if !ok {
			vs[i].Approximate = true
		}
	}

	errs.SortViolations(vs)
	if o.Max > 0 && len(vs) > o.Max {
		vs = vs[:o.Max]
	}
	locale := LocaleFrom(ctx)
	for i := range vs {
		vs[i].Message = message(ctx, vs[i], locale, o)
	}
	return vs
}

func samePath(a, b errs.Path) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func message(ctx context.Context, v errs.Violation, locale string, o *ViolationOptions) string {
	if o.Messages != nil {
		if m, ok := o.Messages.Message(ctx, v, locale); ok && m != "" {
			return m
		}
	}
	if v.Message != "" {
		return v.Message
	}
	if m, ok := defaultMessage(v.Code, o.Codes); ok {
		return m
	}
	return string(v.Code)
}

func defaultMessage(code errs.Code, codes *errs.Codes) (string, bool) {
	if codes != nil {
		return codes.MessageFor(code)
	}
	return DefaultMessage(code)
}
