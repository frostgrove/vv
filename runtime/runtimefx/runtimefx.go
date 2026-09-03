package runtimefx

import (
	"context"
	"log/slog"
	"time"

	"go.uber.org/fx"

	"github.com/frostgrove/vv/runtime"
)

const runnerGroup = `group:"vv.runtime.runners"`

func AsRunner(constructor any) any {
	return fx.Annotate(constructor, fx.As(new(runtime.Runner)), fx.ResultTags(runnerGroup))
}

type Registered struct {
	fx.In

	All []runtime.Runner `group:"vv.runtime.runners"`

	Logger *slog.Logger `optional:"true"`

	Observer runtime.Observer `optional:"true"`
}

type FailurePolicy string

const (
	ShutDownOnFailure    FailurePolicy = "shut-down"
	KeepRunningOnFailure FailurePolicy = "keep-running"
)

type Spec struct {
	DrainGrace time.Duration

	OnFailure FailurePolicy
}

func Supervising(spec Spec) fx.Option {
	return fx.Module("vv.runtime",
		fx.Provide(func(registered Registered, shutdowner fx.Shutdowner) (*runtime.Supervisor, error) {
			return runtime.NewSupervisor(runtime.Spec{
				Runners:    registered.All,
				DrainGrace: spec.DrainGrace,
				Logger:     registered.Logger,
				Observer:   watching(spec, registered, shutdowner),
			})
		}),
		fx.Invoke(func(lifecycle fx.Lifecycle, supervisor *runtime.Supervisor) {
			lifecycle.Append(fx.Hook{OnStart: supervisor.Start, OnStop: supervisor.Stop})
		}),
	)
}

func Auto() fx.Option { return Supervising(Spec{}) }

// ShuttingDownOnFailure is the failure half of Supervising on its own, for a
// component that owns a runtime.Loop: a replica that kept its port open while
// one of its loops is dead serves half of what it says it serves.
func ShuttingDownOnFailure(shutdowner fx.Shutdowner, log *slog.Logger) runtime.Observer {
	return &stopper{shutdowner: shutdowner, log: log}
}

func watching(spec Spec, registered Registered, shutdowner fx.Shutdowner) runtime.Observer {
	var watchers observers
	if registered.Observer != nil {
		watchers = append(watchers, registered.Observer)
	}
	if spec.OnFailure != KeepRunningOnFailure {
		watchers = append(watchers, &stopper{shutdowner: shutdowner, log: registered.Logger})
	}
	if len(watchers) == 0 {
		return nil
	}
	return watchers
}

type observers []runtime.Observer

func (this observers) Observed(state runtime.RunnerState) {
	for _, observer := range this {
		observe(observer, state)
	}
}

func observe(observer runtime.Observer, state runtime.RunnerState) {
	defer func() { _ = recover() }()
	observer.Observed(state)
}

type stopper struct {
	shutdowner fx.Shutdowner
	log        *slog.Logger
}

func (this *stopper) Observed(state runtime.RunnerState) {
	if state.Phase != runtime.PhaseFailed {
		return
	}
	logger(this.log).ErrorContext(context.Background(), "shutting down: a supervised runner died",
		slog.String("runner", state.Name))
	_ = this.shutdowner.Shutdown(fx.ExitCode(1))
}

func logger(log *slog.Logger) *slog.Logger {
	if log != nil {
		return log
	}
	return slog.Default()
}
