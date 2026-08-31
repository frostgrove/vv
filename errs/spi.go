package errs

import "context"

type Classifier interface {
	Classify(err error) (*Fault, bool)
}

type Resolver interface {
	Resolve(Path) (Path, bool)
}

type CodeMapper interface {
	CodeFor(f *Fault, v Violation) (Code, bool)
}

type MessageSource interface {
	Message(ctx context.Context, v Violation, locale string) (string, bool)
}

func Chain(rs ...Resolver) Resolver {
	return chain(rs)
}

type chain []Resolver

func (this chain) Resolve(p Path) (Path, bool) {
	for _, r := range this {
		if r == nil {
			continue
		}
		next, ok := r.Resolve(p)
		if !ok {
			return p, false
		}
		p = next
	}
	return p, true
}
