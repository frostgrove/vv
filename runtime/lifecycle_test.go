package runtime_test

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/frostgrove/vv/runtime"
)

type dualObserver struct {
	state     func(runtime.RunnerState)
	lifecycle func(context.Context, runtime.LifecycleEvent)
}

func (o *dualObserver) Observed(state runtime.RunnerState) {
	if o.state != nil {
		o.state(state)
	}
}

func (o *dualObserver) ObservedLifecycle(ctx context.Context, event runtime.LifecycleEvent) {
	if o.lifecycle != nil {
		o.lifecycle(ctx, event)
	}
}

type lifecycleObservation struct {
	ctx   context.Context
	event runtime.LifecycleEvent
}

type lifecycleRecorder struct {
	mu       sync.Mutex
	states   []runtime.RunnerState
	events   []lifecycleObservation
	sequence []string
	notified chan struct{}
}

func newLifecycleRecorder() *lifecycleRecorder {
	return &lifecycleRecorder{notified: make(chan struct{}, 16)}
}

func (r *lifecycleRecorder) Observed(state runtime.RunnerState) {
	r.mu.Lock()
	r.states = append(r.states, state)
	r.sequence = append(r.sequence, "state:"+string(state.Phase))
	r.mu.Unlock()
	r.notified <- struct{}{}
}

func (r *lifecycleRecorder) ObservedLifecycle(ctx context.Context, event runtime.LifecycleEvent) {
	r.mu.Lock()
	r.events = append(r.events, lifecycleObservation{ctx: ctx, event: event})
	operation := "run"
	if event.Operation() == runtime.LifecycleOperationDrain {
		operation = "drain"
	}
	r.sequence = append(r.sequence, "lifecycle:"+operation)
	r.mu.Unlock()
	r.notified <- struct{}{}
}

func (r *lifecycleRecorder) snapshot() ([]runtime.RunnerState, []lifecycleObservation, []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]runtime.RunnerState(nil), r.states...), append([]lifecycleObservation(nil), r.events...), append([]string(nil), r.sequence...)
}

type declaredDrainingWorker struct {
	*drainingWorker
	declaration runtime.Declaration
}

func (w *declaredDrainingWorker) Declaration() runtime.Declaration { return w.declaration }

type declaredWorker struct {
	*worker
	declaration runtime.Declaration
}

func (w *declaredWorker) Declaration() runtime.Declaration { return w.declaration }

func TestObserversPreserveOrderAndLifecycleCapability(t *testing.T) {
	var mu sync.Mutex
	var order []string
	appendOrder := func(value string) {
		mu.Lock()
		order = append(order, value)
		mu.Unlock()
	}
	first := &dualObserver{
		state:     func(runtime.RunnerState) { appendOrder("first/state") },
		lifecycle: func(context.Context, runtime.LifecycleEvent) { appendOrder("first/lifecycle") },
	}
	panicking := &dualObserver{
		state: func(runtime.RunnerState) {
			appendOrder("panicking/state")
			panic("state observer failed")
		},
		lifecycle: func(context.Context, runtime.LifecycleEvent) {
			appendOrder("panicking/lifecycle")
			panic("lifecycle observer failed")
		},
	}
	last := &dualObserver{
		state:     func(runtime.RunnerState) { appendOrder("last/state") },
		lifecycle: func(context.Context, runtime.LifecycleEvent) { appendOrder("last/lifecycle") },
	}
	lifecycleOnly := runtime.LifecycleObserverFunc(func(context.Context, runtime.LifecycleEvent) {
		appendOrder("func/lifecycle")
	})

	combined, err := runtime.Observers(first, panicking, last, lifecycleOnly)
	if err != nil || combined == nil {
		t.Fatalf("Observers returned observer=%v err=%v", combined, err)
	}
	combined.Observed(runtime.RunnerState{Phase: runtime.PhaseRunning})
	lifecycle, ok := combined.(runtime.LifecycleObserver)
	if !ok {
		t.Fatal("fan-out lost LifecycleObserver capability")
	}
	lifecycle.ObservedLifecycle(context.Background(), runtime.LifecycleEvent{})

	want := []string{
		"first/state", "panicking/state", "last/state",
		"first/lifecycle", "panicking/lifecycle", "last/lifecycle", "func/lifecycle",
	}
	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("callback order=%v, want %v", order, want)
	}
}

func TestObserversHaveBoundedInertAndMustForms(t *testing.T) {
	empty, err := runtime.Observers()
	if err != nil || empty == nil {
		t.Fatalf("empty fan-out observer=%v err=%v", empty, err)
	}
	if _, ok := empty.(runtime.LifecycleObserver); !ok {
		t.Fatal("empty fan-out lost LifecycleObserver capability")
	}
	empty.Observed(runtime.RunnerState{})

	tooMany := make([]runtime.Observer, runtime.MaxObservers+1)
	if got, err := runtime.Observers(tooMany...); got != nil || err != runtime.ErrTooManyObservers {
		t.Fatalf("over-limit result observer=%v err=%v", got, err)
	}
	var recovered any
	func() {
		defer func() { recovered = recover() }()
		runtime.MustObservers(tooMany...)
	}()
	if recovered != runtime.ErrTooManyObservers {
		t.Fatalf("MustObservers panic=%v", recovered)
	}
}

func TestSupervisorReportsNormalRunAndDrainAfterStateTransition(t *testing.T) {
	type contextKey string
	runContexts := make(chan context.Context, 1)
	drainContexts := make(chan context.Context, 1)
	recorder := newLifecycleRecorder()
	declaration := runtime.Declaration{Placement: runtime.Singleton, Durability: runtime.Durable}
	runner := &declaredDrainingWorker{
		drainingWorker: &drainingWorker{worker: worker{
			name: "queue-secret-name",
			run: func(ctx context.Context) error {
				runContexts <- ctx
				<-ctx.Done()
				return ctx.Err()
			},
			drain: func(ctx context.Context) error {
				drainContexts <- ctx
				return nil
			},
		}},
		declaration: declaration,
	}
	supervised := supervisor(t, runtime.Spec{Runners: []runtime.Runner{runner}, Observer: recorder})
	startContext := context.WithValue(context.Background(), contextKey("run"), "preserved")
	if err := supervised.Start(startContext); err != nil {
		t.Fatal(err)
	}
	runContext := <-runContexts
	if runContext.Value(contextKey("run")) != "preserved" {
		t.Fatal("runner context lost the Start values")
	}
	drainContext := context.WithValue(context.Background(), contextKey("drain"), "preserved")
	if err := supervised.Stop(drainContext); err != nil {
		t.Fatal(err)
	}
	passedDrainContext := <-drainContexts

	states, events, sequence := recorder.snapshot()
	if len(states) != 2 || states[0].Phase != runtime.PhaseRunning || states[1].Phase != runtime.PhaseStopped {
		t.Fatalf("states=%+v", states)
	}
	if len(events) != 2 {
		t.Fatalf("lifecycle events=%+v", events)
	}
	var runEvent, drainEvent lifecycleObservation
	for _, observation := range events {
		switch observation.event.Operation() {
		case runtime.LifecycleOperationRun:
			runEvent = observation
		case runtime.LifecycleOperationDrain:
			drainEvent = observation
		}
	}
	if runEvent.ctx != runContext || runEvent.event.Outcome() != runtime.LifecycleOutcomeOK || runEvent.event.Err() != nil || runEvent.event.Declaration() != declaration || runEvent.event.Elapsed() < 0 {
		t.Fatalf("run event context_equal=%t event=%+v", runEvent.ctx == runContext, runEvent.event)
	}
	if drainEvent.ctx != passedDrainContext || drainEvent.event.Outcome() != runtime.LifecycleOutcomeOK || drainEvent.event.Err() != nil || drainEvent.event.Declaration() != declaration || drainEvent.event.Elapsed() < 0 {
		t.Fatalf("drain event context_equal=%t event=%+v", drainEvent.ctx == passedDrainContext, drainEvent.event)
	}
	stoppedIndex, runIndex := -1, -1
	for index, item := range sequence {
		if item == "state:stopped" {
			stoppedIndex = index
		}
		if item == "lifecycle:run" {
			runIndex = index
		}
	}
	if stoppedIndex < 0 || runIndex <= stoppedIndex {
		t.Fatalf("terminal state was not observed before run completion: %v", sequence)
	}
}

func TestSupervisorReportsEarlyRunErrorWithoutChangingIt(t *testing.T) {
	wantErr := errors.New("private backend failure")
	recorder := newLifecycleRecorder()
	declaration := runtime.Declaration{Placement: runtime.PerReplica, Durability: runtime.NonDurable}
	runner := &declaredWorker{
		worker:      &worker{name: "worker-private-name", run: func(context.Context) error { return wantErr }},
		declaration: declaration,
	}
	supervised := supervisor(t, runtime.Spec{Runners: []runtime.Runner{runner}, Observer: recorder})
	if err := supervised.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(2 * time.Second)
	for {
		_, events, _ := recorder.snapshot()
		if len(events) > 0 {
			event := events[0].event
			if event.Operation() != runtime.LifecycleOperationRun || event.Outcome() != runtime.LifecycleOutcomeError || event.Err() != wantErr || event.Declaration() != declaration {
				t.Fatalf("early failure event=%+v", event)
			}
			state := stateOf(t, supervised, runner.Name())
			if state.Phase != runtime.PhaseFailed || state.Err != wantErr {
				t.Fatalf("terminal state=%+v", state)
			}
			break
		}
		select {
		case <-recorder.notified:
		case <-deadline:
			t.Fatal("early run completion was not observed")
		}
	}
	if err := supervised.Stop(context.Background()); err != nil {
		t.Fatalf("stopping an already failed runner changed behavior: %v", err)
	}
}

func TestSupervisorReportsUnexpectedNilAsRunnerReturned(t *testing.T) {
	recorder := newLifecycleRecorder()
	runner := &worker{name: "returned", run: func(context.Context) error { return nil }}
	supervised := supervisor(t, runtime.Spec{Runners: []runtime.Runner{runner}, Observer: recorder})
	if err := supervised.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(2 * time.Second)
	for {
		_, events, _ := recorder.snapshot()
		if len(events) > 0 {
			event := events[0].event
			if event.Outcome() != runtime.LifecycleOutcomeError || !errors.Is(event.Err(), runtime.ErrRunnerReturned) {
				t.Fatalf("unexpected return event=%+v err=%v", event, event.Err())
			}
			break
		}
		select {
		case <-recorder.notified:
		case <-deadline:
			t.Fatal("unexpected return was not observed")
		}
	}
	if err := supervised.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestSupervisorReportsExactDrainFailure(t *testing.T) {
	wantErr := errors.New("private drain failure")
	recorder := newLifecycleRecorder()
	runner := &drainingWorker{worker: worker{
		name: "draining",
		drain: func(context.Context) error {
			return wantErr
		},
	}}
	supervised := supervisor(t, runtime.Spec{Runners: []runtime.Runner{runner}, Observer: recorder})
	if err := supervised.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	stopErr := supervised.Stop(context.Background())
	if !errors.Is(stopErr, wantErr) {
		t.Fatalf("Stop error=%v", stopErr)
	}
	_, events, _ := recorder.snapshot()
	for _, observation := range events {
		if observation.event.Operation() != runtime.LifecycleOperationDrain {
			continue
		}
		if observation.event.Outcome() != runtime.LifecycleOutcomeError || observation.event.Err() != wantErr {
			t.Fatalf("drain event=%+v err=%v", observation.event, observation.event.Err())
		}
		return
	}
	t.Fatal("drain completion was not observed")
}
