package jobs

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type scheduleObserverRecord struct {
	ctx   context.Context
	event ScheduleEvent
}

type scheduleObserverRecorder struct {
	mu      sync.Mutex
	records []scheduleObserverRecord
}

func (recorder *scheduleObserverRecorder) Observe(ctx context.Context, event ScheduleEvent) {
	recorder.mu.Lock()
	recorder.records = append(recorder.records, scheduleObserverRecord{ctx: ctx, event: event})
	recorder.mu.Unlock()
}

func (recorder *scheduleObserverRecorder) snapshot() []scheduleObserverRecord {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return append([]scheduleObserverRecord(nil), recorder.records...)
}

func TestScheduleObserversBoundFilteringOrderAndPanicIsolation(t *testing.T) {
	wantErr := errors.New("terminal schedule failure")
	event := ScheduleEvent{
		result:  ScheduleRunResult{Due: 1, Existing: 1},
		err:     wantErr,
		elapsed: 3 * time.Millisecond,
	}
	ctx := context.WithValue(t.Context(), struct{}{}, "kept")
	var order []string
	first := ScheduleObserverFunc(func(got context.Context, observed ScheduleEvent) {
		order = append(order, "first")
		if got != ctx || observed.Result() != event.Result() || observed.Err() != wantErr || observed.Elapsed() != event.Elapsed() {
			t.Fatalf("first observation = (%p, %#v, %v, %s)", got, observed.Result(), observed.Err(), observed.Elapsed())
		}
		copy := observed.Result()
		copy.Due = MaxDefinitions
	})
	panicking := ScheduleObserverFunc(func(context.Context, ScheduleEvent) {
		order = append(order, "panicking")
		panic("private observer panic")
	})
	last := ScheduleObserverFunc(func(_ context.Context, observed ScheduleEvent) {
		order = append(order, "last")
		if observed.Result() != event.Result() {
			t.Fatalf("event was mutable through an earlier accessor: %#v", observed.Result())
		}
	})
	var typedNil *scheduleObserverRecorder
	var typedNilFunc ScheduleObserverFunc
	combined, err := ScheduleObservers(nil, typedNil, first, panicking, typedNilFunc, last, nil, nil)
	if err != nil || nilInterface(combined) {
		t.Fatalf("ScheduleObservers = (%T, %v)", combined, err)
	}
	combined.Observe(ctx, event)
	wantOrder := []string{"first", "panicking", "last"}
	if len(order) != len(wantOrder) {
		t.Fatalf("observer order = %v", order)
	}
	for index := range wantOrder {
		if order[index] != wantOrder[index] {
			t.Fatalf("observer order = %v", order)
		}
	}

	empty, err := ScheduleObservers()
	if err != nil || nilInterface(empty) {
		t.Fatalf("empty fan-out = (%T, %v), want non-nil inert observer", empty, err)
	}
	empty.Observe(ctx, event)

	tooMany := make([]ScheduleObserver, MaxScheduleObservers+1)
	if observer, err := ScheduleObservers(tooMany...); observer != nil || !errors.Is(err, ErrTooLarge) {
		t.Fatalf("nine filtered observers = (%T, %v), want nil and ErrTooLarge", observer, err)
	}
	defer func() {
		recovered := recover()
		err, ok := recovered.(error)
		if !ok || !errors.Is(err, ErrTooLarge) {
			t.Fatalf("MustScheduleObservers panic = %v", recovered)
		}
	}()
	_ = MustScheduleObservers(tooMany...)
}

type observedScheduleSender struct {
	description BackendDescription
	mu          sync.Mutex
	calls       int
	failAt      int
	failure     error
}

func (sender *observedScheduleSender) Description() BackendDescription { return sender.description }

func (sender *observedScheduleSender) Place(_ context.Context, placement Placement) (PlacementResult, error) {
	sender.mu.Lock()
	sender.calls++
	call := sender.calls
	sender.mu.Unlock()
	if call == sender.failAt {
		return PlacementResult{}, sender.failure
	}
	return NewPlacementResult(placement.Candidate(), PlacementCreated)
}

type observedScheduleClock struct {
	now   time.Time
	delay time.Duration
	calls atomic.Int32
}

func (clock *observedScheduleClock) Now() time.Time {
	clock.calls.Add(1)
	time.Sleep(clock.delay)
	return clock.now
}

func (*observedScheduleClock) NewTimerAt(time.Time) Timer { panic("unexpected timer") }

func TestSchedulerRunDueEmitsOneExactTerminalEventForEveryAdmittedReturn(t *testing.T) {
	base := time.Date(2036, 2, 3, 4, 5, 6, 0, time.UTC)
	tests := []struct {
		name       string
		count      int
		context    func() context.Context
		now        time.Time
		failAt     int
		failure    error
		wantResult ScheduleRunResult
		wantErr    error
	}{
		{
			name:       "success",
			count:      1,
			context:    context.Background,
			now:        base,
			wantResult: ScheduleRunResult{Due: 1, Placed: 1},
		},
		{
			name:    "pre-canceled context",
			count:   1,
			context: func() context.Context { ctx, cancel := context.WithCancel(context.Background()); cancel(); return ctx },
			now:     base,
			wantErr: context.Canceled,
		},
		{
			name:    "clock failure",
			count:   1,
			context: context.Background,
			wantErr: ErrInvalid,
		},
		{
			name:       "partial placement failure",
			count:      2,
			context:    context.Background,
			now:        base,
			failAt:     2,
			failure:    RejectPlacement(ErrSaturated),
			wantResult: ScheduleRunResult{Due: 2, Placed: 1},
			wantErr:    ErrSaturated,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := &scheduleObserverRecorder{}
			clock := &observedScheduleClock{now: test.now}
			sender := &observedScheduleSender{description: queueTestBackendDescription(31), failAt: test.failAt, failure: test.failure}
			scheduler := newObservedScheduler(t, recorder, clock, sender, base, test.count)
			ctx := test.context()
			result, err := scheduler.RunDue(ctx)
			if result != test.wantResult || !errors.Is(err, test.wantErr) || (test.wantErr == nil && err != nil) {
				t.Fatalf("RunDue = (%#v, %v), want (%#v, %v)", result, err, test.wantResult, test.wantErr)
			}
			records := recorder.snapshot()
			if len(records) != 1 {
				t.Fatalf("terminal events = %d, want one", len(records))
			}
			observed := records[0]
			if observed.ctx != ctx || observed.event.Result() != result || observed.event.Err() != err || observed.event.Elapsed() < 0 {
				t.Fatalf("terminal event = (%p, %#v, %v, %s)", observed.ctx, observed.event.Result(), observed.event.Err(), observed.event.Elapsed())
			}
		})
	}
}

func TestSchedulerRunDueElapsedCoversTheClockBoundary(t *testing.T) {
	recorder := &scheduleObserverRecorder{}
	clock := &observedScheduleClock{delay: 15 * time.Millisecond}
	sender := &observedScheduleSender{description: queueTestBackendDescription(34)}
	scheduler := newObservedScheduler(t, recorder, clock, sender, time.Now().UTC(), 1)

	if _, err := scheduler.RunDue(context.Background()); !errors.Is(err, ErrInvalid) {
		t.Fatalf("RunDue error = %v", err)
	}
	records := recorder.snapshot()
	if len(records) != 1 || records[0].event.Elapsed() < clock.delay {
		t.Fatalf("elapsed = %v, want at least %v", records, clock.delay)
	}
}

func TestSchedulerRunUsesRunDueTerminalObservation(t *testing.T) {
	base := time.Date(2036, 2, 3, 4, 5, 6, 0, time.UTC)
	ctx, cancel := context.WithCancel(context.Background())
	recorder := &scheduleObserverRecorder{}
	observer := MustScheduleObservers(recorder, ScheduleObserverFunc(func(context.Context, ScheduleEvent) { cancel() }))
	clock := &observedScheduleClock{now: base}
	sender := &observedScheduleSender{description: queueTestBackendDescription(35)}
	scheduler := newObservedScheduler(t, observer, clock, sender, base, 1)

	if err := scheduler.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v", err)
	}
	records := recorder.snapshot()
	if len(records) != 1 || records[0].ctx != ctx || records[0].event.Result() != (ScheduleRunResult{Due: 1, Placed: 1}) || records[0].event.Err() != nil {
		t.Fatalf("Run terminal observations = %#v", records)
	}
}

type nilScheduleContext struct{}

func (*nilScheduleContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (*nilScheduleContext) Done() <-chan struct{}       { return nil }
func (*nilScheduleContext) Err() error                  { return nil }
func (*nilScheduleContext) Value(any) any               { return nil }

func TestSchedulerRunDuePreAdmissionConflictsEmitNothing(t *testing.T) {
	recorder := &scheduleObserverRecorder{}
	clock := &observedScheduleClock{now: time.Date(2036, 2, 3, 4, 5, 6, 0, time.UTC)}
	sender := &observedScheduleSender{description: queueTestBackendDescription(32)}
	scheduler := newObservedScheduler(t, recorder, clock, sender, clock.now, 1)
	var typedNil *nilScheduleContext
	for _, ctx := range []context.Context{nil, typedNil} {
		if result, err := scheduler.RunDue(ctx); result != (ScheduleRunResult{}) || err != ErrConflict {
			t.Fatalf("RunDue(%T) = (%#v, %v)", ctx, result, err)
		}
	}
	scheduler.cycle.Store(true)
	if result, err := scheduler.RunDue(context.Background()); result != (ScheduleRunResult{}) || err != ErrConflict {
		t.Fatalf("conflicting RunDue = (%#v, %v)", result, err)
	}
	scheduler.cycle.Store(false)
	var nilScheduler *Scheduler
	if result, err := nilScheduler.RunDue(context.Background()); result != (ScheduleRunResult{}) || err != ErrConflict {
		t.Fatalf("nil RunDue = (%#v, %v)", result, err)
	}
	if records := recorder.snapshot(); len(records) != 0 {
		t.Fatalf("pre-admission terminal events = %d", len(records))
	}
	if clock.calls.Load() != 0 {
		t.Fatalf("pre-admission clock calls = %d", clock.calls.Load())
	}
}

func TestSchedulerObserverRunsBeforeGuardReleaseAndCannotChangeTheResult(t *testing.T) {
	base := time.Date(2036, 2, 3, 4, 5, 6, 0, time.UTC)
	clock := &observedScheduleClock{now: base}
	sender := &observedScheduleSender{description: queueTestBackendDescription(33)}
	var scheduler *Scheduler
	var calls int
	observer := ScheduleObserverFunc(func(ctx context.Context, _ ScheduleEvent) {
		calls++
		result, err := scheduler.RunDue(ctx)
		if result != (ScheduleRunResult{}) || err != ErrConflict {
			t.Fatalf("reentrant RunDue = (%#v, %v), want pre-admission conflict", result, err)
		}
		panic("private observer panic")
	})
	scheduler = newObservedScheduler(t, observer, clock, sender, base, 1)
	result, err := scheduler.RunDue(context.Background())
	if err != nil || result != (ScheduleRunResult{Due: 1, Placed: 1}) || calls != 1 {
		t.Fatalf("first RunDue = (%#v, %v), observer calls=%d", result, err, calls)
	}
	result, err = scheduler.RunDue(context.Background())
	if err != nil || result != (ScheduleRunResult{}) || calls != 2 {
		t.Fatalf("second RunDue = (%#v, %v), observer calls=%d", result, err, calls)
	}
}

func newObservedScheduler(t *testing.T, observer ScheduleObserver, clock Clock, sender Sender, due time.Time, count int) *Scheduler {
	t.Helper()
	definition := MustDefine(DefinitionSpec[string]{
		Name:   testJobName(t, "schedule.observer.job"),
		Codec:  String(1),
		Policy: testPolicy(t),
	})
	queue, err := NewQueue(QueueSpec{
		Namespace: queueTestNamespace(t, "schedule-observer"),
		Catalog:   MustCatalog(definition),
		Sender:    sender,
	})
	if err != nil {
		t.Fatal(err)
	}
	schedules := make([]Schedule, count)
	for index := range schedules {
		name := "schedule.observer.run"
		if index == 1 {
			name = "schedule.observer.run.second"
		}
		schedule, err := DefineSchedule(ScheduleSpec[string]{
			Name:     testJobName(t, name),
			Revision: 1,
			Cadence:  At(due),
			Job:      definition,
			Payload:  func(time.Time) (string, error) { return "payload", nil },
		})
		if err != nil {
			t.Fatal(err)
		}
		schedules[index] = schedule
	}
	scheduler, err := NewScheduler(SchedulerSpec{Queue: queue, Clock: clock, Observer: observer}, schedules...)
	if err != nil {
		t.Fatal(err)
	}
	return scheduler
}
