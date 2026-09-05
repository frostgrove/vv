package port

import (
	"context"
	"reflect"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/query"
	"github.com/frostgrove/vv/errs"
)

type Service[M any, ID comparable, U any] interface {
	Meta() *crud.Meta

	List(ctx context.Context, cmd ListCommand) (crud.PaginatedResponse[M], error)
	Count(ctx context.Context, cmd CountCommand) (int64, error)
	Get(ctx context.Context, cmd GetCommand[ID]) (M, error)

	Create(ctx context.Context, cmd CreateCommand[M]) (M, error)
	Update(ctx context.Context, cmd UpdateCommand[ID, U]) (M, error)
	Replace(ctx context.Context, cmd ReplaceCommand[ID, M]) (M, error)
	Delete(ctx context.Context, cmd DeleteCommand[ID]) (int64, error)
	DeleteMany(ctx context.Context, cmd BulkDeleteCommand[ID]) (int64, error)

	Paths() errs.Resolver
}

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

type ServiceMiddleware[M any, ID comparable, U any] func(Service[M, ID, U]) Service[M, ID, U]

func ChainService[M any, ID comparable, U any](base Service[M, ID, U], middleware ...ServiceMiddleware[M, ID, U]) Service[M, ID, U] {
	if nilService(base) {
		return nil
	}
	current := base
	for i := len(middleware) - 1; i >= 0; i-- {
		if middleware[i] == nil {
			continue
		}
		current = middleware[i](current)
		if nilService(current) {
			return nil
		}
	}
	return current
}

func nilService(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}

type ServiceOption func(*serviceConfig)

type serviceConfig struct {
	query         *query.Config
	queryVariants map[string]*query.Config
	querySelector QuerySelector
	paths         errs.Resolver
	allowClientID bool
}

func WithQuery(config *query.Config) ServiceOption {
	return func(c *serviceConfig) {
		c.query, c.queryVariants, c.querySelector = config, nil, nil
	}
}

type QuerySelector func(context.Context) string

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

func AllowClientID() ServiceOption {
	return func(c *serviceConfig) { c.allowClientID = true }
}

func WithPaths(r errs.Resolver) ServiceOption {
	return func(c *serviceConfig) { c.paths = r }
}

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

func NewService[M any, ID comparable, U any](repository Repository[M, ID, U], options ...ServiceOption) *DefaultService[M, ID, U] {
	var c serviceConfig
	for _, o := range options {
		if o != nil {
			o(&c)
		}
	}

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

func (this *DefaultService[M, ID, U]) Meta() *crud.Meta { return this.meta }

func (this *DefaultService[M, ID, U]) Paths() errs.Resolver { return this.paths }

func (this *DefaultService[M, ID, U]) List(ctx context.Context, cmd ListCommand) (crud.PaginatedResponse[M], error) {
	options, err := this.compile(ctx, cmd.Query, cmd.Options)
	if err != nil {
		return crud.PaginatedResponse[M]{}, err
	}
	return this.repository.Get(ctx, options...)
}

func (this *DefaultService[M, ID, U]) Count(ctx context.Context, cmd CountCommand) (int64, error) {
	request := requestCopy(cmd.Query)
	NarrowForCount(request)

	options, err := this.compile(ctx, request, cmd.Options)
	if err != nil {
		return 0, err
	}
	return this.repository.Count(ctx, options...)
}

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

// DeleteMany hands an empty list to the repository rather than answering it here.
// The security gate decorates the repository, so a shortcut in this method is a
// shortcut past the gate: bulk-deleting nothing used to answer 200 to a caller
// with no principal at all. Deleting no ids is a no-op that costs no statement,
// which is what makes passing it down free.
func (this *DefaultService[M, ID, U]) DeleteMany(ctx context.Context, cmd BulkDeleteCommand[ID]) (int64, error) {
	return this.repository.Delete(ctx, cmd.IDs...)
}

func (this *DefaultService[M, ID, U]) Restorable() (RestorableService[ID], bool) {
	if this.restorer == nil {
		return nil, false
	}
	return defaultRestorableService[ID]{repository: this.restorer}, true
}

type defaultRestorableService[ID comparable] struct {
	repository restorableRepository[ID]
}

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

func (this defaultRestorableService[ID]) RestoreMany(ctx context.Context, cmd BulkRestoreCommand[ID]) (int64, error) {
	if len(cmd.IDs) == 0 {
		return 0, nil
	}
	return this.repository.Restore(ctx, cmd.IDs...)
}

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
