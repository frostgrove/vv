package runtime_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frostgrove/vv/runtime"
)

type worker struct {
	name  string
	run   func(ctx context.Context) error
	drain func(ctx context.Context) error
	ready func(ctx context.Context) error
}

func (this *worker) Name() string { return this.name }

func (this *worker) Run(ctx context.Context) error {
	if this.run != nil {
		return this.run(ctx)
	}
	<-ctx.Done()
	return ctx.Err()
}

type drainingWorker struct {
	worker
}

func (this *drainingWorker) Drain(ctx context.Context) error { return this.drain(ctx) }

type reportingWorker struct {
	worker
}

func (this *reportingWorker) Ready(ctx context.Context) error { return this.ready(ctx) }

func supervisor(t *testing.T, spec runtime.Spec) *runtime.Supervisor {
	t.Helper()
	built, err := runtime.NewSupervisor(spec)
	if err != nil {
		t.Fatalf("a well-formed supervisor was refused: %v", err)
	}
	return built
}

func started(t *testing.T, supervisor *runtime.Supervisor) {
	t.Helper()
	if err := supervisor.Start(context.Background()); err != nil {
		t.Fatalf("the supervisor did not start: %v", err)
	}
	t.Cleanup(func() { _ = supervisor.Stop(context.Background()) })
}

func stateOf(t *testing.T, supervisor *runtime.Supervisor, name string) runtime.RunnerState {
	t.Helper()
	for _, state := range supervisor.States() {
		if state.Name == name {
			return state
		}
	}
	t.Fatalf("the supervisor knows nothing about %q", name)
	return runtime.RunnerState{}
}

func awaitPhase(t *testing.T, supervisor *runtime.Supervisor, name string, phase runtime.Phase) runtime.RunnerState {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		state := stateOf(t, supervisor, name)
		if state.Phase == phase {
			return state
		}
		if time.Now().After(deadline) {
			t.Fatalf("%q stayed in phase %q instead of reaching %q", name, state.Phase, phase)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestARunnerThatReturnsOnItsOwnIsReportedInsteadOfDisappearing(t *testing.T) {
	supervised := supervisor(t, runtime.Spec{Runners: []runtime.Runner{
		&worker{name: "sweeper", run: func(context.Context) error { return nil }},
	}})
	started(t, supervised)

	state := awaitPhase(t, supervised, "sweeper", runtime.PhaseFailed)

	if !errors.Is(state.Err, runtime.ErrRunnerReturned) {
		t.Fatalf("a worker that quietly returned was not recorded as a failure: %v", state.Err)
	}
}

func TestAPanickingRunnerFailsAloneAndTheOthersKeepRunning(t *testing.T) {
	supervised := supervisor(t, runtime.Spec{Runners: []runtime.Runner{
		&worker{name: "sweeper", run: func(context.Context) error { panic("nil repository") }},
		&worker{name: "collector"},
	}})
	started(t, supervised)

	state := awaitPhase(t, supervised, "sweeper", runtime.PhaseFailed)

	if !errors.Is(state.Err, runtime.ErrRunnerPanicked) {
		t.Fatalf("a panicking runner was not recorded as a panic: %v", state.Err)
	}
	if phase := stateOf(t, supervised, "collector").Phase; phase != runtime.PhaseRunning {
		t.Fatalf("one runner's panic stopped another: %q", phase)
	}
}

func TestTwoRunnersWithOneNameAreRefusedBeforeAnythingStarts(t *testing.T) {
	_, err := runtime.NewSupervisor(runtime.Spec{Runners: []runtime.Runner{
		&worker{name: "sweeper"},
		&worker{name: "sweeper"},
	}})

	if !errors.Is(err, runtime.ErrDuplicateRunner) {
		t.Fatalf("two runners called \"sweeper\" were accepted, so one of them is unaddressable: %v", err)
	}
}

func TestARunnerOutlivesTheStartUpThatLaunchedIt(t *testing.T) {
	supervised := supervisor(t, runtime.Spec{Runners: []runtime.Runner{&worker{name: "sweeper"}}})

	startUp, done := context.WithCancel(context.Background())
	if err := supervised.Start(startUp); err != nil {
		t.Fatalf("the supervisor did not start: %v", err)
	}
	t.Cleanup(func() { _ = supervised.Stop(context.Background()) })
	done()

	time.Sleep(20 * time.Millisecond)
	if phase := stateOf(t, supervised, "sweeper").Phase; phase != runtime.PhaseRunning {
		t.Fatalf("the runner was handed the start-up context and stopped with it: %q", phase)
	}
}

func TestStopDrainsBeforeItCancels(t *testing.T) {
	var order []string
	var mutex sync.Mutex
	record := func(step string) {
		mutex.Lock()
		defer mutex.Unlock()
		order = append(order, step)
	}

	supervised := supervisor(t, runtime.Spec{Runners: []runtime.Runner{
		&drainingWorker{worker: worker{
			name: "queue",
			run: func(ctx context.Context) error {
				<-ctx.Done()
				record("cancelled")
				return ctx.Err()
			},
			drain: func(context.Context) error {
				time.Sleep(50 * time.Millisecond)
				record("drained")
				return nil
			},
		}},
	}})
	started(t, supervised)

	if err := supervised.Stop(context.Background()); err != nil {
		t.Fatalf("a clean stop reported a problem: %v", err)
	}

	mutex.Lock()
	defer mutex.Unlock()
	if len(order) != 2 || order[0] != "drained" || order[1] != "cancelled" {
		t.Fatalf("the runner was cancelled before it was allowed to drain: %v", order)
	}
}

func TestStopNamesTheRunnerThatIgnoredTheDrainGrace(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	supervised := supervisor(t, runtime.Spec{
		DrainGrace: 30 * time.Millisecond,
		Runners: []runtime.Runner{
			&worker{name: "stuck", run: func(context.Context) error {
				<-release
				return nil
			}},
		},
	})
	started(t, supervised)

	err := supervised.Stop(context.Background())

	if !errors.Is(err, runtime.ErrDrainDeadline) {
		t.Fatalf("a runner that never returned let shutdown report success: %v", err)
	}
	if !strings.Contains(err.Error(), "stuck") {
		t.Fatalf("the shutdown failure does not name the runner still holding the process: %v", err)
	}
}

func TestARunnerStoppedOnPurposeIsNotAFailure(t *testing.T) {
	supervised := supervisor(t, runtime.Spec{Runners: []runtime.Runner{&worker{name: "sweeper"}}})
	started(t, supervised)

	if err := supervised.Stop(context.Background()); err != nil {
		t.Fatalf("stopping a well-behaved runner reported a problem: %v", err)
	}
	if phase := stateOf(t, supervised, "sweeper").Phase; phase != runtime.PhaseStopped {
		t.Fatalf("a runner that obeyed cancellation was recorded as %q", phase)
	}
}

func TestReadinessRefusesWhileASupervisedRunnerIsDead(t *testing.T) {
	supervised := supervisor(t, runtime.Spec{Runners: []runtime.Runner{
		&worker{name: "sweeper", run: func(context.Context) error { return errors.New("no database") }},
	}})
	started(t, supervised)
	awaitPhase(t, supervised, "sweeper", runtime.PhaseFailed)

	if err := supervised.Ready(context.Background()); err == nil {
		t.Fatal("a process whose only worker is dead reported itself ready")
	}
}

func TestReadinessAsksTheRunnersThatCanAnswer(t *testing.T) {
	supervised := supervisor(t, runtime.Spec{Runners: []runtime.Runner{
		&reportingWorker{worker: worker{name: "consumer", ready: func(context.Context) error {
			return errors.New("no lease yet")
		}}},
	}})
	started(t, supervised)

	err := supervised.Ready(context.Background())

	if err == nil || !strings.Contains(err.Error(), "no lease yet") {
		t.Fatalf("a runner that reports itself unready was not asked: %v", err)
	}
}

func TestAnObserverSeesEveryPhaseAndCannotBreakTheSupervisor(t *testing.T) {
	var seen atomic.Int64
	supervised := supervisor(t, runtime.Spec{
		Observer: runtime.ObserverFunc(func(runtime.RunnerState) {
			seen.Add(1)
			panic("the metrics registry is nil")
		}),
		Runners: []runtime.Runner{&worker{name: "sweeper"}},
	})
	started(t, supervised)

	if err := supervised.Stop(context.Background()); err != nil {
		t.Fatalf("a panicking observer broke shutdown: %v", err)
	}
	if seen.Load() < 2 {
		t.Fatalf("the observer saw %d transitions, not the start and the stop", seen.Load())
	}
}

func TestASecondStartIsRefusedRatherThanDoubled(t *testing.T) {
	supervised := supervisor(t, runtime.Spec{Runners: []runtime.Runner{&worker{name: "sweeper"}}})
	started(t, supervised)

	if err := supervised.Start(context.Background()); !errors.Is(err, runtime.ErrAlreadyStarted) {
		t.Fatalf("starting twice ran every runner twice: %v", err)
	}
}

func TestARunnerThatDiesAfterARestartIsStillReportedAsAFailure(t *testing.T) {
	dying := make(chan struct{})
	supervised := supervisor(t, runtime.Spec{Runners: []runtime.Runner{
		&worker{name: "sweeper", run: func(ctx context.Context) error {
			select {
			case <-dying:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}},
	}})
	started(t, supervised)
	if err := supervised.Stop(context.Background()); err != nil {
		t.Fatalf("stopping a well-behaved runner reported a problem: %v", err)
	}
	started(t, supervised)

	close(dying)

	state := awaitPhase(t, supervised, "sweeper", runtime.PhaseFailed)
	if !errors.Is(state.Err, runtime.ErrRunnerReturned) {
		t.Fatalf("a worker that quietly returned after a restart was not recorded as a failure: %v", state.Err)
	}
	if err := supervised.Ready(context.Background()); err == nil {
		t.Fatal("a process whose only worker died after a restart reported itself ready")
	}
}

func TestARestartForgetsTheFailureThatPrecededIt(t *testing.T) {
	var runs atomic.Int64
	supervised := supervisor(t, runtime.Spec{Runners: []runtime.Runner{
		&worker{name: "sweeper", run: func(ctx context.Context) error {
			if runs.Add(1) == 1 {
				return errors.New("no database")
			}
			<-ctx.Done()
			return ctx.Err()
		}},
	}})
	started(t, supervised)
	awaitPhase(t, supervised, "sweeper", runtime.PhaseFailed)
	if err := supervised.Stop(context.Background()); err != nil {
		t.Fatalf("stopping after a failure reported a problem of its own: %v", err)
	}
	started(t, supervised)

	state := stateOf(t, supervised, "sweeper")
	if state.Phase != runtime.PhaseRunning {
		t.Fatalf("a restarted runner is in phase %q instead of running", state.Phase)
	}
	if state.Err != nil {
		t.Fatalf("the restarted runner still carries the failure of the previous start: %v", state.Err)
	}
	if err := supervised.Ready(context.Background()); err != nil {
		t.Fatalf("a restarted process was called unready over a failure it no longer has: %v", err)
	}
}

func TestAStartIsRefusedWhileTheRunnersOfTheLastStopAreStillAlive(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	supervised := supervisor(t, runtime.Spec{
		DrainGrace: 30 * time.Millisecond,
		Runners: []runtime.Runner{
			&worker{name: "stuck", run: func(context.Context) error {
				<-release
				return nil
			}},
		},
	})
	started(t, supervised)
	if err := supervised.Stop(context.Background()); !errors.Is(err, runtime.ErrDrainDeadline) {
		t.Fatalf("a runner that never returned let shutdown report success: %v", err)
	}

	err := supervised.Start(context.Background())

	if !errors.Is(err, runtime.ErrStillStopping) {
		t.Fatalf("a second copy of a runner the last stop never got back was launched: %v", err)
	}
}
