package eventtest_test

import (
	"fmt"
	"testing"

	"github.com/frostgrove/vv/event/eventtest"
)

// go test -list cannot see a subtest, so a suite shipped with twelve of its
// twenty sections written passes every count clause, prints twelve passing lines
// and looks complete. This reads the report instead, and its control is a run
// over an inventory one section shorter, which must fail the same assertion —
// otherwise this proves that a list was iterated rather than that the sections
// exist.
func TestEverySectionInTheInventoryWasReported(t *testing.T) {
	names := eventtest.SectionNames()
	if len(names) != 20 {
		t.Fatalf("the suite carries %d sections where its inventory names twenty", len(names))
	}
	if err := reported(names, eventtest.Certify(t, stagingFactory(faults{}, nil))); err != nil {
		t.Fatal(err)
	}

	shortened := names[:len(names)-1]
	if err := reported(names, eventtest.Certify(t, stagingFactory(faults{}, nil), shortened...)); err == nil {
		t.Fatalf("a run that never dispatched %q passed the assertion that every section is reported exactly once", names[len(names)-1])
	}
}

func reported(names []string, verdicts []eventtest.Verdict) error {
	counted := map[string]int{}
	for _, given := range verdicts {
		switch given.Word {
		case "passed", "not certified", "failed":
		default:
			return fmt.Errorf("the %s section was reported as %q, which is none of the three words", given.Section, given.Word)
		}
		counted[given.Section]++
	}
	for _, name := range names {
		if counted[name] != 1 {
			return fmt.Errorf("the %s section was reported %d times in a run of %d verdicts", name, counted[name], len(verdicts))
		}
	}
	if len(counted) != len(names) {
		return fmt.Errorf("a run reported %d sections where the inventory names %d", len(counted), len(names))
	}
	return nil
}
