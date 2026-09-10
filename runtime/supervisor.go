package runtime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
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
	drained  chan struct{}
	starting bool
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
		if runnerNil(runner) {
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

type RunnerGenerationStarter interface {
	Runner
	BeginRunnerGeneration()
}

// Start hands every runner its own goroutine and a context that outlives the
// start call: an fx OnStart context is cancelled the moment start-up finishes,
// and a background worker given that context stops the instant it is ready.
// It also opens a fresh generation — the shutdown flag that tells an expected
// return from a silent death is cleared here, and a start is refused while the
// goroutines of the previous generation are still alive, because reusing the
// supervisor over them would run every runner twice.
func (this *Supervisor) Start(ctx context.Context) error {
	this.mutex.Lock()
	if this.started || this.starting {
		this.mutex.Unlock()
		return ErrAlreadyStarted
	}
	if !this.previousGenerationFinished() {
		this.mutex.Unlock()
		return ErrStillStopping
	}
	this.starting = true
	this.mutex.Unlock()
	committed := false
	defer func() {
		if committed {
			return
		}
		this.mutex.Lock()
		this.starting = false
		this.mutex.Unlock()
	}()
	for _, runner := range this.runners {
		if err := beginRunnerGeneration(runner); err != nil {
			return err
		}
	}
	running, cancel := context.WithCancel(context.WithoutCancel(ctx))
	finished := make(chan struct{})
	this.mutex.Lock()
	this.started = true
	this.starting = false
	this.stopping = false
	this.cancel = cancel
	this.finished = finished
	this.drained = nil
	this.mutex.Unlock()
	committed = true

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

func beginRunnerGeneration(runner Runner) (err error) {
	starter, ok := runner.(RunnerGenerationStarter)
	if !ok {
		return nil
	}
	completed := false
	defer func() {
		_ = recover()
		if !completed {
			err = fmt.Errorf("%w: %q", ErrRunnerPanicked, runner.Name())
		}
	}()
	starter.BeginRunnerGeneration()
	completed = true
	return nil
}

func runnerNil(runner Runner) bool {
	if runner == nil {
		return true
	}
	value := reflect.ValueOf(runner)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func (this *Supervisor) previousGenerationFinished() bool {
	return channelFinished(this.finished) && channelFinished(this.drained)
}

func channelFinished(done <-chan struct{}) bool {
	if done == nil {
		return true
	}
	select {
	case <-done:
		return true
	default:
		return false
	}
}

func (this *Supervisor) supervise(ctx context.Context, runner Runner) {
	startedAt := time.Now()
	err := invoke(ctx, runner)
	elapsed := time.Since(startedAt)
	err, canceled, errorText := inspectRunnerError(err)

	this.mutex.Lock()
	stopping := this.stopping
	this.mutex.Unlock()

	expected := stopping && (err == nil || canceled)
	if !expected && err == nil {
		err = fmt.Errorf("%w: %q", ErrRunnerReturned, runner.Name())
		errorText = err.Error()
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
	eventErr := err
	if expected {
		eventErr = nil
	}
	observingLifecycle(this.observer, ctx, lifecycleEvent(LifecycleOperationRun, state.Declaration, eventErr, elapsed))
	if state.Phase == PhaseFailed {
		this.log.ErrorContext(ctx, "a supervised runner stopped on its own",
			slog.String("runner", runner.Name()), slog.String("err", errorText))
	}
}

func inspectRunnerError(returned error) (err error, canceled bool, text string) {
	if returned == nil {
		return nil, false, ""
	}
	canceled = safelyMatches(returned, context.Canceled)
	text = safelyRenderError(returned, "runner returned an error that could not be inspected")
	return returned, canceled, text
}

func safelyMatches(err error, target error) (matched bool) {
	completed := false
	defer func() {
		_ = recover()
		if !completed {
			matched = false
		}
	}()
	matched = errors.Is(err, target)
	completed = true
	return matched
}

func safelyRenderError(err error, fallback string) (text string) {
	completed := false
	defer func() {
		_ = recover()
		if !completed {
			text = fallback
		}
	}()
	text = err.Error()
	completed = true
	return text
}

func invoke(ctx context.Context, runner Runner) (err error) {
	completed := false
	defer func() {
		_ = recover()
		if !completed {
			err = fmt.Errorf("%w: %q", ErrRunnerPanicked, runner.Name())
		}
	}()
	err = runner.Run(ctx)
	completed = true
	return err
}

// Stop drains before it cancels: a runner that is told to finish what it holds
// and then given a cancelled context can commit its last unit of work, and one
// that is cancelled first can only abandon it.
func (this *Supervisor) Stop(ctx context.Context) error {
	this.mutex.Lock()
	if this.starting {
		this.mutex.Unlock()
		return ErrAlreadyStarted
	}
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

	problems, pending, drained := this.drain(deadline)
	this.mutex.Lock()
	this.drained = drained
	this.mutex.Unlock()
	cancel()

	overran := len(pending) > 0
	select {
	case <-finished:
	default:
		select {
		case <-finished:
		case <-deadline.Done():
			overran = true
		}
	}
	if overran {
		pending = append(pending, this.stillRunning()...)
		slices.Sort(pending)
		pending = slices.Compact(pending)
		problems = append(problems, fmt.Errorf("%w: %s", ErrDrainDeadline, strings.Join(pending, ", ")))
	}

	this.mutex.Lock()
	this.started = false
	this.mutex.Unlock()
	return errors.Join(problems...)
}

type drainResult struct {
	position int
	name     string
	err      error
	text     string
}

func (this *Supervisor) drain(ctx context.Context) ([]error, []string, chan struct{}) {
	results := make(chan drainResult, len(this.runners))
	pending := make(map[int]string, len(this.runners))
	var group sync.WaitGroup
	for position, runner := range this.runners {
		drainer, drainable := runner.(Drainer)
		if !drainable {
			continue
		}
		pending[position] = runner.Name()
		declaration := this.runnerDeclaration(runner.Name())
		group.Add(1)
		go func() {
			defer group.Done()
			startedAt := time.Now()
			err := drainer.Drain(ctx)
			elapsed := time.Since(startedAt)
			err, text := inspectDrainError(err)
			observingLifecycle(this.observer, ctx, lifecycleEvent(LifecycleOperationDrain, declaration, err, elapsed))
			results <- drainResult{position: position, name: runner.Name(), err: err, text: text}
		}()
	}
	drained := make(chan struct{})
	go func() {
		group.Wait()
		close(drained)
	}()

	completed := make(map[int]error, len(pending))
	for len(pending) > 0 {
		select {
		case result := <-results:
			delete(pending, result.position)
			if result.err != nil {
				completed[result.position] = drainFailure{name: result.name, cause: result.err, text: result.text}
			}
		case <-ctx.Done():
			for {
				select {
				case result := <-results:
					delete(pending, result.position)
					if result.err != nil {
						completed[result.position] = drainFailure{name: result.name, cause: result.err, text: result.text}
					}
				default:
					problems, names := orderedDrainResults(completed, pending)
					return problems, names, drained
				}
			}
		}
	}
	problems, _ := orderedDrainResults(completed, pending)
	<-drained
	return problems, nil, drained
}

func inspectDrainError(returned error) (err error, text string) {
	if returned == nil {
		return nil, ""
	}
	return returned, safelyRenderError(returned, "drainer returned an error that could not be inspected")
}

type drainFailure struct {
	name  string
	cause error
	text  string
}

func (failure drainFailure) Error() string {
	return fmt.Sprintf("runtime: draining %q: %s", failure.name, failure.text)
}

func (failure drainFailure) Unwrap() error { return failure.cause }

func orderedDrainResults(completed map[int]error, pending map[int]string) ([]error, []string) {
	positions := make([]int, 0, len(completed))
	for position := range completed {
		positions = append(positions, position)
	}
	slices.Sort(positions)
	problems := make([]error, 0, len(positions))
	for _, position := range positions {
		problems = append(problems, completed[position])
	}
	positions = positions[:0]
	for position := range pending {
		positions = append(positions, position)
	}
	slices.Sort(positions)
	names := make([]string, 0, len(positions))
	for _, position := range positions {
		names = append(names, pending[position])
	}
	return problems, names
}

func (this *Supervisor) runnerDeclaration(name string) Declaration {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	return this.states[name].Declaration
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
