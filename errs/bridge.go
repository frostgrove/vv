package errs

import "strings"

// FieldViolation is the shape a validation library's error already has.
//
// go-playground/validator's FieldError satisfies it structurally, so neither
// package imports the other and the bridge costs no dependency ([[D-033]]).
// Same trick and same reason as adapter/crudsql asking a driver error for a
// SQLSTATE by shape.
//
// The cost of structural satisfaction is worth naming: these signatures have to
// match the library's exactly, and if one of them ever changes the failure is
// not a compile error here — it is a failed type assertion at the call site. The
// assertion that guards it lives in test/bridge, which is the one place in the
// repository allowed to import a validator.
type FieldViolation interface {
	Namespace() string
	Tag() string
	Param() string
	Value() any
}

// FromFieldViolations converts a validation library's errors into violations.
//
//	verrs := err.(validator.ValidationErrors)
//	vs := errs.FromFieldViolations("CreateUserRequest", verrs...)
//
// T is inferred, so the call site stays one line even though the library's type
// is never named here.
//
// root is the name the library puts at the front of Namespace() — the struct it
// was handed. It is stripped only when the namespace actually carries it. An
// unconditional first-segment drop would turn a mistyped root into a silently
// wrong path (user.email becoming email); match-or-keep leaves one visibly
// extra step instead.
//
// Tag() becomes the [Code], Namespace() becomes the [Path] — with Items[3].Email
// parsing straight into an index step — and Param() and Value() go into
// [Violation.Params] for a message template. Everything here is [OriginInput]:
// it is the caller's own payload, and nothing was read from storage to find it.
//
// A consumer must register validator's tag-name function, or Namespace()
// reports Go field names and every path is quietly wrong. That is a start-up
// step, not a runtime surprise.
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
