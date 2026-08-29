package errs

import "context"

// A Classifier turns some other layer's error into a [Fault]. One per driver,
// per ORM, per anything that produces errors of its own.
//
// An implementation must not presuppose that a SQLSTATE exists. SQLite reports
// none at all and never will, so an interface whose contract assumed one would
// be wrong on a quarter of the engines this library supports ([[D-046]]).
//
// A classifier returns a *Fault, and [Fault]'s wrapped errors are unexported:
// attaching the sentinel and the driver error is [Builder.Wrapping]'s job, and
// there is no other way in.
type Classifier interface {
	Classify(err error) (*Fault, bool)
}

// A Resolver translates one hop of a [Path]: a column to a model field, a model
// field to a command field, a command field to the key the client sent. It
// reports false when the hop is not one it owns, and the caller then marks the
// violation approximate rather than shipping a guess ([[D-043]]).
type Resolver interface {
	Resolve(Path) (Path, bool)
}

// A CodeMapper overrides the code a violation carries — a service that wants
// "email_taken" where the classifier said "unique".
type CodeMapper interface {
	CodeFor(f *Fault, v Violation) (Code, bool)
}

// A MessageSource turns a violation into a sentence for a person. The locale is
// a parameter and never a field on the fault: a fault that crosses a queue must
// not carry the locale of the request that made it.
//
// The context is here for a catalogue that does I/O; [Messages] ignores it.
type MessageSource interface {
	Message(ctx context.Context, v Violation, locale string) (string, bool)
}

// There is no Renderer here, and its absence is deliberate. The test for the
// shared half is whether a non-HTTP transport can implement an interface
// without importing net/http, and a renderer returning (int, http.Header, any)
// fails it — gRPC cannot. It lives in port/porthttp, next to the status table
// it is shaped by ([[D-045]]).

// Chain applies resolvers in order, each to what the last one produced.
//
// A nil hop is skipped. If any hop declines, the path is returned as it was
// transformed up to that point and the result is false — the caller keeps the
// partial translation and marks the violation approximate, which is what makes
// [[D-043]]'s "do not guess a hop you do not own" a mechanism rather than a
// sentence. Chain of nothing is the identity and reports true.
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
