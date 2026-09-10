package jobsfx

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/frostgrove/vv/jobs"
	"github.com/frostgrove/vv/runtime"
)

const (
	WorkersRunnerName   = "vv.jobs.workers"
	SchedulerRunnerName = "vv.jobs.scheduler"
)

// WorkersRunner is the explicit form under Module: a consumer who assembles the
// graph by hand hands the result to any supervisor. ready is the seam that
// keeps the pool off the backend until the queue has been activated; a nil
// channel starts the pool at once.
func WorkersRunner(workers *jobs.Workers, ready <-chan struct{}) (runtime.Runner, error) {
	runner, err := newWorkersRunner(workers, ready)
	if err != nil {
		return nil, err
	}
	return runner, nil
}

func SchedulerRunner(scheduler *jobs.Scheduler, ready <-chan struct{}) (runtime.Runner, error) {
	runner, err := newSchedulerRunner(scheduler, ready)
	if err != nil {
		return nil, err
	}
	return runner, nil
}

func newWorkersRunner(workers *jobs.Workers, ready <-chan struct{}) (*workersRunner, error) {
	if workers == nil {
		return nil, fmt.Errorf("jobsfx: %w: a workers runner without workers", jobs.ErrInvalid)
	}
	return &workersRunner{workers: workers, ready: ready}, nil
}

func newSchedulerRunner(scheduler *jobs.Scheduler, ready <-chan struct{}) (*schedulerRunner, error) {
	if scheduler == nil {
		return nil, fmt.Errorf("jobsfx: %w: a scheduler runner without a scheduler", jobs.ErrInvalid)
	}
	return &schedulerRunner{scheduler: scheduler, ready: ready}, nil
}

func supervisedWorkers(workers *jobs.Workers, queue *queueLifecycle) (*workersRunner, error) {
	return newWorkersRunner(workers, queue.activated)
}

func supervisedScheduler(scheduler *jobs.Scheduler, queue *queueLifecycle) (*schedulerRunner, error) {
	return newSchedulerRunner(scheduler, queue.activated)
}

type workersRunner struct {
	workers    *jobs.Workers
	ready      <-chan struct{}
	mu         sync.Mutex
	generation *workersRunnerGeneration
}

type workersRunnerGeneration struct {
	drain     chan struct{}
	drainOnce sync.Once
	entered   bool
	started   bool
	draining  bool
	completed bool
	ready     bool
}

func (this *workersRunner) Name() string { return WorkersRunnerName }

func (this *workersRunner) Declaration() runtime.Declaration {
	return runtime.Declaration{Placement: runtime.PerReplica, Durability: runtime.Durable}
}

func (this *workersRunner) BeginRunnerGeneration() {
	this.mu.Lock()
	this.generation = newWorkersRunnerGeneration()
	this.mu.Unlock()
}

func (this *workersRunner) Run(ctx context.Context) error {
	generation, ok := this.enterGeneration()
	if !ok {
		return jobs.ErrConflict
	}
	defer this.completeGeneration(generation)
	if err := awaitReadyOrDrain(ctx, this.ready, generation.drain); err != nil {
		return err
	}
	this.mu.Lock()
	if generation.draining {
		this.mu.Unlock()
		return nil
	}
	generation.started = true
	this.mu.Unlock()
	err := this.workers.Run(ctx)
	for attempt := 0; attempt < 2 && errors.Is(err, jobs.ErrConflict); attempt++ {
		this.mu.Lock()
		draining := generation.draining
		this.mu.Unlock()
		if draining {
			return nil
		}
		if attempt == 0 {
			err = this.workers.Run(ctx)
		}
	}
	return err
}

func (this *workersRunner) Drain(ctx context.Context) error {
	this.mu.Lock()
	generation := this.generation
	if generation == nil {
		generation = newWorkersRunnerGeneration()
		this.generation = generation
	}
	generation.draining = true
	generation.drainOnce.Do(func() { close(generation.drain) })
	started := generation.started
	this.mu.Unlock()
	if !started {
		return nil
	}
	return this.workers.Drain(ctx)
}

func (this *workersRunner) Ready(ctx context.Context) error {
	this.mu.Lock()
	generation := this.generation
	started := generation != nil && generation.started
	this.mu.Unlock()
	if !started {
		return jobs.ErrNotActivated
	}
	err := this.workers.Check(ctx)
	this.mu.Lock()
	if this.generation == generation && err == nil {
		generation.ready = true
	}
	ready := generation.ready || generation.completed
	this.mu.Unlock()
	if !ready {
		return jobs.ErrNotActivated
	}
	return err
}

func newWorkersRunnerGeneration() *workersRunnerGeneration {
	return &workersRunnerGeneration{drain: make(chan struct{})}
}

func (this *workersRunner) enterGeneration() (*workersRunnerGeneration, bool) {
	this.mu.Lock()
	defer this.mu.Unlock()
	generation := this.generation
	if generation == nil || generation.completed {
		generation = newWorkersRunnerGeneration()
		this.generation = generation
	}
	if generation.entered {
		return nil, false
	}
	generation.entered = true
	return generation, true
}

func (this *workersRunner) completeGeneration(generation *workersRunnerGeneration) {
	this.mu.Lock()
	if this.generation == generation {
		generation.completed = true
	}
	this.mu.Unlock()
}

type schedulerRunner struct {
	scheduler *jobs.Scheduler
	ready     <-chan struct{}
}

func (this *schedulerRunner) Name() string { return SchedulerRunnerName }

func (this *schedulerRunner) Declaration() runtime.Declaration {
	return runtime.Declaration{Placement: runtime.Singleton, Durability: runtime.Durable}
}

func (this *schedulerRunner) Run(ctx context.Context) error {
	if err := awaitReady(ctx, this.ready); err != nil {
		return err
	}
	return this.scheduler.Run(ctx)
}

func awaitReady(ctx context.Context, ready <-chan struct{}) error {
	if ready == nil {
		return nil
	}
	select {
	case <-ready:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func awaitReadyOrDrain(ctx context.Context, ready <-chan struct{}, drain <-chan struct{}) error {
	if ready == nil {
		select {
		case <-drain:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		default:
			return nil
		}
	}
	select {
	case <-ready:
		return nil
	case <-drain:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
