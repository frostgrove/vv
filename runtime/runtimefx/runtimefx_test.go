package runtimefx_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.uber.org/fx"

	"github.com/frostgrove/vv/runtime"
	"github.com/frostgrove/vv/runtime/runtimefx"
)

type worker struct {
	name    string
	entered chan struct{}
	left    chan struct{}
	run     func(ctx context.Context) error
}

func newWorker(name string, run func(ctx context.Context) error) *worker {
	return &worker{name: name, entered: make(chan struct{}, 1), left: make(chan struct{}, 1), run: run}
}

func (this *worker) Name() string { return this.name }

func (this *worker) Run(ctx context.Context) error {
	this.entered <- struct{}{}
	defer func() { this.left <- struct{}{} }()
	if this.run != nil {
		return this.run(ctx)
	}
	<-ctx.Done()
	return ctx.Err()
}

func startedApp(t *testing.T, options ...fx.Option) *fx.App {
	t.Helper()
	app := fx.New(append(options, fx.NopLogger)...)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := app.Start(ctx); err != nil {
		t.Fatalf("the application did not start: %v", err)
	}
	t.Cleanup(func() {
		stop, release := context.WithTimeout(context.Background(), 5*time.Second)
		defer release()
		_ = app.Stop(stop)
	})
	return app
}

func await(t *testing.T, signal <-chan struct{}, complaint string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatal(complaint)
	}
}

func TestARunnerInTheGroupRunsWithoutAnInvokeThatNamesIt(t *testing.T) {
	sweeper := newWorker("translation-debt", nil)

	startedApp(t,
		fx.Provide(runtimefx.AsRunner(func() *worker { return sweeper })),
		runtimefx.Auto(),
	)

	await(t, sweeper.entered, "the contributed runner never ran, so activation still needs an fx.Invoke that mentions the type")
}

func TestStoppingTheApplicationStopsTheRunners(t *testing.T) {
	sweeper := newWorker("translation-debt", nil)
	app := startedApp(t,
		fx.Provide(runtimefx.AsRunner(func() *worker { return sweeper })),
		runtimefx.Auto(),
	)
	await(t, sweeper.entered, "the contributed runner never ran")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := app.Stop(ctx); err != nil {
		t.Fatalf("a clean shutdown reported a problem: %v", err)
	}

	await(t, sweeper.left, "the runner was still running after the application stopped")
}

func TestADeadRunnerTakesTheProcessDownWithIt(t *testing.T) {
	sweeper := newWorker("translation-debt", func(context.Context) error {
		return errors.New("the queue is gone")
	})

	app := startedApp(t,
		fx.Provide(runtimefx.AsRunner(func() *worker { return sweeper })),
		runtimefx.Auto(),
	)

	select {
	case signal := <-app.Wait():
		if signal.ExitCode != 1 {
			t.Fatalf("the process asked to exit with %d after losing a worker", signal.ExitCode)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the only worker died and the process kept serving as if nothing had happened")
	}
}

func TestAnApplicationMaySayThatARunnersDeathIsSurvivable(t *testing.T) {
	sweeper := newWorker("translation-debt", func(context.Context) error {
		return errors.New("the queue is gone")
	})

	app := startedApp(t,
		fx.Provide(runtimefx.AsRunner(func() *worker { return sweeper })),
		runtimefx.Supervising(runtimefx.Spec{OnFailure: runtimefx.KeepRunningOnFailure}),
	)
	await(t, sweeper.left, "the runner never ran")

	select {
	case <-app.Wait():
		t.Fatal("a deployment that chose to survive a dead runner was shut down anyway")
	case <-time.After(200 * time.Millisecond):
	}
}

func TestTwoRunnersWithOneNameKeepTheApplicationFromStarting(t *testing.T) {
	err := fx.New(
		fx.NopLogger,
		fx.Provide(
			fx.Annotate(func() *worker { return newWorker("translation-debt", nil) },
				fx.As(new(runtime.Runner)), fx.ResultTags(`group:"vv.runtime.runners"`)),
			fx.Annotate(func() *worker { return newWorker("translation-debt", nil) },
				fx.As(new(runtime.Runner)), fx.ResultTags(`group:"vv.runtime.runners"`)),
		),
		runtimefx.Auto(),
	).Err()

	if !errors.Is(err, runtime.ErrDuplicateRunner) {
		t.Fatalf("an application with two runners of one name started: %v", err)
	}
}
