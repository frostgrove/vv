package jobsfx

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"go.uber.org/fx"

	"github.com/frostgrove/vv/jobs"
	"github.com/frostgrove/vv/runtime"
	"github.com/frostgrove/vv/runtime/runtimefx"
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

type backendPreparer interface {
	Prepare(context.Context) error
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

// Activation is the deployment role said out loud. A container that happens to
// hold a consumer is not a worker replica, and a container that happens to hold
// a schedule is not the process that owns the clock.
type Activation string

const (
	Enabled  Activation = "enabled"
	Disabled Activation = "disabled"
)

func (this Activation) check(field string) error {
	switch this {
	case "", Enabled, Disabled:
		return nil
	}
	return fmt.Errorf("jobsfx: %w: Spec.%s is %q, which is neither jobsfx.Enabled nor jobsfx.Disabled",
		jobs.ErrInvalid, field, string(this))
}

type Spec struct {
	Namespace jobs.Namespace
	Catalog   jobs.Catalog
	Queue     jobs.QueueSpec
	Workers   jobs.WorkersSpec
	Scheduler jobs.SchedulerSpec

	Consuming Activation

	Scheduling Activation
}

// Module wires the queue every deployment needs, and the roles the spec names.
// A role the spec leaves unstated is not wired: nothing here starts a goroutine
// of its own, and what Consuming and Scheduling activate is a contribution to
// the runner group a supervisor owns ([[D-092]]).
func Module(spec Spec) fx.Option {
	if spec.Namespace.IsZero() {
		return fx.Error(fmt.Errorf("jobsfx: %w: namespace is required", jobs.ErrInvalid))
	}
	if err := errors.Join(spec.Consuming.check("Consuming"), spec.Scheduling.check("Scheduling")); err != nil {
		return fx.Error(err)
	}

	options := []fx.Option{
		fx.Provide(
			func(registered declarationContributions) (jobs.Catalog, error) {
				return resolveCatalog(spec.Catalog, registered)
			},
			func(catalog jobs.Catalog, backend Backend) (*jobs.Queue, error) {
				configured := spec.Queue
				configured.Namespace = spec.Namespace
				configured.Catalog = catalog
				configured.Sender = backend
				return jobs.NewQueue(configured)
			},
			func(catalog jobs.Catalog, backend Backend, queue *jobs.Queue, consumers consumerContributions, schedules scheduleContributions) (deployment, error) {
				return spec.deployment(catalog, backend, queue, consumers, schedules)
			},
			func(built deployment) *jobs.Workers { return built.workers },
			func(built deployment) *jobs.Scheduler { return built.scheduler },
			func(backend Backend, queue *jobs.Queue, built deployment) *queueLifecycle {
				return newQueueLifecycle(backend, queue, built.supervised())
			},
		),
	}
	if spec.Consuming == Enabled {
		options = append(options, fx.Provide(runtimefx.AsRunner(supervisedWorkers)))
	}
	if spec.Scheduling == Enabled {
		options = append(options, fx.Provide(runtimefx.AsRunner(supervisedScheduler)))
	}
	if spec.Consuming == Enabled || spec.Scheduling == Enabled {
		options = append(options, fx.Invoke(bindSupervisedQueue))
	} else {
		options = append(options, fx.Invoke(bindQueue))
	}
	return fx.Module("vv.jobs", options...)
}

// deployment is the role decision made once: what the spec named, checked
// against what the container actually holds, so that a graph carrying a
// consumer nobody activated is refused rather than quietly producing only.
type deployment struct {
	workers   *jobs.Workers
	scheduler *jobs.Scheduler
}

func (this deployment) supervised() []string {
	var names []string
	if this.workers != nil {
		names = append(names, WorkersRunnerName)
	}
	if this.scheduler != nil {
		names = append(names, SchedulerRunnerName)
	}
	return names
}

func (this Spec) deployment(catalog jobs.Catalog, backend Backend, queue *jobs.Queue, consumers consumerContributions, schedules scheduleContributions) (deployment, error) {
	workers, workersErr := this.workers(catalog, backend, consumers)
	scheduler, schedulerErr := this.scheduler(queue, schedules)
	if err := errors.Join(workersErr, schedulerErr); err != nil {
		return deployment{}, err
	}
	return deployment{workers: workers, scheduler: scheduler}, nil
}

func (this Spec) workers(catalog jobs.Catalog, backend Backend, registered consumerContributions) (*jobs.Workers, error) {
	consumers := resolveConsumers(catalog, registered.Consumers)
	if this.Consuming != Enabled {
		if this.Consuming == Disabled || len(consumers) == 0 {
			return nil, nil
		}
		return nil, fmt.Errorf(
			"jobsfx: %w: %d job consumers are wired and Spec.Consuming names neither jobsfx.Enabled nor jobsfx.Disabled; a deployment role is declared, not read off the graph",
			jobs.ErrInvalid, len(consumers))
	}
	if len(consumers) == 0 {
		return nil, fmt.Errorf("jobsfx: %w: Spec.Consuming is jobsfx.Enabled and no job consumer was contributed", jobs.ErrInvalid)
	}
	configured := this.Workers
	configured.Namespace = this.Namespace
	configured.Catalog = catalog
	configured.Driver = backend
	return jobs.NewWorkers(configured, consumers...)
}

func (this Spec) scheduler(queue *jobs.Queue, registered scheduleContributions) (*jobs.Scheduler, error) {
	if this.Scheduling != Enabled {
		if this.Scheduling == Disabled || len(registered.Schedules) == 0 {
			return nil, nil
		}
		return nil, fmt.Errorf(
			"jobsfx: %w: %d schedules are wired and Spec.Scheduling names neither jobsfx.Enabled nor jobsfx.Disabled; a deployment role is declared, not read off the graph",
			jobs.ErrInvalid, len(registered.Schedules))
	}
	if len(registered.Schedules) == 0 {
		return nil, fmt.Errorf("jobsfx: %w: Spec.Scheduling is jobsfx.Enabled and no schedule was contributed", jobs.ErrInvalid)
	}
	configured := this.Scheduler
	configured.Queue = queue
	return jobs.NewScheduler(configured, registered.Schedules...)
}

type declarationContributions struct {
	fx.In

	Declarations []jobs.Declaration `group:"vv.jobs.declarations"`
}

type consumerContributions struct {
	fx.In

	Consumers []jobs.Consumer `group:"vv.jobs.consumers"`
}

type scheduleContributions struct {
	fx.In

	Schedules []jobs.Schedule `group:"vv.jobs.schedules"`
}

func newCatalog(registered declarationContributions) (jobs.Catalog, error) {
	declarations := contributionDeclarations(registered)
	if len(declarations) == 0 {
		return jobs.Catalog{}, fmt.Errorf("jobsfx: %w: catalog is empty; use jobsfx.Registry, configure Spec.Catalog, or contribute jobsfx.AsDeclaration", jobs.ErrInvalid)
	}
	catalog, err := jobs.NewCatalog(declarations...)
	if err != nil {
		return jobs.Catalog{}, fmt.Errorf("jobsfx: catalog: %w", err)
	}
	return catalog, nil
}

func resolveCatalog(configured jobs.Catalog, registered declarationContributions) (jobs.Catalog, error) {
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

func contributionDeclarations(registered declarationContributions) []jobs.Declaration {
	declarations := make([]jobs.Declaration, 0, len(registered.Declarations))
	seen := make(map[jobs.Declaration]struct{}, len(registered.Declarations))
	for _, declaration := range registered.Declarations {
		if _, exists := seen[declaration]; exists {
			continue
		}
		seen[declaration] = struct{}{}
		declarations = append(declarations, declaration)
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

type queueLifecycle struct {
	backend    Backend
	queue      *jobs.Queue
	supervised []string
	supervisor *runtime.Supervisor
	activation *jobs.QueueActivation
	activated  chan struct{}
}

func newQueueLifecycle(backend Backend, queue *jobs.Queue, supervised []string) *queueLifecycle {
	return &queueLifecycle{
		backend:    backend,
		queue:      queue,
		supervised: supervised,
		activated:  make(chan struct{}),
	}
}

func bindQueue(lifecycle fx.Lifecycle, queue *queueLifecycle) {
	lifecycle.Append(fx.Hook{OnStart: queue.start, OnStop: queue.stop})
}

func bindSupervisedQueue(lifecycle fx.Lifecycle, queue *queueLifecycle, supervisor *runtime.Supervisor) {
	queue.supervisor = supervisor
	bindQueue(lifecycle, queue)
}

func (this *queueLifecycle) start(ctx context.Context) error {
	if err := this.verifySupervision(); err != nil {
		return err
	}
	if preparer, ok := this.backend.(backendPreparer); ok {
		if err := prepareBackend(ctx, preparer); err != nil {
			return fmt.Errorf("jobsfx: prepare backend: %w", err)
		}
	}
	activation, err := this.queue.Activate()
	if err != nil {
		return fmt.Errorf("jobsfx: activate queue: %w", err)
	}
	this.activation = activation
	close(this.activated)
	return nil
}

func (this *queueLifecycle) verifySupervision() error {
	if len(this.supervised) == 0 {
		return nil
	}
	held := make(map[string]struct{})
	if this.supervisor != nil {
		for _, state := range this.supervisor.States() {
			held[state.Name] = struct{}{}
		}
	}
	var missing []string
	for _, name := range this.supervised {
		if _, ok := held[name]; !ok {
			missing = append(missing, name)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf(
		"jobsfx: %w: %s never reached a supervisor; the graph provides a *runtime.Supervisor that was not built from the runner group",
		jobs.ErrInvalid, strings.Join(missing, " and "))
}

func prepareBackend(ctx context.Context, preparer backendPreparer) (err error) {
	defer func() {
		if recover() != nil {
			err = jobs.ErrDriver
		}
	}()
	return preparer.Prepare(ctx)
}

// stop asks the supervisor to stop before it closes the activation, because the
// activation is what jobs.Go resolves to: closing it under a worker that is
// still draining turns that worker's follow-up step into ErrNotActivated. fx
// runs stop hooks in reverse order of registration, so the supervisor has
// usually stopped already and this call is a no-op; asking makes the order hold
// either way.
func (this *queueLifecycle) stop(ctx context.Context) error {
	var problems []error
	if this.supervisor != nil {
		problems = append(problems, this.supervisor.Stop(ctx))
	}
	if this.activation != nil {
		problems = append(problems, this.activation.Close())
	}
	return errors.Join(problems...)
}
