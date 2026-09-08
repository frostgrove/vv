package eventtest_test

import (
	"fmt"
	"testing"

	"github.com/frostgrove/vv/event/eventtest"
)

// A conformance suite is the one artifact whose failure mode is silent by
// construction: one that tests nothing passes everything, and nobody notices
// until a store is wrong in production. This runs every defect the suite was
// built to detect against the section named for it and fails when a section
// passes, with the same store minus the defect as that section's control.
func TestTheSuiteStillDetectsEveryDefectItWasBuiltToDetect(t *testing.T) {
	sections := map[string]bool{}
	for _, name := range eventtest.SectionNames() {
		sections[name] = true
	}
	named := map[string]bool{}
	for _, defect := range eventtest.Defects() {
		if named[defect.Name] {
			t.Fatalf("two defects are named %q, so one of them is proving the other's section", defect.Name)
		}
		named[defect.Name] = true
		if !sections[defect.Section] {
			t.Fatalf("the defect %q names the %s section, which this suite does not have", defect.Name, defect.Section)
		}

		plain, broken := factoriesFor(t, defect)
		if word := oneVerdict(t, broken, defect.Section); word != "failed" {
			t.Errorf("the suite no longer detects a store that %s: its %s section was reported %q", defect.Name, defect.Section, word)
		}
		if word := oneVerdict(t, plain, defect.Section); word != "passed" {
			t.Errorf("the same store without the defect that %s was reported %q for the %s section, so the failure above is not the defect's", defect.Name, word, defect.Section)
		}
	}
	if len(named) == 0 {
		t.Fatal("the suite carries no defects at all, so this run asserted nothing")
	}
}

// The inventory is the only mutation harness this suite has, so its size is
// asserted rather than left to whatever the slice holds: a row dropped in a
// refactor takes a section's only control with it and reports nothing. The
// control is the same assertion over an inventory one row shorter.
const inventoried = 29

func TestTheDefectInventoryIsTheSizeItSaysItIs(t *testing.T) {
	defects := eventtest.Defects()
	if err := sized(defects); err != nil {
		t.Fatal(err)
	}
	if err := sized(defects[:len(defects)-1]); err == nil {
		t.Fatal("an inventory one row shorter passed the assertion that pins the size, so what is pinned is whatever the slice holds")
	}
}

func sized(defects []eventtest.Defect) error {
	if len(defects) != inventoried {
		return fmt.Errorf("the suite carries %d defects where its inventory is %d rows", len(defects), inventoried)
	}
	return nil
}

// A section no defect names has no control that can fail: every assertion in it
// can be deleted and every run stays green, which is the failure a conformance
// suite cannot see about itself. The control is one run of the same assertion
// per section, over an inventory with that section's rows taken out — so this
// proves the sections are covered rather than that a list was iterated.
func TestEverySectionIsNamedByADefectThatBreaksIt(t *testing.T) {
	names := eventtest.SectionNames()
	defects := eventtest.Defects()
	if err := guarded(names, defects); err != nil {
		t.Fatal(err)
	}
	for _, unguarded := range names {
		kept := []eventtest.Defect{}
		for _, defect := range defects {
			if defect.Section != unguarded {
				kept = append(kept, defect)
			}
		}
		if err := guarded(names, kept); err == nil {
			t.Fatalf("an inventory naming no defect of the %s section passed the assertion that every section carries one", unguarded)
		}
	}
}

func guarded(names []string, defects []eventtest.Defect) error {
	naming := map[string]int{}
	for _, defect := range defects {
		naming[defect.Section]++
	}
	for _, name := range names {
		if naming[name] == 0 {
			return fmt.Errorf("no defect in this suite's inventory breaks the %s section, so nothing here can tell whether that section still asserts anything", name)
		}
	}
	return nil
}

func oneVerdict(t *testing.T, factory eventtest.Factory, section string) string {
	t.Helper()
	verdicts := eventtest.Certify(t, factory, section)
	if len(verdicts) != 1 {
		t.Fatalf("running the %s section alone reported %d verdicts", section, len(verdicts))
	}
	return verdicts[0].Word
}

// The four defects that are stores rather than decorators, each with the pair of
// factories it is built from: one that has the defect and one that does not.
// Everything else in the inventory wraps the store its row describes.
func storeShaped() map[string]func(broken bool) eventtest.Factory {
	return map[string]func(broken bool) eventtest.Factory{
		"ignores AppendRequest.Expected and admits every append": func(broken bool) eventtest.Factory {
			return sliceFactory(broken, nil)
		},
		"leaves a rolled-back transaction's events readable": func(broken bool) eventtest.Factory {
			return stagingFactory(broken, nil)
		},
		"claims persistence and builds a second value over its backing that has none of what the first wrote": persistentFactory,
		"reads from the beginning of its log for a cursor it could not parse":                                 lenientFactory,
	}
}

func factoriesFor(t *testing.T, defect eventtest.Defect) (plain, broken eventtest.Factory) {
	t.Helper()
	if defect.Over != nil {
		return stagingFactory(false, nil), stagingFactory(false, defect.Over)
	}
	build, known := storeShaped()[defect.Name]
	if !known {
		t.Fatalf("the defect %q is a store rather than a decorator and this test builds no store for it", defect.Name)
	}
	return build(false), build(true)
}
