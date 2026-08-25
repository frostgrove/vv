package port

import "context"

// A Mapper turns a transport's input type into the model. It is the resource
// adapter of the layering: it sits before the Service, so the Service never
// sees a type that came off a wire.
//
// It may also implement errs.Resolver. A mapper that does contributes the
// adapter's hop to the path chain, because it is the layer that performed the
// mapping and is therefore the only one that can invert it ([[D-043]]). It is a
// separate optional interface rather than a second method, so a hand-written
// mapper is not forced to write a path map it has no use for.
type Mapper[In, M any] interface {
	Model(ctx context.Context, in In) (M, error)
}

// Identity is the mapper a binding installs when the body binds straight onto
// the model, which is what New means. One code path for both constructors: the
// alternative is a nil check on every write route, and the two branches drift.
func Identity[M any]() Mapper[M, M] { return identity[M]{} }

type identity[M any] struct{}

func (identity[M]) Model(_ context.Context, in M) (M, error) { return in, nil }
