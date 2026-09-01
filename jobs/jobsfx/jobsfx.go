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
	scheduleGroup    = `group:"vv.jobs.schedules"`
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

func AsSchedule(constructor any) any {
	return fx.Annotate(constructor, fx.As(new(jobs.Schedule)), fx.ResultTags(scheduleGroup))
}

func AsBackend(constructor any) any {
	return fx.Annotate(constructor, fx.As(new(Backend)), fx.As(fx.Self()))
}

type Spec struct {
	Namespace jobs.Namespace
	Catalog   jobs.Catalog
	Queue     jobs.QueueSpec
	Workers   jobs.WorkersSpec
	Scheduler jobs.SchedulerSpec
}

func Module(spec Spec) fx.Option {
	if spec.Namespace.IsZero() {
		return fx.Error(fmt.Errorf("jobsfx: %w: namespace is required", jobs.ErrInvalid))
	}
	return fx.Module("vv.jobs",
		fx.Provide(
			func(registered contributions) (jobs.Catalog, error) {
				return resolveCatalog(spec.Catalog, registered)
			},
			func(catalog jobs.Catalog, backend Backend) (*jobs.Queue, error) {
				configured := spec.Queue
				configured.Namespace = spec.Namespace
				configured.Catalog = catalog
				configured.Sender = backend
				return jobs.NewQueue(configured)
			},
			func(catalog jobs.Catalog, backend Backend, registered contributions) (*jobs.Workers, error) {
				consumers := resolveConsumers(catalog, registered.Consumers)
				if len(consumers) == 0 {
					return nil, nil
				}
				configured := spec.Workers
				configured.Namespace = spec.Namespace
				configured.Catalog = catalog
				configured.Driver = backend
				return jobs.NewWorkers(configured, consumers...)
			},
			func(queue *jobs.Queue, registered contributions) (scheduledRuntime, error) {
				if len(registered.Schedules) == 0 {
					return scheduledRuntime{}, nil
				}
				configured := spec.Scheduler
				configured.Queue = queue
				scheduler, err := jobs.NewScheduler(configured, registered.Schedules...)
				if err != nil {
					return scheduledRuntime{}, err
				}
				return scheduledRuntime{scheduler: scheduler}, nil
			},
		),
		fx.Invoke(bindLifecycle),
	)
}

type contributions struct {
	fx.In

	Declarations []jobs.Declaration `group:"vv.jobs.declarations"`
	Consumers    []jobs.Consumer    `group:"vv.jobs.consumers"`
	Schedules    []jobs.Schedule    `group:"vv.jobs.schedules"`
}

func newCatalog(registered contributions) (jobs.Catalog, error) {
	declarations := contributionDeclarations(registered)
	catalog, err := jobs.NewCatalog(declarations...)
	if err != nil {
		return jobs.Catalog{}, fmt.Errorf("jobsfx: catalog: %w", err)
	}
	return catalog, nil
}

func resolveCatalog(configured jobs.Catalog, registered contributions) (jobs.Catalog, error) {
	if configured.Fingerprint() == "" {
		return newCatalog(registered)
	}
	contributed, err := jobs.NewCatalog(contributionDeclarations(registered)...)
	if err != nil {
		return jobs.Catalog{}, fmt.Errorf("jobsfx: contributions: %w", err)
	}
	for _, declaration := range contributed.Definitions() {
		member, ok := configured.Lookup(declaration.Describe().Name)
		if !ok || member != declaration {
			return jobs.Catalog{}, fmt.Errorf("jobsfx: %w: declaration %q is not a member of the configured catalog", jobs.ErrInvalid, declaration.Describe().Name)
		}
	}
	return configured, nil
}

func contributionDeclarations(registered contributions) []jobs.Declaration {
	declarations := make([]jobs.Declaration, 0, len(registered.Declarations)+len(registered.Consumers))
	declarations = append(declarations, registered.Declarations...)
	for _, consumer := range registered.Consumers {
		if consumer == nil {
			declarations = append(declarations, nil)
			continue
		}
		declarations = append(declarations, consumer.Declaration())
	}
	return declarations
}

func resolveConsumers(catalog jobs.Catalog, explicit []jobs.Consumer) []jobs.Consumer {
	consumers := append([]jobs.Consumer(nil), explicit...)
	explicitDeclarations := make(map[jobs.Declaration]struct{}, len(explicit))
	for _, consumer := range explicit {
		if consumer != nil {
			explicitDeclarations[consumer.Declaration()] = struct{}{}
		}
	}
	for _, consumer := range catalog.AutomaticConsumers() {
		declaration := consumer.Declaration()
		if _, exists := explicitDeclarations[declaration]; exists {
			continue
		}
		consumers = append(consumers, consumer)
	}
	return consumers
}

type scheduledRuntime struct {
	scheduler *jobs.Scheduler
}

type schedulerRunResult struct {
	err       error
	cancelled bool
}

type lifecycleRuntime struct {
	queue           *jobs.Queue
	workers         *jobs.Workers
	scheduler       *jobs.Scheduler
	shutdowner      fx.Shutdowner
	activation      *jobs.QueueActivation
	schedulerCancel context.CancelFunc
	schedulerDone   chan schedulerRunResult
}

func bindLifecycle(lifecycle fx.Lifecycle, shutdowner fx.Shutdowner, queue *jobs.Queue, workers *jobs.Workers, scheduled scheduledRuntime) {
	runtime := &lifecycleRuntime{queue: queue, workers: workers, scheduler: scheduled.scheduler, shutdowner: shutdowner}
	lifecycle.Append(fx.Hook{OnStart: runtime.start, OnStop: runtime.stop})
}

func (runtime *lifecycleRuntime) start(ctx context.Context) error {
	activation, err := runtime.queue.Activate()
	if err != nil {
		return fmt.Errorf("jobsfx: activate queue: %w", err)
	}
	runtime.activation = activation
	if runtime.workers != nil {
		go runtime.runWorkers(context.WithoutCancel(ctx))
	}
	if runtime.scheduler != nil {
		schedulerContext, cancel := context.WithCancel(context.WithoutCancel(ctx))
		runtime.schedulerCancel = cancel
		runtime.schedulerDone = make(chan schedulerRunResult, 1)
		go runtime.runScheduler(schedulerContext)
	}
	return nil
}

func (runtime *lifecycleRuntime) runWorkers(ctx context.Context) {
	if err := runtime.workers.Run(ctx); err != nil {
		_ = runtime.shutdowner.Shutdown(fx.ExitCode(1))
	}
}

func (runtime *lifecycleRuntime) runScheduler(ctx context.Context) {
	err := runtime.scheduler.Run(ctx)
	result := schedulerRunResult{err: err, cancelled: ctx.Err() != nil}
	runtime.schedulerDone <- result
	close(runtime.schedulerDone)
	if err != nil && !result.cancelled {
		_ = runtime.shutdowner.Shutdown(fx.ExitCode(1))
	}
}

func (runtime *lifecycleRuntime) stop(ctx context.Context) error {
	if runtime.schedulerCancel != nil {
		runtime.schedulerCancel()
	}
	var drainErr error
	if runtime.workers != nil {
		drainErr = runtime.workers.Drain(ctx)
	}
	schedulerErr := runtime.waitScheduler(ctx)
	var activationErr error
	if runtime.activation != nil {
		activationErr = runtime.activation.Close()
	}
	return errors.Join(drainErr, schedulerErr, activationErr)
}

func (runtime *lifecycleRuntime) waitScheduler(ctx context.Context) error {
	if runtime.schedulerDone == nil {
		return nil
	}
	select {
	case result := <-runtime.schedulerDone:
		if result.cancelled && errors.Is(result.err, context.Canceled) {
			return nil
		}
		return result.err
	case <-ctx.Done():
		return ctx.Err()
	}
}
