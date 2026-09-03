package runtime_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/frostgrove/vv/runtime"
)

func awaitLoop(t *testing.T, loop *runtime.Loop, phase runtime.Phase) runtime.RunnerState {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		state := loop.State()
		if state.Phase == phase {
			return state
		}
		if time.Now().After(deadline) {
			t.Fatalf("the loop stayed in phase %q instead of reaching %q", state.Phase, phase)
		}
		time.Sleep(time.Millisecond)
	}
}

type recordedStates struct {
	mutex  sync.Mutex
	states []runtime.RunnerState
}

func (this *recordedStates) Observed(state runtime.RunnerState) {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	this.states = append(this.states, state)
}

func (this *recordedStates) failures() []runtime.RunnerState {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	var failed []runtime.RunnerState
	for _, state := range this.states {
		if state.Phase == runtime.PhaseFailed {
			failed = append(failed, state)
		}
	}
	return failed
}

func TestAPanicInALoopIsRecoveredAndReportedUnderItsNameInsteadOfTakingTheProcessDown(t *testing.T) {
	reported := &recordedStates{}
	loop := runtime.NewLoop(runtime.LoopSpec{
		Name:     "core.listener",
		Observer: reported,
		Run:      func(context.Context) error { panic("the pool handed out nothing") },
	})

	if err := loop.Start(context.Background()); err != nil {
		t.Fatalf("the loop did not start: %v", err)
	}
	t.Cleanup(func() { _ = loop.Stop(context.Background()) })

	state := awaitLoop(t, loop, runtime.PhaseFailed)
	if !errors.Is(state.Err, runtime.ErrRunnerPanicked) {
		t.Fatalf("a panicking loop was recorded as %v", state.Err)
	}
	if !strings.Contains(state.Err.Error(), "core.listener") {
		t.Fatalf("the failure does not name the loop it came from: %v", state.Err)
	}

	failures := reported.failures()
	if len(failures) != 1 || failures[0].Name != "core.listener" {
		t.Fatalf("the owner's observer was told %+v, and a panic nobody is told about is a component that stopped working in silence", failures)
	}
}

func TestALoopThatReturnsBeforeItsOwnerStopsItIsAFailure(t *testing.T) {
	loop := runtime.NewLoop(runtime.LoopSpec{
		Name: "core.listener",
		Run:  func(context.Context) error { return nil },
	})

	if err := loop.Start(context.Background()); err != nil {
		t.Fatalf("the loop did not start: %v", err)
	}
	t.Cleanup(func() { _ = loop.Stop(context.Background()) })

	state := awaitLoop(t, loop, runtime.PhaseFailed)
	if !errors.Is(state.Err, runtime.ErrRunnerReturned) {
		t.Fatalf("a loop that quietly returned was recorded as %v", state.Err)
	}
}

func TestStoppingALoopWaitsForTheGoroutineAndIsNotAFailure(t *testing.T) {
	released := make(chan struct{})
	loop := runtime.NewLoop(runtime.LoopSpec{
		Name: "core.listener",
		Run: func(ctx context.Context) error {
			<-ctx.Done()
			time.Sleep(10 * time.Millisecond)
			close(released)
			return ctx.Err()
		},
	})

	if err := loop.Start(context.Background()); err != nil {
		t.Fatalf("the loop did not start: %v", err)
	}
	if err := loop.Stop(context.Background()); err != nil {
		t.Fatalf("stopping the loop reported %v", err)
	}

	select {
	case <-released:
	default:
		t.Fatal("the stop returned while the loop was still running, so the owner is free to tear down what it is still reading")
	}
	if phase := loop.State().Phase; phase != runtime.PhaseStopped {
		t.Fatalf("a loop its owner stopped is in phase %q", phase)
	}
}

func TestALoopStoppedBeforeItStartedAndOneWithNothingToRunAreBothAnswered(t *testing.T) {
	never := runtime.NewLoop(runtime.LoopSpec{Name: "core.listener", Run: func(context.Context) error { return nil }})
	if err := never.Stop(context.Background()); err != nil {
		t.Fatalf("stopping a loop that never started reported %v", err)
	}

	empty := runtime.NewLoop(runtime.LoopSpec{Name: "core.listener"})
	if err := empty.Start(context.Background()); err == nil {
		t.Fatal("a loop with no body started, and the component that owns it heard nothing")
	}
	if err := empty.Stop(context.Background()); err != nil {
		t.Fatalf("stopping a loop that was refused a start reported %v", err)
	}
}

func TestALoopIsPerReplicaAndSurvivesNothing(t *testing.T) {
	loop := runtime.NewLoop(runtime.LoopSpec{
		Name: "core.listener",
		Run:  func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() },
	})

	if err := loop.Start(context.Background()); err != nil {
		t.Fatalf("the loop did not start: %v", err)
	}
	t.Cleanup(func() { _ = loop.Stop(context.Background()) })

	declared := loop.State().Declaration
	if declared.Placement != runtime.PerReplica || declared.Durability != runtime.NonDurable {
		t.Fatalf("a component loop declares %+v; every replica runs its own and an interrupted one is lost", declared)
	}
}
