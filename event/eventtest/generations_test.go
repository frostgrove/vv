package eventtest_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/frostgrove/vv/event/eventtest"
	"github.com/frostgrove/vv/event/projection"
)

// The ownership row carries the obligation stated in capitals on its own
// interface and enforced by nothing: Active is a locking read. RunGenerations
// over the reference is what says the harness certifies one written that way,
// and the defect inventory below is what says the certificate means anything.
func TestTheReferenceOwnershipRowIsCertified(t *testing.T) {
	eventtest.RunGenerations(t, generationsFactory(t, newMemoryGenerations(), nil))
}

// A factory that declares a serializable unit is told what this harness cannot
// measure rather than being passed for it. The control is the same run under the
// declaration it can: one reports six words and the other seven, and neither of
// them is a skip.
func TestASerializableUnitIsDeclinedRatherThanCertified(t *testing.T) {
	factory := generationsFactory(t, newMemoryGenerations(), nil)
	factory.Closes = eventtest.SerializableUnit

	verdicts := eventtest.CertifyGenerations(t, factory)
	if err := reported(eventtest.GenerationsSectionNames(), verdicts); err != nil {
		t.Fatal(err)
	}
	for _, given := range verdicts {
		if given.Section != "locking read" {
			continue
		}
		if given.Word != "not certified" {
			t.Fatalf("a factory declaring a serializable unit was reported %q for the locking read section, and what this harness can measure is the wait", given.Word)
		}
		if given.Reason == "" {
			t.Fatal("the locking read section was declined and said nothing about why, so an implementer reads a run that certified four of five and no reason for the fifth")
		}
	}
	if certified := eventtest.Certified(verdicts); certified != 4 {
		t.Fatalf("a factory declaring a serializable unit certified %d sections where four of the five do not depend on the declaration", certified)
	}
	if certified := eventtest.Certified(eventtest.CertifyGenerations(t, generationsFactory(t, newMemoryGenerations(), nil))); certified != 5 {
		t.Fatalf("the same implementation under the locking declaration certified %d of five, so the decline above is not the declaration's", certified)
	}
}

func TestTheOwnershipHarnessStillDetectsEveryDefectItWasBuiltToDetect(t *testing.T) {
	named := map[string]bool{}
	sections := map[string]bool{}
	for _, name := range eventtest.GenerationsSectionNames() {
		sections[name] = true
	}
	for _, defect := range eventtest.GenerationsDefects() {
		if named[defect.Name] {
			t.Fatalf("two ownership defects are named %q, so one of them is proving the other's section", defect.Name)
		}
		named[defect.Name] = true
		if !sections[defect.Section] {
			t.Fatalf("the ownership defect %q names the %s section, which this harness does not have", defect.Name, defect.Section)
		}

		plain, broken := generationsFactoriesFor(t, defect)
		if word := oneGenerationsVerdict(t, broken, defect.Section); word != "failed" {
			t.Errorf("the harness no longer detects an ownership row that %s: its %s section was reported %q", defect.Name, defect.Section, word)
		}
		if word := oneGenerationsVerdict(t, plain, defect.Section); word != "passed" {
			t.Errorf("the same ownership row without the defect that %s was reported %q for the %s section, so the failure above is not the defect's", defect.Name, word, defect.Section)
		}
	}
	if len(named) == 0 {
		t.Fatal("the ownership harness carries no defects at all, so this run asserted nothing")
	}
}

const generationsInventoried = 6

func TestTheOwnershipDefectInventoryIsTheSizeItSaysItIs(t *testing.T) {
	defects := eventtest.GenerationsDefects()
	if err := sizedGenerations(defects); err != nil {
		t.Fatal(err)
	}
	if err := sizedGenerations(defects[:len(defects)-1]); err == nil {
		t.Fatal("an inventory one row shorter passed the assertion that pins the size, so what is pinned is whatever the slice holds")
	}
}

func sizedGenerations(defects []eventtest.GenerationsDefect) error {
	if len(defects) != generationsInventoried {
		return fmt.Errorf("the ownership harness carries %d defects where its inventory is %d rows", len(defects), generationsInventoried)
	}
	return nil
}

func TestEveryOwnershipSectionIsNamedByADefectThatBreaksIt(t *testing.T) {
	names := eventtest.GenerationsSectionNames()
	defects := eventtest.GenerationsDefects()
	if err := guardedGenerations(names, defects); err != nil {
		t.Fatal(err)
	}
	for _, unguarded := range names {
		kept := []eventtest.GenerationsDefect{}
		for _, defect := range defects {
			if defect.Section != unguarded {
				kept = append(kept, defect)
			}
		}
		if err := guardedGenerations(names, kept); err == nil {
			t.Fatalf("an inventory naming no defect of the %s section passed the assertion that every section carries one", unguarded)
		}
	}
}

func guardedGenerations(names []string, defects []eventtest.GenerationsDefect) error {
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

func TestEveryOwnershipSectionInTheInventoryWasReported(t *testing.T) {
	names := eventtest.GenerationsSectionNames()
	if len(names) != 5 {
		t.Fatalf("the ownership harness carries %d sections where its inventory names five", len(names))
	}
	if err := reported(names, eventtest.CertifyGenerations(t, generationsFactory(t, newMemoryGenerations(), nil))); err != nil {
		t.Fatal(err)
	}
	shortened := names[:len(names)-1]
	if err := reported(names, eventtest.CertifyGenerations(t, generationsFactory(t, newMemoryGenerations(), nil), shortened...)); err == nil {
		t.Fatalf("a run that never dispatched %q passed the assertion that every section is reported exactly once", names[len(names)-1])
	}
}

func generationsFactory(t *testing.T, held *memoryGenerations, over func(projection.Generations) projection.Generations) eventtest.GenerationsFactory {
	t.Helper()
	return eventtest.GenerationsFactory{
		New: func(*testing.T) projection.Generations {
			beside := held.beside()
			if over == nil {
				return beside
			}
			return over(beside)
		},
		Begin: func(_ *testing.T, ctx context.Context, _ projection.Generations) (context.Context, eventtest.Tx) {
			return beginUnit(ctx, held.rows)
		},
		Closes: eventtest.LockingRead,
	}
}

// The three defects that are ownership rows rather than decorators: which handle
// each of the two statements runs on, and whether the read takes a lock.
func generationsShaped() map[string]func(*memoryGenerations) *memoryGenerations {
	return map[string]func(*memoryGenerations) *memoryGenerations{
		"answers from a handle of its own rather than the caller's unit":            (*memoryGenerations).onASecondHandle,
		"reads the ownership row without taking a lock on it":                       (*memoryGenerations).unlocked,
		"reads from a handle of its own while its write rides in the caller's unit": (*memoryGenerations).readingElsewhere,
	}
}

func generationsFactoriesFor(t *testing.T, defect eventtest.GenerationsDefect) (plain, broken eventtest.GenerationsFactory) {
	t.Helper()
	if defect.Over != nil {
		return generationsFactory(t, newMemoryGenerations(), nil), generationsFactory(t, newMemoryGenerations(), defect.Over)
	}
	build, known := generationsShaped()[defect.Name]
	if !known {
		t.Fatalf("the ownership defect %q is an implementation rather than a decorator and this test builds none for it", defect.Name)
	}
	return generationsFactory(t, newMemoryGenerations(), nil), generationsFactory(t, build(newMemoryGenerations()), nil)
}

func oneGenerationsVerdict(t *testing.T, factory eventtest.GenerationsFactory, section string) string {
	t.Helper()
	verdicts := eventtest.CertifyGenerations(t, factory, section)
	if len(verdicts) != 1 {
		t.Fatalf("running the %s section alone reported %d verdicts", section, len(verdicts))
	}
	return verdicts[0].Word
}
