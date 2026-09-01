package jobspgfx

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/fx"

	"github.com/frostgrove/vv/jobs"
)

type retentionSweeperFunc func(context.Context, int) (int, error)

func (sweeper retentionSweeperFunc) SweepTerminalRetention(ctx context.Context, limit int) (int, error) {
	return sweeper(ctx, limit)
}

func TestNormalizeHousekeepingDefaultsDisablesAndRejectsUnsafeSettings(t *testing.T) {
	configured, err := normalizeHousekeeping(HousekeepingSettings{})
	if err != nil {
		t.Fatal(err)
	}
	if configured.disabled || configured.interval != DefaultHousekeepingInterval || configured.sweepTimeout != DefaultHousekeepingSweepTimeout || configured.batchSize != jobs.DefaultPurgeLimit || configured.maxBatches != DefaultHousekeepingMaxBatches {
		t.Fatalf("defaults = %+v", configured)
	}
	disabled, err := normalizeHousekeeping(HousekeepingSettings{Disabled: true, Interval: -1, BatchSize: -1})
	if err != nil || !disabled.disabled {
		t.Fatalf("disabled = (%+v, %v)", disabled, err)
	}
	for _, settings := range []HousekeepingSettings{
		{Interval: -1},
		{Interval: jobs.MinimumPollInterval - 1},
		{Interval: jobs.MaxRetention + 1},
		{SweepTimeout: -1},
		{SweepTimeout: jobs.MinimumPollInterval - 1},
		{SweepTimeout: jobs.MaxRetention + 1},
		{BatchSize: -1},
		{BatchSize: jobs.MaxPurgeLimit + 1},
		{MaxBatches: -1},
		{MaxBatches: MaxHousekeepingBatches + 1},
	} {
		if _, err := normalizeHousekeeping(settings); err == nil {
			t.Fatalf("accepted %+v", settings)
		}
	}
}

func TestRetentionLifecycleRepeatsBoundedSweepsAndCancelsActiveWork(t *testing.T) {
	started := make(chan context.Context, 2)
	var calls atomic.Int32
	sweeper := retentionSweeperFunc(func(ctx context.Context, limit int) (int, error) {
		if limit != 7 {
			t.Errorf("limit = %d", limit)
		}
		calls.Add(1)
		started <- ctx
		if calls.Load() == 1 {
			return limit, nil
		}
		<-ctx.Done()
		return 0, ctx.Err()
	})
	app := retentionTestApp(t, sweeper, housekeepingConfig{interval: jobs.MinimumPollInterval, sweepTimeout: time.Second, batchSize: 7, maxBatches: 1})
	startApp(t, app)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first retention sweep did not run")
	}
	var active context.Context
	select {
	case active = <-started:
	case <-time.After(time.Second):
		t.Fatal("second retention sweep did not run")
	}
	stopContext, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := app.Stop(stopContext); err != nil {
		t.Fatal(err)
	}
	if active.Err() == nil || calls.Load() != 2 {
		t.Fatalf("active context=%v calls=%d", active.Err(), calls.Load())
	}
}

func TestRetentionDrainProcessesBacklogInBoundedBatches(t *testing.T) {
	remaining := 10
	var sizes []int
	sweeper := retentionSweeperFunc(func(context.Context, int) (int, error) {
		count := min(3, remaining)
		remaining -= count
		sizes = append(sizes, count)
		return count, nil
	})
	if err := drainRetention(context.Background(), sweeper, 3, 8); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 || len(sizes) != 4 || sizes[0] != 3 || sizes[1] != 3 || sizes[2] != 3 || sizes[3] != 1 {
		t.Fatalf("drain sizes=%v remaining=%d", sizes, remaining)
	}
	calls := 0
	if err := drainRetention(context.Background(), retentionSweeperFunc(func(context.Context, int) (int, error) {
		calls++
		return 3, nil
	}), 3, 2); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("bounded drain calls = %d", calls)
	}
	if err := drainRetention(context.Background(), retentionSweeperFunc(func(context.Context, int) (int, error) {
		return 4, nil
	}), 3, 1); !errors.Is(err, jobs.ErrDriver) {
		t.Fatalf("invalid backend count = %v", err)
	}
}

func TestRetentionLifecycleTreatsFailuresAndPanicsAsFatal(t *testing.T) {
	for _, test := range []struct {
		name    string
		sweeper retentionSweeperFunc
	}{
		{name: "error", sweeper: func(context.Context, int) (int, error) { return 0, errors.New("retention failed") }},
		{name: "panic", sweeper: func(context.Context, int) (int, error) { panic("retention failed") }},
	} {
		t.Run(test.name, func(t *testing.T) {
			app := retentionTestApp(t, test.sweeper, housekeepingConfig{interval: jobs.MinimumPollInterval, sweepTimeout: time.Second, batchSize: 1, maxBatches: 1})
			startApp(t, app)
			select {
			case signal := <-app.Wait():
				if signal.ExitCode != 1 {
					t.Fatalf("exit code = %d", signal.ExitCode)
				}
			case <-time.After(time.Second):
				t.Fatal("retention failure did not stop the application")
			}
			stopContext, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := app.Stop(stopContext); err == nil {
				t.Fatal("fatal retention error was lost during stop")
			}
		})
	}
}

func TestRetentionLifecycleRetriesAConfiguredSweepTimeout(t *testing.T) {
	called := make(chan int, 2)
	var calls atomic.Int32
	sweeper := retentionSweeperFunc(func(ctx context.Context, _ int) (int, error) {
		call := int(calls.Add(1))
		called <- call
		if call == 1 {
			<-ctx.Done()
			return 0, ctx.Err()
		}
		return 0, nil
	})
	app := retentionTestApp(t, sweeper, housekeepingConfig{
		interval:     jobs.MinimumPollInterval,
		sweepTimeout: jobs.MinimumPollInterval,
		batchSize:    1,
		maxBatches:   1,
	})
	startApp(t, app)
	for expected := 1; expected <= 2; expected++ {
		select {
		case call := <-called:
			if call != expected {
				t.Fatalf("call = %d, want %d", call, expected)
			}
		case <-time.After(time.Second):
			t.Fatalf("sweep %d did not run", expected)
		}
	}
	select {
	case signal := <-app.Wait():
		t.Fatalf("sweep timeout stopped application: %+v", signal)
	default:
	}
	stopContext, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := app.Stop(stopContext); err != nil {
		t.Fatal(err)
	}
}

func TestRetentionLifecycleWaitsTheIntervalAfterEachDrain(t *testing.T) {
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	var calls atomic.Int32
	sweeper := retentionSweeperFunc(func(context.Context, int) (int, error) {
		call := calls.Add(1)
		started <- struct{}{}
		if call == 1 {
			<-release
		}
		return 0, nil
	})
	interval := 50 * time.Millisecond
	app := retentionTestApp(t, sweeper, housekeepingConfig{interval: interval, sweepTimeout: time.Second, batchSize: 1, maxBatches: 1})
	startApp(t, app)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first sweep did not run")
	}
	time.Sleep(2 * interval)
	close(release)
	select {
	case <-started:
		t.Fatal("queued tick bypassed the post-drain interval")
	case <-time.After(interval / 2):
	}
	select {
	case <-started:
	case <-time.After(2 * interval):
		t.Fatal("second sweep did not run after the interval")
	}
	stopContext, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := app.Stop(stopContext); err != nil {
		t.Fatal(err)
	}
}

func TestDisabledRetentionLifecycleDoesNotCallTheBackend(t *testing.T) {
	called := make(chan struct{}, 1)
	app := retentionTestApp(t, retentionSweeperFunc(func(context.Context, int) (int, error) {
		called <- struct{}{}
		return 0, nil
	}), housekeepingConfig{disabled: true})
	startApp(t, app)
	time.Sleep(2 * jobs.MinimumPollInterval)
	stopContext, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := app.Stop(stopContext); err != nil {
		t.Fatal(err)
	}
	select {
	case <-called:
		t.Fatal("disabled retention called the backend")
	default:
	}
}

func retentionTestApp(t *testing.T, sweeper jobs.RetentionSweeper, config housekeepingConfig) *fx.App {
	t.Helper()
	app := fx.New(
		fx.NopLogger,
		fx.Supply(config),
		fx.Provide(func() jobs.RetentionSweeper { return sweeper }),
		fx.Invoke(bindRetentionLifecycle),
	)
	if err := app.Err(); err != nil {
		t.Fatal(err)
	}
	return app
}

func startApp(t *testing.T, app *fx.App) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := app.Start(ctx); err != nil {
		t.Fatal(err)
	}
}
