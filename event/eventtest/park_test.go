package eventtest_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/frostgrove/vv/event/eventtest"
	"github.com/frostgrove/vv/event/projection"
)

// The queue is the application's table and its four methods run at three
// different tiers: Sequences outside a unit of work, Park and Holds inside one,
// and Holds outside one as well, because a wait asks it on a request path's own
// goroutine. RunPark over the reference is what says the harness certifies a
// queue written that way.
func TestTheReferenceQueueIsCertified(t *testing.T) {
	eventtest.RunPark(t, parkFactory(t, newMemoryPark().bounded(3, 2), nil))
}

// A factory that declares neither bound is told so rather than passed for it,
// and the control is the same implementation under a factory that declares them.
func TestAnUndeclaredBoundIsDeclinedRatherThanCertified(t *testing.T) {
	factory := parkFactory(t, newMemoryPark(), nil)
	verdicts := eventtest.CertifyPark(t, factory)
	if err := reported(eventtest.ParkSectionNames(), verdicts); err != nil {
		t.Fatal(err)
	}
	for _, given := range verdicts {
		if given.Section != "bounds" {
			continue
		}
		if given.Word != "not certified" {
			t.Fatalf("a factory declaring neither bound was reported %q for the bounds section, and a bound nobody stated is not one this harness may drive", given.Word)
		}
		if given.Reason == "" {
			t.Fatal("the bounds section was declined and said nothing about why")
		}
	}
	if certified := eventtest.Certified(verdicts); certified != 5 {
		t.Fatalf("a factory declaring neither bound certified %d sections where five of the six do not depend on one", certified)
	}
	if certified := eventtest.Certified(eventtest.CertifyPark(t, parkFactory(t, newMemoryPark().bounded(3, 2), nil))); certified != 6 {
		t.Fatalf("the same queue under declared bounds certified %d of six, so the decline above is not the declaration's", certified)
	}
}

func TestTheQueueHarnessStillDetectsEveryDefectItWasBuiltToDetect(t *testing.T) {
	named := map[string]bool{}
	sections := map[string]bool{}
	for _, name := range eventtest.ParkSectionNames() {
		sections[name] = true
	}
	for _, defect := range eventtest.ParkDefects() {
		if named[defect.Name] {
			t.Fatalf("two queue defects are named %q, so one of them is proving the other's section", defect.Name)
		}
		named[defect.Name] = true
		if !sections[defect.Section] {
			t.Fatalf("the queue defect %q names the %s section, which this harness does not have", defect.Name, defect.Section)
		}

		plain, broken := parkFactoriesFor(t, defect)
		if word := oneParkVerdict(t, broken, defect.Section); word != "failed" {
			t.Errorf("the harness no longer detects a queue that %s: its %s section was reported %q", defect.Name, defect.Section, word)
		}
		if word := oneParkVerdict(t, plain, defect.Section); word != "passed" {
			t.Errorf("the same queue without the defect that %s was reported %q for the %s section, so the failure above is not the defect's", defect.Name, word, defect.Section)
		}
	}
	if len(named) == 0 {
		t.Fatal("the queue harness carries no defects at all, so this run asserted nothing")
	}
}

const parkInventoried = 8

func TestTheQueueDefectInventoryIsTheSizeItSaysItIs(t *testing.T) {
	defects := eventtest.ParkDefects()
	if err := sizedPark(defects); err != nil {
		t.Fatal(err)
	}
	if err := sizedPark(defects[:len(defects)-1]); err == nil {
		t.Fatal("an inventory one row shorter passed the assertion that pins the size, so what is pinned is whatever the slice holds")
	}
}

func sizedPark(defects []eventtest.ParkDefect) error {
	if len(defects) != parkInventoried {
		return fmt.Errorf("the queue harness carries %d defects where its inventory is %d rows", len(defects), parkInventoried)
	}
	return nil
}

func TestEveryQueueSectionIsNamedByADefectThatBreaksIt(t *testing.T) {
	names := eventtest.ParkSectionNames()
	defects := eventtest.ParkDefects()
	if err := guardedPark(names, defects); err != nil {
		t.Fatal(err)
	}
	for _, unguarded := range names {
		kept := []eventtest.ParkDefect{}
		for _, defect := range defects {
			if defect.Section != unguarded {
				kept = append(kept, defect)
			}
		}
		if err := guardedPark(names, kept); err == nil {
			t.Fatalf("an inventory naming no defect of the %s section passed the assertion that every section carries one", unguarded)
		}
	}
}

func guardedPark(names []string, defects []eventtest.ParkDefect) error {
	naming := map[string]int{}
	for _, defect := range defects {
		naming[defect.Section]++
	}
	for _, name := range names {
		if naming[name] == 0 {
			return fmt.Errorf("no defect in this harness's inventory breaks the %s section, so nothing here can tell whether that section still asserts anything", name)
		}
	}
	return nil
}

func TestEveryQueueSectionInTheInventoryWasReported(t *testing.T) {
	names := eventtest.ParkSectionNames()
	if len(names) != 6 {
		t.Fatalf("the queue harness carries %d sections where its inventory names six", len(names))
	}
	if err := reported(names, eventtest.CertifyPark(t, parkFactory(t, newMemoryPark().bounded(3, 2), nil))); err != nil {
		t.Fatal(err)
	}
	shortened := names[:len(names)-1]
	if err := reported(names, eventtest.CertifyPark(t, parkFactory(t, newMemoryPark().bounded(3, 2), nil), shortened...)); err == nil {
		t.Fatalf("a run that never dispatched %q passed the assertion that every section is reported exactly once", names[len(names)-1])
	}
}

func parkFactory(t *testing.T, held *memoryPark, over func(projection.Park) projection.Park) eventtest.ParkFactory {
	t.Helper()
	return eventtest.ParkFactory{
		New: func(*testing.T) projection.Park {
			beside := held.beside()
			if over == nil {
				return beside
			}
			return over(beside)
		},
		Begin: func(_ *testing.T, ctx context.Context, _ projection.Park) (context.Context, eventtest.Tx) {
			return beginUnit(ctx, held.rows)
		},
		Sequences: held.maxSequences,
		Letters:   held.maxLetters,
	}
}

// The three defects that are queues rather than decorators: which tier a
// statement needs, and which handle it runs on.
func parkShaped() map[string]func(held *memoryPark, broken bool) {
	return map[string]func(*memoryPark, bool){
		"requires the caller's unit of work to count its parked sequences":                    func(held *memoryPark, broken bool) { held.needsAUnit = broken },
		"writes its letters on a connection of its own rather than in the caller's unit":      func(held *memoryPark, broken bool) { held.outside = broken },
		"answers the blocking test from the committed state even inside the unit that parked": func(held *memoryPark, broken bool) { held.committedOnly = broken },
	}
}

func parkFactoriesFor(t *testing.T, defect eventtest.ParkDefect) (plain, broken eventtest.ParkFactory) {
	t.Helper()
	if defect.Over != nil {
		return parkFactory(t, newMemoryPark().bounded(3, 2), nil), parkFactory(t, newMemoryPark().bounded(3, 2), defect.Over)
	}
	flag, known := parkShaped()[defect.Name]
	if !known {
		t.Fatalf("the queue defect %q is an implementation rather than a decorator and this test builds none for it", defect.Name)
	}
	sound, wrong := newMemoryPark().bounded(3, 2), newMemoryPark().bounded(3, 2)
	flag(sound, false)
	flag(wrong, true)
	return parkFactory(t, sound, nil), parkFactory(t, wrong, nil)
}

func oneParkVerdict(t *testing.T, factory eventtest.ParkFactory, section string) string {
	t.Helper()
	verdicts := eventtest.CertifyPark(t, factory, section)
	if len(verdicts) != 1 {
		t.Fatalf("running the %s section alone reported %d verdicts", section, len(verdicts))
	}
	return verdicts[0].Word
}
