package jobs

import (
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

type scheduleSender struct {
	description BackendDescription
	mu          sync.Mutex
	placements  []Placement
}

func (sender *scheduleSender) Description() BackendDescription { return sender.description }

func (sender *scheduleSender) Place(_ context.Context, placement Placement) (PlacementResult, error) {
	sender.mu.Lock()
	sender.placements = append(sender.placements, placement)
	sender.mu.Unlock()
	return NewPlacementResult(placement.Candidate(), PlacementCreated)
}

type scheduleClock struct{ now time.Time }

func (clock scheduleClock) Now() time.Time { return clock.now }
func (scheduleClock) NewTimerAt(time.Time) Timer {
	panic("unexpected timer")
}

func TestSchedulerRunDuePlacesTypedOccurrence(t *testing.T) {
	definition := MustDefine(DefinitionSpec[string]{
		Name:   testJobName(t, "maintenance.collect"),
		Codec:  String(1),
		Policy: testPolicy(t),
	})
	catalog := MustCatalog(definition)
	sender := &scheduleSender{description: queueTestBackendDescription(1)}
	queue, err := NewQueue(QueueSpec{
		Namespace: queueTestNamespace(t, "scheduler"),
		Catalog:   catalog,
		Sender:    sender,
	})
	if err != nil {
		t.Fatal(err)
	}
	anchor := time.Date(2035, 1, 2, 3, 0, 0, 0, time.UTC)
	schedule, err := DefineSchedule(ScheduleSpec[string]{
		Name:     testJobName(t, "maintenance.collect.hourly"),
		Revision: 1,
		Cadence:  FixedEvery(time.Hour, Anchor(anchor)),
		Job:      definition,
		Payload: func(due time.Time) (string, error) {
			return due.Format(time.RFC3339), nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	now := anchor.Add(2*time.Hour + 20*time.Minute)
	scheduler, err := NewScheduler(SchedulerSpec{Queue: queue, Clock: scheduleClock{now: now}}, schedule)
	if err != nil {
		t.Fatal(err)
	}
	result, err := scheduler.RunDue(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result != (ScheduleRunResult{Due: 1, Placed: 1}) {
		t.Fatalf("run result = %#v", result)
	}
	sender.mu.Lock()
	placements := append([]Placement(nil), sender.placements...)
	sender.mu.Unlock()
	if len(placements) != 1 || placements[0].Mode() != PlacementOnce {
		t.Fatalf("placements = %#v", placements)
	}
	payload, err := definition.Decode(placements[0].Payload())
	if err != nil || payload != anchor.Add(2*time.Hour).Format(time.RFC3339) {
		t.Fatalf("payload = (%q, %v)", payload, err)
	}
}

func TestSchedulerRunDueContainsPayloadPanic(t *testing.T) {
	definition := MustDefine(DefinitionSpec[string]{
		Name:   testJobName(t, "maintenance.panicking"),
		Codec:  String(1),
		Policy: testPolicy(t),
	})
	sender := &scheduleSender{description: queueTestBackendDescription(1)}
	queue, err := NewQueue(QueueSpec{
		Namespace: queueTestNamespace(t, "scheduler-panic"),
		Catalog:   MustCatalog(definition),
		Sender:    sender,
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2035, 1, 2, 3, 0, 0, 0, time.UTC)
	schedule, err := DefineSchedule(ScheduleSpec[string]{
		Name:     testJobName(t, "maintenance.panicking.hourly"),
		Revision: 1,
		Cadence:  At(now),
		Job:      definition,
		Payload:  func(time.Time) (string, error) { panic("private payload panic") },
	})
	if err != nil {
		t.Fatal(err)
	}
	scheduler, err := NewScheduler(SchedulerSpec{Queue: queue, Clock: scheduleClock{now: now}}, schedule)
	if err != nil {
		t.Fatal(err)
	}
	result, err := scheduler.RunDue(context.Background())
	if !errors.Is(err, ErrInvalid) || strings.Contains(err.Error(), "private") {
		t.Fatalf("panic result = (%#v, %v)", result, err)
	}
	if result != (ScheduleRunResult{Due: 1}) {
		t.Fatalf("run result = %#v", result)
	}
	sender.mu.Lock()
	placements := len(sender.placements)
	sender.mu.Unlock()
	if placements != 0 {
		t.Fatalf("placements = %d", placements)
	}
}

// A scheduled run may overlap the previous one, and there is no option that says
// otherwise — SkipOverlap was a public constant DefineSchedule refused every time,
// so the surface named a capability that did not exist and is gone.
func TestAScheduledRunMayOverlapThePreviousOne(t *testing.T) {
	definition := MustDefine(DefinitionSpec[string]{
		Name:   testJobName(t, "maintenance.sweep"),
		Codec:  String(1),
		Policy: testPolicy(t),
	})
	catalog := MustCatalog(definition)
	sender := &scheduleSender{description: queueTestBackendDescription(1)}
	queue, err := NewQueue(QueueSpec{Namespace: queueTestNamespace(t, "scheduler-overlap"), Catalog: catalog, Sender: sender})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2035, 2, 3, 4, 0, 0, 0, time.UTC)
	due, err := DefineSchedule(ScheduleSpec[string]{
		Name:     testJobName(t, "maintenance.sweep.due"),
		Revision: 1,
		Cadence:  At(now.Add(-time.Minute)),
		Job:      definition,
		Payload:  func(time.Time) (string, error) { return "due", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	future, err := DefineSchedule(ScheduleSpec[string]{
		Name:     testJobName(t, "maintenance.sweep.future"),
		Revision: 1,
		Cadence:  At(now.Add(time.Hour)),
		Job:      definition,
		Payload:  func(time.Time) (string, error) { return "future", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	scheduler, err := NewScheduler(SchedulerSpec{Queue: queue, Clock: scheduleClock{now: now}}, due, future)
	if err != nil {
		t.Fatal(err)
	}
	result, err := scheduler.RunDue(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result != (ScheduleRunResult{Due: 1, Placed: 1}) {
		t.Fatalf("run result = %#v", result)
	}
	sender.mu.Lock()
	placements := append([]Placement(nil), sender.placements...)
	sender.mu.Unlock()
	if len(placements) != 1 || placements[0].Mode() != PlacementOnce {
		t.Fatalf("placements = %#v", placements)
	}
}

// The scheduler places with EnqueueOnce and nothing else, and EnqueueOnce needs a
// payload identity. A schedule over a job without one was accepted at wiring and
// then failed at every run, on the scheduler's goroutine, long after the
// deployment stopped looking at its wiring.
func TestASchedulesJobMustBePlaceableByTheSchedulersOnlyPath(t *testing.T) {
	now := time.Date(2035, 2, 3, 4, 0, 0, 0, time.UTC)
	spec := func(job DefinitionOf[string], name string) ScheduleSpec[string] {
		return ScheduleSpec[string]{
			Name:     testJobName(t, name),
			Revision: 1,
			Cadence:  At(now),
			Job:      job,
			Payload:  func(time.Time) (string, error) { return "x", nil },
		}
	}

	unplaceable := MustDefine(DefinitionSpec[string]{
		Name:   testJobName(t, "maintenance.trusted"),
		Codec:  TrustedJSON[string](1),
		Policy: testPolicy(t),
	})
	if unplaceable.PayloadIdentity().Available {
		t.Fatal("this codec does supply an identity, so the refusal below proves nothing")
	}
	if _, err := DefineSchedule(spec(unplaceable, "maintenance.trusted.hourly")); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("err = %v, want ErrUnsupported — a schedule was built whose every run fails at enqueue", err)
	}

	// The control, and the way out: an identity supplied on the definition rather
	// than inferred from the codec makes the same job schedulable.
	placeable := MustDefine(DefinitionSpec[string]{
		Name:     testJobName(t, "maintenance.identified"),
		Codec:    TrustedJSON[string](1),
		Identity: scheduleStringIdentity{},
		Policy:   testPolicy(t),
	})
	if _, err := DefineSchedule(spec(placeable, "maintenance.identified.hourly")); err != nil {
		t.Fatalf("a job carrying its own payload identity was refused: %v", err)
	}
}

type scheduleStringIdentity struct{}

func (scheduleStringIdentity) ID() CodecID {
	id, err := ParseCodecID("schedule-test-identity")
	if err != nil {
		panic(err)
	}
	return id
}
func (scheduleStringIdentity) Version() SchemaVersion { return 1 }
func (scheduleStringIdentity) Digest(value string, _ PayloadLimit) ([32]byte, error) {
	return sha256.Sum256([]byte(value)), nil
}

// The only thing stopping one occurrence firing twice is the EnqueueOnce intent
// it is keyed on, and that intent is swept on the job's own IntentRetention — a
// value chosen for deduplicating producers, with no relation to the cadence.
func TestAScheduleWhoseIntentIsSweptBeforeItsPeriodIsRefused(t *testing.T) {
	shortLived := MustDefine(DefinitionSpec[string]{
		Name:   testJobName(t, "maintenance.short-intent"),
		Codec:  String(1),
		Policy: testPolicy(t, RetainFor(time.Minute), RetainIntentsFor(time.Minute)),
	})
	_, err := DefineSchedule(ScheduleSpec[string]{
		Name: testJobName(t, "maintenance.short-intent.hourly"), Revision: 1,
		Cadence: FixedEvery(time.Hour), Job: shortLived,
		Payload: func(time.Time) (string, error) { return "x", nil },
	})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("err = %v, want ErrUnsupported — an occurrence would be placed more than once", err)
	}

	// The control: a retention that outlives the period is accepted, so the
	// refusal above is the comparison rather than intervals being refused.
	longLived := MustDefine(DefinitionSpec[string]{
		Name:   testJobName(t, "maintenance.long-intent"),
		Codec:  String(1),
		Policy: testPolicy(t, RetainFor(time.Hour), RetainIntentsFor(2*time.Hour)),
	})
	if _, err := DefineSchedule(ScheduleSpec[string]{
		Name: testJobName(t, "maintenance.long-intent.hourly"), Revision: 1,
		Cadence: FixedEvery(time.Hour), Job: longLived,
		Payload: func(time.Time) (string, error) { return "x", nil },
	}); err != nil {
		t.Fatalf("a cadence its intent outlives was refused: %v", err)
	}
}

// An At occurrence never advances, so once it is past it is due on every cycle
// forever and the intent is the only thing refusing it. A running scheduler
// refuses it locally too, so a swept intent does not turn a run-once schedule
// into a run-every-retention-period one.
func TestARunOnceScheduleIsPlacedOnceWhileTheSchedulerRuns(t *testing.T) {
	definition := MustDefine(DefinitionSpec[string]{
		Name:   testJobName(t, "maintenance.once"),
		Codec:  String(1),
		Policy: testPolicy(t),
	})
	sender := &scheduleSender{description: queueTestBackendDescription(1)}
	queue, err := NewQueue(QueueSpec{Namespace: queueTestNamespace(t, "scheduler-once"), Catalog: MustCatalog(definition), Sender: sender})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2035, 2, 3, 4, 0, 0, 0, time.UTC)
	once, err := DefineSchedule(ScheduleSpec[string]{
		Name: testJobName(t, "maintenance.once.at"), Revision: 1,
		Cadence: At(now.Add(-time.Minute)), Job: definition,
		Payload: func(time.Time) (string, error) { return "once", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	clock := scheduleClock{now: now}
	scheduler, err := NewScheduler(SchedulerSpec{Queue: queue, Clock: clock}, once)
	if err != nil {
		t.Fatal(err)
	}

	first, err := scheduler.RunDue(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if first.Due != 1 || first.Placed != 1 {
		t.Fatalf("first cycle = %#v", first)
	}
	second, err := scheduler.RunDue(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if second.Due != 0 {
		t.Fatalf("second cycle = %#v — the same occurrence came round again", second)
	}
}
