package jobsfx

import (
	"context"
	"errors"
	"fmt"

	"go.uber.org/fx"

	"github.com/frostgrove/vv/jobs"
)

const (
	declarationGroup = `group:"vv.jobs.declarations"`
	consumerGroup    = `group:"vv.jobs.consumers"`
)

type Backend interface {
	jobs.Sender
	jobs.DeliveryDriver
}

func AsDeclaration(constructor any) any {
	return fx.Annotate(constructor, fx.As(new(jobs.Declaration)), fx.ResultTags(declarationGroup))
}

func AsConsumer(constructor any) any {
	return fx.Annotate(constructor, fx.As(new(jobs.Consumer)), fx.ResultTags(consumerGroup))
}

func AsBackend(constructor any) any {
	return fx.Annotate(constructor, fx.As(new(Backend)), fx.As(fx.Self()))
}

type Spec struct {
	Namespace jobs.Namespace
	Queue     jobs.QueueSpec
	Workers   jobs.WorkersSpec
}

func Module(spec Spec) fx.Option {
	if spec.Namespace.IsZero() {
		return fx.Error(fmt.Errorf("jobsfx: %w: namespace is required", jobs.ErrInvalid))
	}
	return fx.Module("vv.jobs",
		fx.Provide(
			newCatalog,
			func(catalog jobs.Catalog, backend Backend) (*jobs.Queue, error) {
				configured := spec.Queue
				configured.Namespace = spec.Namespace
				configured.Catalog = catalog
				configured.Sender = backend
				return jobs.NewQueue(configured)
			},
			func(catalog jobs.Catalog, backend Backend, registered contributions) (*jobs.Workers, error) {
				configured := spec.Workers
				configured.Namespace = spec.Namespace
				configured.Catalog = catalog
				configured.Driver = backend
				return jobs.NewWorkers(configured, registered.Consumers...)
			},
		),
		fx.Invoke(bindLifecycle),
	)
}

type contributions struct {
	fx.In

	Declarations []jobs.Declaration `group:"vv.jobs.declarations"`
	Consumers    []jobs.Consumer    `group:"vv.jobs.consumers"`
}

func newCatalog(registered contributions) (jobs.Catalog, error) {
	declarations := make([]jobs.Declaration, 0, len(registered.Declarations)+len(registered.Consumers))
	declarations = append(declarations, registered.Declarations...)
	for _, consumer := range registered.Consumers {
		if consumer == nil {
			declarations = append(declarations, nil)
			continue
		}
		declarations = append(declarations, consumer.Declaration())
	}
	catalog, err := jobs.NewCatalog(declarations...)
	if err != nil {
		return jobs.Catalog{}, fmt.Errorf("jobsfx: catalog: %w", err)
	}
	return catalog, nil
}

type lifecycleRuntime struct {
	queue      *jobs.Queue
	workers    *jobs.Workers
	shutdowner fx.Shutdowner
	activation *jobs.QueueActivation
}

func bindLifecycle(lifecycle fx.Lifecycle, shutdowner fx.Shutdowner, queue *jobs.Queue, workers *jobs.Workers) {
	runtime := &lifecycleRuntime{queue: queue, workers: workers, shutdowner: shutdowner}
	lifecycle.Append(fx.Hook{OnStart: runtime.start, OnStop: runtime.stop})
}

func (runtime *lifecycleRuntime) start(ctx context.Context) error {
	activation, err := runtime.queue.Activate()
	if err != nil {
		return fmt.Errorf("jobsfx: activate queue: %w", err)
	}
	runtime.activation = activation
	go runtime.run(context.WithoutCancel(ctx))
	return nil
}

func (runtime *lifecycleRuntime) run(ctx context.Context) {
	if err := runtime.workers.Run(ctx); err != nil {
		_ = runtime.shutdowner.Shutdown(fx.ExitCode(1))
	}
}

func (runtime *lifecycleRuntime) stop(ctx context.Context) error {
	drainErr := runtime.workers.Drain(ctx)
	var activationErr error
	if runtime.activation != nil {
		activationErr = runtime.activation.Close()
	}
	return errors.Join(drainErr, activationErr)
}
