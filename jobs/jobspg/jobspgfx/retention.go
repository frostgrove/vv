package jobspgfx

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.uber.org/fx"

	"github.com/frostgrove/vv/jobs"
	"github.com/frostgrove/vv/jobs/jobspg"
)

const DefaultHousekeepingInterval = time.Minute
const DefaultHousekeepingSweepTimeout = 30 * time.Second
const DefaultHousekeepingMaxBatches = 64
const MaxHousekeepingBatches = 1024

type housekeepingConfig struct {
	disabled     bool
	interval     time.Duration
	sweepTimeout time.Duration
	batchSize    int
	maxBatches   int
}

type retentionRuntime struct {
	sweeper    jobs.RetentionSweeper
	shutdowner fx.Shutdowner
	config     housekeepingConfig
	cancel     context.CancelFunc
	done       chan error
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

func bindRetentionLifecycle(lifecycle fx.Lifecycle, shutdowner fx.Shutdowner, sweeper jobs.RetentionSweeper, config housekeepingConfig) {
	runtime := &retentionRuntime{sweeper: sweeper, shutdowner: shutdowner, config: config}
	lifecycle.Append(fx.Hook{OnStart: runtime.start, OnStop: runtime.stop})
}

func (runtime *retentionRuntime) start(ctx context.Context) error {
	if runtime.config.disabled {
		return nil
	}
	runContext, cancel := context.WithCancel(context.WithoutCancel(ctx))
	runtime.cancel = cancel
	runtime.done = make(chan error, 1)
	go runtime.run(runContext)
	return nil
}

func (runtime *retentionRuntime) run(ctx context.Context) {
	timer := time.NewTimer(runtime.config.interval)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			runtime.done <- nil
			return
		case <-timer.C:
			cycleContext, cancel := context.WithTimeout(ctx, runtime.config.sweepTimeout)
			err := drainRetention(cycleContext, runtime.sweeper, runtime.config.batchSize, runtime.config.maxBatches)
			cycleTimedOut := errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil
			cancel()
			if cycleTimedOut {
				timer.Reset(runtime.config.interval)
				continue
			}
			if errors.Is(err, jobspg.ErrNotReady) {
				timer.Reset(runtime.config.interval)
				continue
			}
			if err == nil {
				timer.Reset(runtime.config.interval)
				continue
			}
			if ctx.Err() == nil {
				_ = runtime.shutdowner.Shutdown(fx.ExitCode(1))
			}
			runtime.done <- err
			return
		}
	}
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

func (runtime *retentionRuntime) stop(ctx context.Context) error {
	if runtime.cancel == nil {
		return nil
	}
	runtime.cancel()
	select {
	case err := <-runtime.done:
		if errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}
