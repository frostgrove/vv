package probe

import (
	"context"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/errs"
)

type Handler interface {
	Enrich(ctx context.Context, request *Request) (*errs.Fault, error)
}

type Declarer interface {
	Declare(meta *crud.Meta) (Handler, error)
}

type Savepointer interface {
	Savepoints() (want bool, budget int)
}

type Request struct {
	Op string

	Fault *errs.Fault

	Meta *crud.Meta

	Source crud.Source

	Rows []Row

	Batch bool

	Upsert bool

	Stored bool

	Recovered bool

	Resolve func(table string, columns []string) (errs.Path, bool)
}

type Row struct {
	Values map[string]any

	ID    any
	HasID bool
}

func Simple() Handler { return simple{} }

type simple struct{}

func (simple) Enrich(_ context.Context, request *Request) (*errs.Fault, error) {
	if request == nil {
		return nil, nil
	}
	return request.Fault, nil
}

func (this simple) Declare(*crud.Meta) (Handler, error) { return this, nil }

var (
	_ Handler  = simple{}
	_ Declarer = simple{}
)
