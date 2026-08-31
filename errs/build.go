package errs

type P = map[string]any

type Builder struct {
	f Fault

	open int
}

func New(kind Kind) *Builder { return &Builder{f: Fault{Kind: kind}, open: -1} }

func Validation() *Builder { return New(KindValidation) }

func Conflict() *Builder { return New(KindConflict) }

func NotFound() *Builder { return New(KindNotFound) }

func Forbidden() *Builder { return New(KindForbidden) }

func Unauthorized() *Builder { return New(KindUnauthorized) }

func BadRequest() *Builder { return New(KindBadRequest) }

func TooLarge() *Builder { return New(KindTooLarge) }

func MethodNotAllowed() *Builder { return New(KindMethodNotAllowed) }

func Retryable() *Builder { return New(KindRetryable) }

func Internal() *Builder { return New(KindInternal) }

func (this *Builder) Field(name string) *Builder { return this.At(Path{Named(name)}) }

func (this *Builder) At(p Path) *Builder {
	this.f.Violations = append(this.f.Violations, Violation{Path: p})
	this.open = len(this.f.Violations) - 1
	return this
}

func (this *Builder) General() *Builder { return this.At(nil) }

func (this *Builder) current() *Violation {
	if this.open < 0 {
		this.General()
	}
	return &this.f.Violations[this.open]
}

func (this *Builder) Code(c Code) *Builder {
	if this.open < 0 {
		this.f.Code = c
		return this
	}
	this.f.Violations[this.open].Code = c
	return this
}

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

func (this *Builder) Message(s string) *Builder {
	if this.open < 0 {
		this.f.Message = s
		return this
	}
	this.f.Violations[this.open].Message = s
	return this
}

func (this *Builder) Origin(o Origin) *Builder {
	this.current().Origin = o
	return this
}

func (this *Builder) Source(s Source) *Builder {
	this.current().Source = s
	return this
}

func (this *Builder) Approximate(yes bool) *Builder {
	this.current().Approximate = yes
	return this
}

func (this *Builder) Op(op string) *Builder {
	this.f.Op = op
	return this
}

func (this *Builder) Entity(e string) *Builder {
	this.f.Entity = e
	return this
}

func (this *Builder) Partial(yes bool) *Builder {
	this.f.Partial = yes
	return this
}

func (this *Builder) Detail(d Detail) *Builder {
	this.f.Detail = d
	return this
}

func (this *Builder) Wrapping(errs ...error) *Builder {
	for _, err := range errs {
		if err != nil {
			this.f.wrapped = append(this.f.wrapped, err)
		}
	}
	return this
}

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

func cloneStrings(s []string) []string {
	if s == nil {
		return nil
	}
	out := make([]string, len(s))
	copy(out, s)
	return out
}
