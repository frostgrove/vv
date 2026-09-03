package jobsfx

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"

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
	workers *jobs.Workers
	ready   <-chan struct{}
	drained atomic.Bool
}

func (this *workersRunner) Name() string { return WorkersRunnerName }

func (this *workersRunner) Declaration() runtime.Declaration {
	return runtime.Declaration{Placement: runtime.PerReplica, Durability: runtime.Durable}
}

// Run tells one shape of ErrConflict from the rest: a supervisor that drains
// before the pool reached jobs.Workers.Run has stopped a pool that never
// started, and reporting that as a runner that died on its own would fail every
// shutdown that arrived quickly enough.
func (this *workersRunner) Run(ctx context.Context) error {
	if err := awaitReady(ctx, this.ready); err != nil {
		return err
	}
	err := this.workers.Run(ctx)
	if err != nil && this.drained.Load() && errors.Is(err, jobs.ErrConflict) {
		return nil
	}
	return err
}

func (this *workersRunner) Drain(ctx context.Context) error {
	this.drained.Store(true)
	return this.workers.Drain(ctx)
}

func (this *workersRunner) Ready(ctx context.Context) error { return this.workers.Check(ctx) }

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
