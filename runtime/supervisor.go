package runtime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"
)

const DefaultDrainGrace = 15 * time.Second

type Spec struct {
	Runners []Runner

	DrainGrace time.Duration

	Logger *slog.Logger

	Observer Observer
}

type Supervisor struct {
	runners  []Runner
	grace    time.Duration
	log      *slog.Logger
	observer Observer

	mutex    sync.Mutex
	states   map[string]RunnerState
	cancel   context.CancelFunc
	finished chan struct{}
	started  bool
	stopping bool
}

func NewSupervisor(spec Spec) (*Supervisor, error) {
	grace := spec.DrainGrace
	if grace == 0 {
		grace = DefaultDrainGrace
	}
	log := spec.Logger
	if log == nil {
		log = slog.Default()
	}

	var problems []error
	if grace < 0 {
		problems = append(problems, fmt.Errorf("runtime: the drain grace is negative (%s)", grace))
	}

	named := make(map[string]struct{}, len(spec.Runners))
	runners := make([]Runner, 0, len(spec.Runners))
	for position, runner := range spec.Runners {
		if runner == nil {
			problems = append(problems, fmt.Errorf("runtime: runner %d is nil", position))
			continue
		}
		name := runner.Name()
		if name == "" {
			problems = append(problems, fmt.Errorf("runtime: runner %d has no name", position))
			continue
		}
		if _, duplicate := named[name]; duplicate {
			problems = append(problems, fmt.Errorf("%w: %q", ErrDuplicateRunner, name))
			continue
		}
		named[name] = struct{}{}
		runners = append(runners, runner)
	}
	if len(problems) > 0 {
		return nil, errors.Join(problems...)
	}

	supervisor := &Supervisor{
		runners:  runners,
		grace:    grace,
		log:      log,
		observer: spec.Observer,
		states:   make(map[string]RunnerState, len(runners)),
	}
	for _, runner := range runners {
		supervisor.states[runner.Name()] = RunnerState{
			Name:        runner.Name(),
			Declaration: DeclarationOf(runner),
			Phase:       PhaseIdle,
		}
	}
	return supervisor, nil
}

func Auto(runners ...Runner) (*Supervisor, error) { return NewSupervisor(Spec{Runners: runners}) }

// Start hands every runner its own goroutine and a context that outlives the
// start call: an fx OnStart context is cancelled the moment start-up finishes,
// and a background worker given that context stops the instant it is ready.
// It also opens a fresh generation — the shutdown flag that tells an expected
// return from a silent death is cleared here, and a start is refused while the
// goroutines of the previous generation are still alive, because reusing the
// supervisor over them would run every runner twice.
func (this *Supervisor) Start(ctx context.Context) error {
	this.mutex.Lock()
	if this.started {
		this.mutex.Unlock()
		return ErrAlreadyStarted
	}
	if !this.previousGenerationFinished() {
		this.mutex.Unlock()
		return ErrStillStopping
	}
	running, cancel := context.WithCancel(context.WithoutCancel(ctx))
	finished := make(chan struct{})
	this.started = true
	this.stopping = false
	this.cancel = cancel
	this.finished = finished
	this.mutex.Unlock()

	var group sync.WaitGroup
	for _, runner := range this.runners {
		this.transition(runner.Name(), func(state *RunnerState) {
			*state = RunnerState{
				Name:        runner.Name(),
				Declaration: DeclarationOf(runner),
				Phase:       PhaseRunning,
				StartedAt:   time.Now(),
			}
		})
		group.Add(1)
		go func() {
			defer group.Done()
			this.supervise(running, runner)
		}()
	}

	go func() {
		group.Wait()
		close(finished)
	}()
	return nil
}

func (this *Supervisor) previousGenerationFinished() bool {
	if this.finished == nil {
		return true
	}
	select {
	case <-this.finished:
		return true
	default:
		return false
	}
}

func (this *Supervisor) supervise(ctx context.Context, runner Runner) {
	err := invoke(ctx, runner)

	this.mutex.Lock()
	stopping := this.stopping
	this.mutex.Unlock()

	expected := stopping && (err == nil || errors.Is(err, context.Canceled))
	if !expected && err == nil {
		err = fmt.Errorf("%w: %q", ErrRunnerReturned, runner.Name())
	}

	state := this.transition(runner.Name(), func(state *RunnerState) {
		state.EndedAt = time.Now()
		if expected {
			state.Phase = PhaseStopped
			return
		}
		state.Phase = PhaseFailed
		state.Err = err
	})
	if state.Phase == PhaseFailed {
		this.log.Error("a supervised runner stopped on its own",
			slog.String("runner", runner.Name()), slog.String("err", err.Error()))
	}
}

func invoke(ctx context.Context, runner Runner) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("%w: %q: %v", ErrRunnerPanicked, runner.Name(), recovered)
		}
	}()
	return runner.Run(ctx)
}

// Stop drains before it cancels: a runner that is told to finish what it holds
// and then given a cancelled context can commit its last unit of work, and one
// that is cancelled first can only abandon it.
func (this *Supervisor) Stop(ctx context.Context) error {
	this.mutex.Lock()
	if !this.started {
		this.mutex.Unlock()
		return nil
	}
	this.stopping = true
	cancel := this.cancel
	finished := this.finished
	this.mutex.Unlock()

	deadline, release := context.WithTimeout(ctx, this.grace)
	defer release()

	problems := this.drain(deadline)
	cancel()

	select {
	case <-finished:
	case <-deadline.Done():
		problems = append(problems, fmt.Errorf("%w: %s", ErrDrainDeadline, strings.Join(this.stillRunning(), ", ")))
	}

	this.mutex.Lock()
	this.started = false
	this.mutex.Unlock()
	return errors.Join(problems...)
}

func (this *Supervisor) drain(ctx context.Context) []error {
	problems := make([]error, len(this.runners))
	var group sync.WaitGroup
	for position, runner := range this.runners {
		drainer, drainable := runner.(Drainer)
		if !drainable {
			continue
		}
		group.Add(1)
		go func() {
			defer group.Done()
			if err := drainer.Drain(ctx); err != nil {
				problems[position] = fmt.Errorf("runtime: draining %q: %w", runner.Name(), err)
			}
		}()
	}
	group.Wait()
	return slices.DeleteFunc(problems, func(err error) bool { return err == nil })
}

func (this *Supervisor) stillRunning() []string {
	var names []string
	for _, state := range this.States() {
		if state.Phase == PhaseRunning {
			names = append(names, state.Name)
		}
	}
	return names
}

func (this *Supervisor) States() []RunnerState {
	this.mutex.Lock()
	defer this.mutex.Unlock()

	states := make([]RunnerState, 0, len(this.runners))
	for _, runner := range this.runners {
		states = append(states, this.states[runner.Name()])
	}
	slices.SortFunc(states, func(a, b RunnerState) int { return strings.Compare(a.Name, b.Name) })
	return states
}

// Ready is the readiness half of the runner contract, and it is a seam rather
// than a health check: this package never learns what a health registry is, and
// the composition root is what registers this as a contribution.
func (this *Supervisor) Ready(ctx context.Context) error {
	this.mutex.Lock()
	started := this.started
	this.mutex.Unlock()
	if !started {
		return ErrNotRunning
	}

	var problems []error
	for _, state := range this.States() {
		if state.Phase == PhaseFailed {
			problems = append(problems, state.Err)
		}
	}
	for _, runner := range this.runners {
		readier, reports := runner.(Readier)
		if !reports {
			continue
		}
		if err := readier.Ready(ctx); err != nil {
			problems = append(problems, fmt.Errorf("runtime: %q is not ready: %w", runner.Name(), err))
		}
	}
	return errors.Join(problems...)
}

func (this *Supervisor) transition(name string, change func(state *RunnerState)) RunnerState {
	this.mutex.Lock()
	state := this.states[name]
	change(&state)
	this.states[name] = state
	observer := this.observer
	this.mutex.Unlock()

	if observer != nil {
		observing(observer, state)
	}
	return state
}

func observing(observer Observer, state RunnerState) {
	defer func() { _ = recover() }()
	observer.Observed(state)
}
