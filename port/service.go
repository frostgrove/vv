package port

import (
	"context"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/query"
	"github.com/frostgrove/vv/errs"
)

// A Service is what a transport talks to. It takes commands and answers models,
// and it is where the orchestration lives: what a create request may not
// dictate, what a count drops from the query document, what a PUT has to find
// before it replaces.
//
// Three type parameters and not four. The Mapper runs before the Service, so a
// transport's input type never reaches here — which also means Service infers
// exactly as Repository does, and one value mounts on every binding.
type Service[M any, ID comparable, U any] interface {
	// Meta is the model this service serves. No route calls it, and it is here
	// anyway: it is the one method every service already has — a repository
	// answers it and an embedder gets it free — and it is what makes a Service
	// self-describing for a transport that has to name its resource. Adding it
	// later would break every implementation.
	Meta() *crud.Meta

	List(ctx context.Context, cmd ListCommand) (crud.PaginatedResponse[M], error)
	Count(ctx context.Context, cmd CountCommand) (int64, error)
	Get(ctx context.Context, cmd GetCommand[ID]) (M, error)

	Create(ctx context.Context, cmd CreateCommand[M]) (M, error)
	Update(ctx context.Context, cmd UpdateCommand[ID, U]) (M, error)
	Replace(ctx context.Context, cmd ReplaceCommand[ID, M]) (M, error)
	Delete(ctx context.Context, cmd DeleteCommand[ID]) (int64, error)
	DeleteMany(ctx context.Context, cmd BulkDeleteCommand[ID]) (int64, error)

	// Paths is this service's hop of the path chain — the model's field names
	// to its command's. nil is legal and means the service adds no hop.
	Paths() errs.Resolver
}

// A ServiceOption configures the default service. It carries no type
// parameters because nothing it sets mentions one, which is what lets a binding
// translate its own options into these without spelling any generics.
type ServiceOption func(*serviceConfig)

type serviceConfig struct {
	query         *query.Config
	paths         errs.Resolver
	allowClientID bool
}

// WithQuery bounds what clients may filter, sort, select and preload.
func WithQuery(cfg *query.Config) ServiceOption {
	return func(c *serviceConfig) { c.query = cfg }
}

// AllowClientID lets a create request carry its own primary key even when the
// database would generate one.
func AllowClientID() ServiceOption {
	return func(c *serviceConfig) { c.allowClientID = true }
}

// WithPaths declares this service's hop of the path chain.
func WithPaths(r errs.Resolver) ServiceOption {
	return func(c *serviceConfig) { c.paths = r }
}

// A DefaultService is the Service every binding builds when it is handed a
// repository rather than a service. It is the orchestration and nothing else:
// no business rules, and no knowledge of how the request arrived.
type DefaultService[M any, ID comparable, U any] struct {
	repo Repository[M, ID, U]
	meta *crud.Meta
	cfg  *query.Config

	paths         errs.Resolver
	allowClientID bool
}

// NewService builds the default service over a repository.
func NewService[M any, ID comparable, U any](repo Repository[M, ID, U], opts ...ServiceOption) *DefaultService[M, ID, U] {
	var c serviceConfig
	for _, o := range opts {
		if o != nil {
			o(&c)
		}
	}
	return &DefaultService[M, ID, U]{
		repo:          repo,
		meta:          repo.Meta(),
		cfg:           c.query,
		paths:         c.paths,
		allowClientID: c.allowClientID,
	}
}

var _ Service[struct{}, int, struct{}] = (*DefaultService[struct{}, int, struct{}])(nil)

// Meta implements [Service].
func (s *DefaultService[M, ID, U]) Meta() *crud.Meta { return s.meta }

// Paths implements [Service].
func (s *DefaultService[M, ID, U]) Paths() errs.Resolver { return s.paths }

// List implements [Service].
func (s *DefaultService[M, ID, U]) List(ctx context.Context, cmd ListCommand) (crud.PaginatedResponse[M], error) {
	opts, err := s.compile(cmd.Query, cmd.Options)
	if err != nil {
		return crud.PaginatedResponse[M]{}, err
	}
	return s.repo.Get(ctx, opts...)
}

// Count implements [Service].
func (s *DefaultService[M, ID, U]) Count(ctx context.Context, cmd CountCommand) (int64, error) {
	req := cmd.Query
	if req == nil {
		req = &query.Request{}
	}
	NarrowForCount(req)

	opts, err := s.compile(req, cmd.Options)
	if err != nil {
		return 0, err
	}
	return s.repo.Count(ctx, opts...)
}

// Get implements [Service].
func (s *DefaultService[M, ID, U]) Get(ctx context.Context, cmd GetCommand[ID]) (M, error) {
	var zero M
	req := cmd.Query
	if req == nil {
		req = &query.Request{}
	}
	NarrowForEntity(req)

	opts, err := s.compile(req, cmd.Options)
	if err != nil {
		return zero, err
	}
	return s.repo.GetByID(ctx, cmd.ID, opts...)
}

// Create implements [Service].
//
// The order is the guarantee: what a client may not choose is cleared first,
// and only then does the hook see the model. A hook that ran before the
// clearing would be handed a client-chosen key and a forged timestamp
// ([[UC-013]] guarantee 7).
func (s *DefaultService[M, ID, U]) Create(ctx context.Context, cmd CreateCommand[M]) (M, error) {
	var zero M
	m := cmd.Model
	if err := Sanitize(s.meta, &m, s.allowClientID); err != nil {
		return zero, err
	}
	if cmd.Before != nil {
		if err := cmd.Before(&m); err != nil {
			return zero, err
		}
	}
	if err := s.repo.Save(ctx, &m); err != nil {
		return zero, err
	}
	return m, nil
}

// Update implements [Service].
func (s *DefaultService[M, ID, U]) Update(ctx context.Context, cmd UpdateCommand[ID, U]) (M, error) {
	var zero M
	patch := cmd.Patch
	if cmd.Before != nil {
		if err := cmd.Before(&patch); err != nil {
			return zero, err
		}
	}
	return s.repo.Update(ctx, cmd.ID, patch)
}

// Replace implements [Service].
//
// When the database generates the key, a replace never creates: the key has to
// name a row that already exists. Otherwise it is the way around AllowClientID
// — a client cannot pick its key on create but could name one here — and on
// PostgreSQL an explicit insert into a serial column does not advance the
// sequence, so the next create collides on the primary key and keeps colliding
// until somebody repairs the sequence by hand ([[D-012]]). A key the client
// owns (a uuid, a slug) is a different matter and is still created.
func (s *DefaultService[M, ID, U]) Replace(ctx context.Context, cmd ReplaceCommand[ID, M]) (M, error) {
	var zero M
	if s.meta.PK.Auto && !s.allowClientID {
		if _, err := s.repo.GetByID(ctx, cmd.ID); err != nil {
			return zero, err
		}
	}
	m := cmd.Model
	if err := ClearGenerated(s.meta, &m); err != nil {
		return zero, err
	}
	if err := s.meta.SetID(&m, cmd.ID); err != nil {
		return zero, BadRequestAs(errs.CodeInvalidID, nil, "%s", err)
	}
	if cmd.Before != nil {
		if err := cmd.Before(&m); err != nil {
			return zero, err
		}
	}
	if err := s.repo.Save(ctx, &m); err != nil {
		return zero, err
	}
	return m, nil
}

// Delete implements [Service]. Removing nothing is crud.ErrNotFound: the caller
// named one row, and it was not there.
func (s *DefaultService[M, ID, U]) Delete(ctx context.Context, cmd DeleteCommand[ID]) (int64, error) {
	n, err := s.repo.Delete(ctx, cmd.ID)
	if err != nil {
		return 0, err
	}
	if n == 0 {
		return 0, crud.ErrNotFound
	}
	return n, nil
}

// DeleteMany implements [Service]. An empty set never reaches the repository:
// asking a database to delete nothing is a statement with no rows in it.
func (s *DefaultService[M, ID, U]) DeleteMany(ctx context.Context, cmd BulkDeleteCommand[ID]) (int64, error) {
	if len(cmd.IDs) == 0 {
		return 0, nil
	}
	return s.repo.Delete(ctx, cmd.IDs...)
}

// compile turns the query document into repository options and appends the
// caller's. Appended and not merged: crud.Where ANDs, so a transport scope
// narrows the client's filter instead of replacing it ([[D-004]]).
func (s *DefaultService[M, ID, U]) compile(req *query.Request, extra []crud.Option) ([]crud.Option, error) {
	if req == nil {
		req = &query.Request{}
	}
	opts, err := req.Compile(s.meta, s.cfg)
	if err != nil {
		return nil, err
	}
	return append(opts, extra...), nil
}
