package runtime

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

type LoopSpec struct {
	Name string

	Run func(ctx context.Context) error

	Logger *slog.Logger

	Observer Observer

	StopGrace time.Duration
}

// Loop is a supervisor of one, for the background activity a component owns
// rather than the process: it starts when its owner starts, stops when its
// owner stops, and keeps everything a supervised runner has — a name, a
// recovered panic, and a return before the stop recorded as a failure and
// handed to the observer. Work whose lifetime is the process's belongs in the
// runner group instead, where the supervisor is the only thing invoked.
type Loop struct {
	name       string
	supervisor *Supervisor
	problem    error
}

// NewLoop cannot fail, because the component that owns a loop builds it where
// it has nobody to report to. The refusals NewSupervisor makes arrive at Start
// instead, which the owner is already returning an error from.
func NewLoop(spec LoopSpec) *Loop {
	if spec.Run == nil {
		return &Loop{name: spec.Name, problem: fmt.Errorf("runtime: the loop %q has nothing to run", spec.Name)}
	}
	supervisor, problem := NewSupervisor(Spec{
		Runners:    []Runner{&loopBody{name: spec.Name, run: spec.Run}},
		DrainGrace: spec.StopGrace,
		Logger:     spec.Logger,
		Observer:   spec.Observer,
	})
	return &Loop{name: spec.Name, supervisor: supervisor, problem: problem}
}

func (this *Loop) Start(ctx context.Context) error {
	if this.problem != nil {
		return this.problem
	}
	return this.supervisor.Start(ctx)
}

func (this *Loop) Stop(ctx context.Context) error {
	if this.problem != nil {
		return nil
	}
	return this.supervisor.Stop(ctx)
}

func (this *Loop) State() RunnerState {
	if this.problem != nil {
		return RunnerState{Name: this.name, Phase: PhaseFailed, Err: this.problem}
	}
	return this.supervisor.States()[0]
}

type loopBody struct {
	name string
	run  func(ctx context.Context) error
}

func (this *loopBody) Name() string { return this.name }

func (this *loopBody) Declaration() Declaration {
	return Declaration{Placement: PerReplica, Durability: NonDurable}
}

func (this *loopBody) Run(ctx context.Context) error { return this.run(ctx) }
