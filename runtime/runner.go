package runtime

import (
	"context"
	"errors"
	"time"
)

// Runner is a background activity the process owns: Run blocks until the
// context it is given is done, and returning earlier is a failure the
// supervisor reports rather than a shutdown nobody notices.
type Runner interface {
	Name() string

	Run(ctx context.Context) error
}

type Drainer interface {
	Drain(ctx context.Context) error
}

type Readier interface {
	Ready(ctx context.Context) error
}

type Declaring interface {
	Declaration() Declaration
}

type Placement string

const (
	PerReplica Placement = "per-replica"
	Singleton  Placement = "singleton"
)

type Durability string

const (
	NonDurable Durability = "non-durable"
	Durable    Durability = "durable"
)

// Declaration is what a runner promises about how many replicas run it and
// what survives losing one. A process-local ticker is per-replica and
// non-durable: three replicas sweep three times, and a pass interrupted by a
// deploy is simply lost.
type Declaration struct {
	Placement Placement

	Durability Durability
}

var PerReplicaTimer = Declaration{Placement: PerReplica, Durability: NonDurable}

func DeclarationOf(runner Runner) Declaration {
	if declaring, ok := runner.(Declaring); ok {
		return declaring.Declaration()
	}
	return Declaration{}
}

type Phase string

const (
	PhaseIdle    Phase = "idle"
	PhaseRunning Phase = "running"
	PhaseStopped Phase = "stopped"
	PhaseFailed  Phase = "failed"
)

type RunnerState struct {
	Name string

	Declaration Declaration

	Phase Phase

	Err error

	StartedAt time.Time

	EndedAt time.Time
}

type Observer interface {
	Observed(state RunnerState)
}

type ObserverFunc func(state RunnerState)

func (this ObserverFunc) Observed(state RunnerState) { this(state) }

var (
	ErrDuplicateRunner = errors.New("runtime: two runners share a name")
	ErrRunnerReturned  = errors.New("runtime: a runner returned before it was stopped")
	ErrRunnerPanicked  = errors.New("runtime: a runner panicked")
	ErrDrainDeadline   = errors.New("runtime: runners did not stop within the drain grace")
	ErrNotRunning      = errors.New("runtime: the supervisor is not running")
	ErrAlreadyStarted  = errors.New("runtime: the supervisor was already started")
	ErrStillStopping   = errors.New("runtime: the runners of the previous start are still alive")
)
