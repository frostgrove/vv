package jobspgfx

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.uber.org/fx"

	"github.com/frostgrove/vv/jobs"
	"github.com/frostgrove/vv/jobs/jobspg"
	"github.com/frostgrove/vv/runtime"
)

const DefaultHousekeepingInterval = time.Minute
const DefaultHousekeepingSweepTimeout = 30 * time.Second
const DefaultHousekeepingMaxBatches = 64
const MaxHousekeepingBatches = 1024

const RetentionRunnerName = "vv.jobspg.retention"

type housekeepingConfig struct {
	disabled     bool
	interval     time.Duration
	sweepTimeout time.Duration
	batchSize    int
	maxBatches   int
}

func normalizeHousekeeping(settings HousekeepingSettings) (housekeepingConfig, error) {
	if settings.Disabled {
		return housekeepingConfig{disabled: true}, nil
	}
	interval := settings.Interval
	if interval == 0 {
		interval = DefaultHousekeepingInterval
	}
	sweepTimeout := settings.SweepTimeout
	if sweepTimeout == 0 {
		sweepTimeout = DefaultHousekeepingSweepTimeout
	}
	batchSize := settings.BatchSize
	if batchSize == 0 {
		batchSize = jobs.DefaultPurgeLimit
	}
	maxBatches := settings.MaxBatches
	if maxBatches == 0 {
		maxBatches = DefaultHousekeepingMaxBatches
	}
	if interval < jobs.MinimumPollInterval || interval > jobs.MaxRetention || sweepTimeout < jobs.MinimumPollInterval || sweepTimeout > jobs.MaxRetention || batchSize < 0 {
		return housekeepingConfig{}, fmt.Errorf("jobspgfx: %w: housekeeping settings", jobs.ErrInvalid)
	}
	if batchSize > jobs.MaxPurgeLimit {
		return housekeepingConfig{}, fmt.Errorf("jobspgfx: %w: housekeeping batch", jobs.ErrTooLarge)
	}
	if maxBatches < 0 {
		return housekeepingConfig{}, fmt.Errorf("jobspgfx: %w: housekeeping batches", jobs.ErrInvalid)
	}
	if maxBatches > MaxHousekeepingBatches {
		return housekeepingConfig{}, fmt.Errorf("jobspgfx: %w: housekeeping batches", jobs.ErrTooLarge)
	}
	return housekeepingConfig{interval: interval, sweepTimeout: sweepTimeout, batchSize: batchSize, maxBatches: maxBatches}, nil
}

// RetentionRunner is the explicit form under Module: a graph assembled by hand
// hands the result to any supervisor.
func RetentionRunner(sweeper jobs.RetentionSweeper, settings HousekeepingSettings) (runtime.Runner, error) {
	config, err := normalizeHousekeeping(settings)
	if err != nil {
		return nil, err
	}
	return newRetentionRunner(sweeper, config)
}

func newRetentionRunner(sweeper jobs.RetentionSweeper, config housekeepingConfig) (*retentionRunner, error) {
	if config.disabled {
		return nil, fmt.Errorf("jobspgfx: %w: housekeeping is disabled and has no runner", jobs.ErrInvalid)
	}
	if sweeper == nil {
		return nil, fmt.Errorf("jobspgfx: %w: a retention runner without a sweeper", jobs.ErrInvalid)
	}
	return &retentionRunner{sweeper: sweeper, config: config}, nil
}

type retentionRunner struct {
	sweeper jobs.RetentionSweeper
	config  housekeepingConfig
}

func (this *retentionRunner) Name() string { return RetentionRunnerName }

func (this *retentionRunner) Declaration() runtime.Declaration { return runtime.PerReplicaTimer }

// Run measures the interval from the end of a drain rather than from a tick, so
// a sweep that outlasts its own period does not find the next one already
// queued and start again the instant it returns.
func (this *retentionRunner) Run(ctx context.Context) error {
	timer := time.NewTimer(this.config.interval)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			if err := this.sweep(ctx); err != nil {
				return err
			}
			timer.Reset(this.config.interval)
		}
	}
}

// sweep separates the two failures that must not stop the schedule — a pass
// that ran out of its own budget, and a backend that is not migrated yet — from
// the one that must, which the supervisor turns into a shutdown.
func (this *retentionRunner) sweep(ctx context.Context) error {
	cycle, cancel := context.WithTimeout(ctx, this.config.sweepTimeout)
	defer cancel()

	err := drainRetention(cycle, this.sweeper, this.config.batchSize, this.config.maxBatches)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil:
		return nil
	case errors.Is(err, jobspg.ErrNotReady):
		return nil
	}
	return err
}

func drainRetention(ctx context.Context, sweeper jobs.RetentionSweeper, batchSize, maxBatches int) error {
	for batch := 0; batch < maxBatches; batch++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		count, err := sweepRetention(ctx, sweeper, batchSize)
		if err != nil {
			return err
		}
		if count < 0 || count > batchSize {
			return jobs.ErrDriver
		}
		if count < batchSize {
			return nil
		}
	}
	return nil
}

func sweepRetention(ctx context.Context, sweeper jobs.RetentionSweeper, limit int) (count int, err error) {
	defer func() {
		if recover() != nil {
			count = 0
			err = jobs.ErrDriver
		}
	}()
	return sweeper.SweepTerminalRetention(ctx, limit)
}

// supervision is optional in the graph and mandatory at start: a container that
// holds the runner and no supervisor built from the runner group is a container
// that would sweep nothing and say nothing, which is the failure the runner
// contract exists to remove.
type supervision struct {
	fx.In

	Supervisor *runtime.Supervisor `optional:"true"`
}

func bindRetentionSupervision(lifecycle fx.Lifecycle, injected supervision) {
	guard := &retentionSupervision{supervisor: injected.Supervisor}
	lifecycle.Append(fx.Hook{OnStart: guard.start, OnStop: guard.stop})
}

type retentionSupervision struct {
	supervisor *runtime.Supervisor
}

func (this *retentionSupervision) start(context.Context) error {
	if _, known := this.state(); known {
		return nil
	}
	if this.supervisor == nil {
		return fmt.Errorf(
			"jobspgfx: %w: %s has no supervisor; add runtimefx.Supervising to the graph, or switch housekeeping off",
			jobs.ErrInvalid, RetentionRunnerName)
	}
	return fmt.Errorf(
		"jobspgfx: %w: %s never reached the supervisor; the graph provides a *runtime.Supervisor that was not built from the runner group",
		jobs.ErrInvalid, RetentionRunnerName)
}

func (this *retentionSupervision) stop(context.Context) error {
	state, known := this.state()
	if !known || state.Phase != runtime.PhaseFailed {
		return nil
	}
	return fmt.Errorf("jobspgfx: retention housekeeping stopped: %w", state.Err)
}

func (this *retentionSupervision) state() (runtime.RunnerState, bool) {
	if this.supervisor == nil {
		return runtime.RunnerState{}, false
	}
	for _, state := range this.supervisor.States() {
		if state.Name == RetentionRunnerName {
			return state, true
		}
	}
	return runtime.RunnerState{}, false
}
