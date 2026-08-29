package errs

// P is the shape of a message template's parameters. It is an alias, not a
// defined type, so a plain map[string]any built somewhere else passes with no
// conversion.
type P = map[string]any

// A Builder builds a [Fault] by hand. A service layer is a first-class producer
// of violations, not an afterthought:
//
//	return errs.Validation().
//		Field("Age").Code("too_young").Params(errs.P{"min": 18}).
//		Fault()
//
// Field names the model field. Turning it into ["user","age"] on the way out is
// the job of the layer that performed that mapping and of no other ([[D-043]]).
//
// One rule resolves the ambiguity that chain creates. [Builder.Code],
// [Builder.Params] and [Builder.Message] apply to the violation opened by the
// most recent [Builder.Field], [Builder.At] or [Builder.General]; before any
// violation is opened, Code and Message apply to the fault itself. The four
// steps that have no fault-level meaning — Params, [Builder.Origin],
// [Builder.Source] and [Builder.Approximate] — open a general violation rather
// than being dropped, so a misordered chain produces a visibly odd fault rather
// than a silently empty one.
type Builder struct {
	f Fault
	// open indexes the violation the per-violation steps apply to, or -1.
	open int
}

// New starts a fault of the given kind.
func New(kind Kind) *Builder { return &Builder{f: Fault{Kind: kind}, open: -1} }

// Validation starts a 422-class fault: the request was well-formed and its
// values were refused.
func Validation() *Builder { return New(KindValidation) }

// Conflict starts a 409-class fault: it collided with what is stored.
func Conflict() *Builder { return New(KindConflict) }

// NotFound starts a 404-class fault.
func NotFound() *Builder { return New(KindNotFound) }

// Forbidden starts a 403-class fault.
func Forbidden() *Builder { return New(KindForbidden) }

// Unauthorized starts a 401-class fault.
func Unauthorized() *Builder { return New(KindUnauthorized) }

// BadRequest starts a 400-class fault: the request itself was malformed.
func BadRequest() *Builder { return New(KindBadRequest) }

// TooLarge starts a 413-class fault: the request body is past the cap the
// transport reads to. Nothing in it was parsed, so it carries no field.
func TooLarge() *Builder { return New(KindTooLarge) }

// Retryable starts a fault nothing the caller sent is wrong about ([[D-040]]).
func Retryable() *Builder { return New(KindRetryable) }

// Internal starts a fault that says nothing.
func Internal() *Builder { return New(KindInternal) }

// Field opens a violation at a single-step path.
func (this *Builder) Field(name string) *Builder { return this.At(Path{Named(name)}) }

// At opens a violation at a path.
func (this *Builder) At(p Path) *Builder {
	this.f.Violations = append(this.f.Violations, Violation{Path: p})
	this.open = len(this.f.Violations) - 1
	return this
}

// General opens a violation with no path, for something the client cannot fix
// by editing one field.
func (this *Builder) General() *Builder { return this.At(nil) }

// current returns the open violation, opening a general one if a per-violation
// step arrived before any Field/At/General.
func (this *Builder) current() *Violation {
	if this.open < 0 {
		this.General()
	}
	return &this.f.Violations[this.open]
}

// Code sets the open violation's code, or the fault's if none is open.
func (this *Builder) Code(c Code) *Builder {
	if this.open < 0 {
		this.f.Code = c
		return this
	}
	this.f.Violations[this.open].Code = c
	return this
}

// Params merges into the open violation's parameters. Merging rather than
// replacing, so a second call cannot silently drop a placeholder the first one
// set.
func (this *Builder) Params(p P) *Builder {
	v := this.current()
	if v.Params == nil {
		v.Params = make(P, len(p))
	}
	for k, val := range p {
		v.Params[k] = val
	}
	return this
}

// Message sets the open violation's message, or the fault's developer-facing
// message if none is open. A violation's message is normally left to the
// [MessageSource]; this is for a rule whose text has nowhere else to live.
func (this *Builder) Message(s string) *Builder {
	if this.open < 0 {
		this.f.Message = s
		return this
	}
	this.f.Violations[this.open].Message = s
	return this
}

// Origin marks the open violation as input-shaped or state-shaped.
func (this *Builder) Origin(o Origin) *Builder {
	this.current().Origin = o
	return this
}

// Source attaches storage provenance to the open violation.
func (this *Builder) Source(s Source) *Builder {
	this.current().Source = s
	return this
}

// Approximate marks the open violation's path as unresolved rather than
// resolved ([[D-043]]).
func (this *Builder) Approximate(yes bool) *Builder {
	this.current().Approximate = yes
	return this
}

// Op names the repository verb.
func (this *Builder) Op(op string) *Builder {
	this.f.Op = op
	return this
}

// Entity names the model.
func (this *Builder) Entity(e string) *Builder {
	this.f.Entity = e
	return this
}

// Partial says a cap was hit and the set is incomplete. A capped answer says so
// rather than listing four violations in a way that implies there are four.
func (this *Builder) Partial(yes bool) *Builder {
	this.f.Partial = yes
	return this
}

// Detail attaches the internal provenance. Nothing in it is ever rendered.
func (this *Builder) Detail(d Detail) *Builder {
	this.f.Detail = d
	return this
}

// Wrapping attaches the errors this fault describes, and is the only way
// anything reaches [Fault.Unwrap].
//
// It is what keeps a fault additive: the driver error and the crud sentinel go
// in here, so errors.Is(err, crud.ErrConflict) stays true and no transport
// needs to learn a new type ([[D-038]]). It is also why a third-party
// [Classifier] cannot forge a match — it returns a *Fault, and the field behind
// this step is unexported.
func (this *Builder) Wrapping(errs ...error) *Builder {
	for _, err := range errs {
		if err != nil {
			this.f.wrapped = append(this.f.wrapped, err)
		}
	}
	return this
}

// Fault returns the built fault. The violations are copied all the way down —
// path, params and column lists — so a builder that is reused cannot mutate a
// fault it already handed back, and a caller's scratch [Path] does not stay
// live inside one.
//
// A shallow copy would leave two faults from one builder sharing one Path
// array. A resolver that rewrites a hop in place, which [[D-043]] invites, then
// rewrites every fault that builder ever produced.
func (this *Builder) Fault() *Fault {
	f := this.f
	f.Detail.Columns = cloneStrings(this.f.Detail.Columns)
	f.Detail.RefColumns = cloneStrings(this.f.Detail.RefColumns)
	if n := len(this.f.Violations); n > 0 {
		f.Violations = make([]Violation, n)
		copy(f.Violations, this.f.Violations)
		for i := range f.Violations {
			v := &f.Violations[i]
			if len(v.Path) > 0 {
				v.Path = append(Path(nil), v.Path...)
			}
			v.Source.Columns = cloneStrings(v.Source.Columns)
			if p := v.Params; p != nil {
				v.Params = make(P, len(p))
				for k, val := range p {
					v.Params[k] = val
				}
			}
		}
	}
	if n := len(this.f.wrapped); n > 0 {
		f.wrapped = make([]error, n)
		copy(f.wrapped, this.f.wrapped)
	}
	return &f
}

// cloneStrings keeps a nil nil: a column list that was absent must not come
// back as an empty one, which reads as "no columns" instead of "not known".
func cloneStrings(s []string) []string {
	if s == nil {
		return nil
	}
	out := make([]string, len(s))
	copy(out, s)
	return out
}
