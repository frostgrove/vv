package jobs

import (
	"context"
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
		Overlap: AllowOverlap,
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

func TestSchedulerUsesCollapseForNoOverlapAndSkipsFutureOneShot(t *testing.T) {
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
		Overlap:  AllowOverlap,
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
	if len(placements) != 1 || placements[0].Mode() != PlacementCollapse {
		t.Fatalf("placements = %#v", placements)
	}
}
