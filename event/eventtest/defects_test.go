package eventtest_test

import (
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

func oneVerdict(t *testing.T, factory eventtest.Factory, section string) string {
	t.Helper()
	verdicts := eventtest.Certify(t, factory, section)
	if len(verdicts) != 1 {
		t.Fatalf("running the %s section alone reported %d verdicts", section, len(verdicts))
	}
	return verdicts[0].Word
}

// The two defects that are stores rather than decorators are the two a decorator
// could not commit without doing something the contract forbids a decorator, so
// this test builds them and every other defect wraps the store the row describes.
func factoriesFor(t *testing.T, defect eventtest.Defect) (plain, broken eventtest.Factory) {
	t.Helper()
	if defect.Over != nil {
		return stagingFactory(false, nil), stagingFactory(false, defect.Over)
	}
	switch defect.Name {
	case "ignores AppendRequest.Expected and admits every append":
		return sliceFactory(false, nil), sliceFactory(true, nil)
	case "leaves a rolled-back transaction's events readable":
		return stagingFactory(false, nil), stagingFactory(true, nil)
	case "claims persistence and builds a second value over its backing that has none of what the first wrote":
		return persistentFactory(false), persistentFactory(true)
	case "reads from the beginning of its log for a cursor it could not parse":
		return lenientFactory(false), lenientFactory(true)
	}
	t.Fatalf("the defect %q is a store rather than a decorator and this test builds no store for it", defect.Name)
	return eventtest.Factory{}, eventtest.Factory{}
}
