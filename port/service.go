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

// RestorableService is the optional application-usecase surface for a
// soft-deleting resource. Bindings may expose restore routes only when their
// declaration receives this capability; ordinary Service stays minimal for
// hard-delete resources and hand-written implementations.
type RestorableService[ID comparable] interface {
	Restore(ctx context.Context, cmd RestoreCommand[ID]) (int64, error)
	RestoreMany(ctx context.Context, cmd BulkRestoreCommand[ID]) (int64, error)
}

type restorableRepository[ID comparable] interface {
	Restore(ctx context.Context, ids ...ID) (int64, error)
}

type restorableProvider[ID comparable] interface {
	Restorable() (RestorableService[ID], bool)
}

// RestorableOf resolves the optional lifecycle application use cases without
// making every ordinary Service structurally restorable. A hand-written
// service may implement RestorableService directly; DefaultService publishes a
// capability-preserving wrapper only when its repository really supports a
// tombstone lifecycle.
func RestorableOf[ID comparable](service any) (RestorableService[ID], bool) {
	if direct, ok := service.(RestorableService[ID]); ok {
		return direct, true
	}
	provider, ok := service.(restorableProvider[ID])
	if !ok {
		return nil, false
	}
	return provider.Restorable()
}

// A ServiceOption configures the default service. It carries no type
// parameters because nothing it sets mentions one, which is what lets a binding
// translate its own options into these without spelling any generics.
type ServiceOption func(*serviceConfig)

type serviceConfig struct {
	query         *query.Config
	queryVariants map[string]*query.Config
	querySelector QuerySelector
	paths         errs.Resolver
	allowClientID bool
}

// WithQuery bounds what clients may filter, sort, select and preload.
func WithQuery(config *query.Config) ServiceOption {
	return func(c *serviceConfig) {
		c.query, c.queryVariants, c.querySelector = config, nil, nil
	}
}

// QuerySelector selects a named query vocabulary for one request. Returning
// the empty string uses the default configuration; any other value must name a
// configuration declared in [WithQueryFor]. It is a vocabulary decision, not a
// row-level policy: use security.Gate for the latter.
type QuerySelector func(context.Context) string

// WithQueryFor declares a default query vocabulary and named alternatives for
// the same route. The selector normally reads the authenticated principal from
// context, letting an administrator receive a wider *declared* vocabulary
// without a second mount or a hand-written service.
//
// Every configuration is resolved against the model when NewService runs. An
// empty or unknown selector result never falls through to an unrestricted
// configuration: empty chooses default, unknown is a query refusal.
func WithQueryFor(defaultConfig *query.Config, variants map[string]*query.Config, selectConfig QuerySelector) ServiceOption {
	if defaultConfig == nil {
		panic("port.WithQueryFor: default query config is nil")
	}
	if selectConfig == nil {
		panic("port.WithQueryFor: query selector is nil")
	}
	copy := make(map[string]*query.Config, len(variants))
	for key, config := range variants {
		if key == "" {
			panic("port.WithQueryFor: an alternative query config has an empty name")
		}
		if config == nil {
			panic("port.WithQueryFor: query config " + key + " is nil")
		}
		copy[key] = config
	}
	return func(c *serviceConfig) {
		c.query, c.queryVariants, c.querySelector = defaultConfig, copy, selectConfig
	}
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
	repository    Repository[M, ID, U]
	restorer      restorableRepository[ID]
	meta          *crud.Meta
	config        *query.Config
	queryVariants map[string]*query.Config
	querySelector QuerySelector

	paths         errs.Resolver
	allowClientID bool
}

// NewService builds the default service over a repository.
func NewService[M any, ID comparable, U any](repository Repository[M, ID, U], options ...ServiceOption) *DefaultService[M, ID, U] {
	var c serviceConfig
	for _, o := range options {
		if o != nil {
			o(&c)
		}
	}
	// A query allow-list is a declaration, not request data. Resolve it while
	// the route is being built so a misspelt field stops the process next to the
	// declaration instead of making every client request fail later. Every stock
	// HTTP and gRPC binding constructs this service, giving all transports the
	// same start-up guarantee.
	if c.query != nil {
		c.query.MustCheck(repository.Meta())
	}
	for _, config := range c.queryVariants {
		config.MustCheck(repository.Meta())
	}
	service := &DefaultService[M, ID, U]{
		repository:    repository,
		meta:          repository.Meta(),
		config:        c.query,
		queryVariants: c.queryVariants,
		querySelector: c.querySelector,
		paths:         c.paths,
		allowClientID: c.allowClientID,
	}
	if restorer, ok := repository.(restorableRepository[ID]); ok {
		// Stock repositories expose a convenience Restore method even for a
		// hard-delete blueprint, so its explicit capability probe wins. A custom
		// repository with a real optional method needs no additional marker.
		supported := true
		if probe, ok := any(repository).(interface{ SupportsRestore() bool }); ok {
			supported = probe.SupportsRestore()
		}
		if supported {
			service.restorer = restorer
		}
	}
	return service
}

var _ Service[struct{}, int, struct{}] = (*DefaultService[struct{}, int, struct{}])(nil)

// Meta implements [Service].
func (this *DefaultService[M, ID, U]) Meta() *crud.Meta { return this.meta }

// Paths implements [Service].
func (this *DefaultService[M, ID, U]) Paths() errs.Resolver { return this.paths }

// List implements [Service].
func (this *DefaultService[M, ID, U]) List(ctx context.Context, cmd ListCommand) (crud.PaginatedResponse[M], error) {
	options, err := this.compile(ctx, cmd.Query, cmd.Options)
	if err != nil {
		return crud.PaginatedResponse[M]{}, err
	}
	return this.repository.Get(ctx, options...)
}

// Count implements [Service].
func (this *DefaultService[M, ID, U]) Count(ctx context.Context, cmd CountCommand) (int64, error) {
	request := requestCopy(cmd.Query)
	NarrowForCount(request)

	options, err := this.compile(ctx, request, cmd.Options)
	if err != nil {
		return 0, err
	}
	return this.repository.Count(ctx, options...)
}

// Get implements [Service].
func (this *DefaultService[M, ID, U]) Get(ctx context.Context, cmd GetCommand[ID]) (M, error) {
	var zero M
	request := requestCopy(cmd.Query)
	NarrowForEntity(request)

	options, err := this.compile(ctx, request, cmd.Options)
	if err != nil {
		return zero, err
	}
	return this.repository.GetByID(ctx, cmd.ID, options...)
}

// Create implements [Service].
//
// The order is the guarantee: what a client may not choose is cleared first,
// and only then does the hook see the model. A hook that ran before the
// clearing would be handed a client-chosen key and a forged timestamp
// ([[UC-013]] guarantee 7).
func (this *DefaultService[M, ID, U]) Create(ctx context.Context, cmd CreateCommand[M]) (M, error) {
	var zero M
	m := cmd.Model
	if err := Sanitize(this.meta, &m, this.allowClientID); err != nil {
		return zero, err
	}
	if cmd.Before != nil {
		if err := cmd.Before(&m); err != nil {
			return zero, err
		}
	}
	return this.repository.Save(ctx, &m)
}

// Update implements [Service].
func (this *DefaultService[M, ID, U]) Update(ctx context.Context, cmd UpdateCommand[ID, U]) (M, error) {
	var zero M
	patch := cmd.Patch
	if cmd.Before != nil {
		if err := cmd.Before(&patch); err != nil {
			return zero, err
		}
	}
	return this.repository.Update(ctx, cmd.ID, patch)
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
func (this *DefaultService[M, ID, U]) Replace(ctx context.Context, cmd ReplaceCommand[ID, M]) (M, error) {
	var zero M
	if this.meta.PK.Auto && !this.allowClientID {
		if _, err := this.repository.GetByID(ctx, cmd.ID); err != nil {
			return zero, err
		}
	}
	m := cmd.Model
	if err := ClearWriteProtected(this.meta, &m); err != nil {
		return zero, err
	}
	if err := this.meta.SetID(&m, cmd.ID); err != nil {
		return zero, BadRequestAs(errs.CodeInvalidID, nil, "%s", err)
	}
	if cmd.Before != nil {
		if err := cmd.Before(&m); err != nil {
			return zero, err
		}
	}
	return this.repository.Save(ctx, &m)
}

// Delete implements [Service]. Removing nothing is crud.ErrNotFound: the caller
// named one row, and it was not there.
func (this *DefaultService[M, ID, U]) Delete(ctx context.Context, cmd DeleteCommand[ID]) (int64, error) {
	n, err := this.repository.Delete(ctx, cmd.ID)
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
func (this *DefaultService[M, ID, U]) DeleteMany(ctx context.Context, cmd BulkDeleteCommand[ID]) (int64, error) {
	if len(cmd.IDs) == 0 {
		return 0, nil
	}
	return this.repository.Delete(ctx, cmd.IDs...)
}

// Restorable returns the optional lifecycle application surface. Keeping it in
// a wrapper means a hard-delete DefaultService does not accidentally satisfy
// RestorableService merely because the stock repository has a fail-closed
// convenience method.
func (this *DefaultService[M, ID, U]) Restorable() (RestorableService[ID], bool) {
	if this.restorer == nil {
		return nil, false
	}
	return defaultRestorableService[ID]{repository: this.restorer}, true
}

type defaultRestorableService[ID comparable] struct {
	repository restorableRepository[ID]
}

// Restore runs the lifecycle use case for one tombstone. The application seam
// turns a zero row count into not-found just as Delete does; the repository owns
// SQL and the security decorator owns Restore authorisation.
func (this defaultRestorableService[ID]) Restore(ctx context.Context, cmd RestoreCommand[ID]) (int64, error) {
	n, err := this.repository.Restore(ctx, cmd.ID)
	if err != nil {
		return 0, err
	}
	if n == 0 {
		return 0, crud.ErrNotFound
	}
	return n, nil
}

// RestoreMany is Restore's set-oriented application use case.
func (this defaultRestorableService[ID]) RestoreMany(ctx context.Context, cmd BulkRestoreCommand[ID]) (int64, error) {
	if len(cmd.IDs) == 0 {
		return 0, nil
	}
	return this.repository.Restore(ctx, cmd.IDs...)
}

// compile turns the query document into repository options and appends the
// caller's. Appended and not merged: crud.Where ANDs, so a transport scope
// narrows the client's filter instead of replacing it ([[D-004]]).
func (this *DefaultService[M, ID, U]) compile(ctx context.Context, request *query.Request, extra []crud.Option) ([]crud.Option, error) {
	if request == nil {
		request = &query.Request{}
	}
	config, err := this.queryConfig(ctx)
	if err != nil {
		return nil, err
	}
	options, err := request.Compile(this.meta, config)
	if err != nil {
		return nil, err
	}
	return append(options, extra...), nil
}

func (this *DefaultService[M, ID, U]) queryConfig(ctx context.Context) (*query.Config, error) {
	if this.querySelector == nil {
		return this.config, nil
	}
	key := this.querySelector(ctx)
	if key == "" {
		return this.config, nil
	}
	config, ok := this.queryVariants[key]
	if !ok {
		return nil, &query.Error{Path: "queryConfig", Reason: "selector chose an undeclared query vocabulary"}
	}
	return config, nil
}
