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

	resolvers := append(HopsFrom(ctx), o.Resolvers...)
	declared := errs.Chain(resolvers...)
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
		vs[i].Message, vs[i].MessageLocale = message(ctx, vs[i], locale, o)
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

func message(ctx context.Context, v errs.Violation, locale string, o *ViolationOptions) (string, string) {
	if o.Messages != nil {
		if localized, ok := o.Messages.(errs.LocalizedMessageSource); ok {
			if m, actual, found := localized.MessageWithLocale(ctx, v, locale); found && m != "" {
				if !validMessageLocale(actual) {
					actual = ""
				}
				return m, actual
			}
		} else if m, ok := o.Messages.Message(ctx, v, locale); ok && m != "" {
			return m, ""
		}
	}
	if v.Message != "" {
		return v.Message, ""
	}
	if o.Codes != nil {
		if m, ok := o.Codes.Message(ctx, v, locale); ok && m != "" {
			return m, ""
		}
		return string(v.Code), ""
	}
	if m, ok := DefaultMessage(v.Code); ok {
		return m, ""
	}
	return string(v.Code), ""
}

func validMessageLocale(locale string) bool {
	if locale == "" || len(locale) > 128 {
		return false
	}
	previousHyphen := true
	for _, r := range locale {
		letter := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'
		digit := r >= '0' && r <= '9'
		if letter || digit {
			previousHyphen = false
			continue
		}
		if r != '-' || previousHyphen {
			return false
		}
		previousHyphen = true
	}
	return !previousHyphen
}
