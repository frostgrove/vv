package port

import (
	"context"

	"github.com/frostgrove/vv/errs"
)

// MaxViolations is how many violations one response carries before the rest are
// dropped and Partial is set. A response is not a log.
const MaxViolations = 100

// ViolationOptions is everything the pipeline needs that is not the fault.
// Every field is optional; the zero value renders through the standard
// vocabulary with no catalogue, no hops and no cap.
type ViolationOptions struct {
	// Resolvers are the declared path-translation hops, in order.
	Resolvers []errs.Resolver
	// Fallback runs only for a path no declared hop changed. It is the
	// transport's own guess — over HTTP, the raw-body index — and it is a
	// separate field rather than a last resolver so a declaration always beats
	// it ([[D-043]]).
	Fallback errs.Resolver
	// Messages is the catalogue rung of the ladder.
	Messages errs.MessageSource
	// Codes is the vocabulary the default messages come from. nil is the
	// standard one.
	Codes *errs.Codes
	// Max caps the list. Zero or less means no cap.
	Max int
}

// Violations is the pipeline every transport renders from: the copy, the path
// chain, the sort, the cap and the message ladder, in that order.
//
// It is here rather than in a transport package because none of those five
// steps has a protocol in it, and the second implementation is what settled it:
// an HTTP envelope and a gRPC status detail list are the same five steps and a
// different final shape ([[D-045]]).
//
// The order is load-bearing. Messages come after path translation because the
// ladder is derived from the path — expanding first would key a catalogue entry
// on the model's field name on one deployment and on the client's on another,
// for the same violation. The cap comes after the sort, so what survives is the
// front of a total order rather than whatever the classifier happened to append
// first.
//
// The locale is read from ctx rather than passed, so a transport that installed
// it with [WithLocale] gets the same ladder whichever renderer runs. A caller
// that has no context to carry it uses [WithLocale] on a background one.
//
// The fault is a value two goroutines may render at once ([[D-042]]), so
// nothing here writes through to it: a resolved path or an expanded message
// landing on the shared fault would make the second render depend on the first.
func Violations(ctx context.Context, f *errs.Fault, o ViolationOptions) []errs.Violation {
	if f == nil {
		return nil
	}
	vs := make([]errs.Violation, 0, max(len(f.Violations), 1))
	vs = append(vs, f.Violations...)
	if len(vs) == 0 {
		// A 404 and a bare 403 carry none. The transport class alone is not a
		// thing a client can branch on, so the fault's own code becomes one
		// violation.
		code := f.Code
		if code == "" {
			code = CodeForKind(f.Kind)
		}
		vs = append(vs, errs.Violation{Code: code, Message: f.Message})
	}

	declared := errs.Chain(o.Resolvers...)
	for i := range vs {
		p, ok := declared.Resolve(vs[i].Path)
		// The fallback is for a path nobody translated. A declared hop that
		// changed the path has already named the client's key, and letting a
		// guess re-match its last step against a same-named key elsewhere in
		// the payload would be a guess overturning a declaration — [[D-043]]
		// exactly backwards. An unchanged path is one nothing owned, which is
		// what the fallback is for.
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

// message is `ROADMAP-errors.md` §9's ladder: the catalogue, then the code's
// declared default, then the code itself. Never the driver's text, and never a
// template with an unexpanded placeholder still in it — errs.Messages falls
// back one level up rather than emitting {max}.
func message(ctx context.Context, v errs.Violation, locale string, o ViolationOptions) string {
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

// defaultMessage is the rung below the catalogue: what the vocabulary declares
// for the code.
func defaultMessage(code errs.Code, codes *errs.Codes) (string, bool) {
	if codes != nil {
		return codes.MessageFor(code)
	}
	return DefaultMessage(code)
}
