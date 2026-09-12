package eventtest_test

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/frostgrove/vv/event/eventtest"
	"github.com/frostgrove/vv/event/receipt"
)

// The admission rules of the three application harnesses are a t.Fatal before
// any section runs, so the only place they can be watched from is one process
// out: a failure anywhere under a T fails the T that started it. The child runs
// the case the environment names and the parent reads the exit status, what was
// said, and — the half the rule is for — that no section reported a verdict at
// all.
const admissionCase = "EVENTTEST_APPLICATION_ADMISSION_CASE"

func TestAnApplicationFactoryThatCanCertifyNothingFailsBeforeASectionRuns(t *testing.T) {
	if name := os.Getenv(admissionCase); name != "" {
		runsTheAdmissionCase(t, name)
		return
	}
	for _, one := range []struct {
		what     string
		says     string
		sections []string
	}{
		{"a ledger factory that builds no ledger", eventtest.BuildsNoLedger, eventtest.LedgerSectionNames()},
		{"a ledger factory that answers no ledger", eventtest.AnswersNoLedger, eventtest.LedgerSectionNames()},
		{"a ledger factory that begins no unit", eventtest.OpensNoUnit, eventtest.LedgerSectionNames()},
		{"an ownership factory that builds no row", eventtest.BuildsNoGenerations, eventtest.GenerationsSectionNames()},
		{"an ownership factory that begins no unit", eventtest.OpensNoOwnershipUnit, eventtest.GenerationsSectionNames()},
		{"an ownership factory that states no closure", eventtest.StatesNoClosure, eventtest.GenerationsSectionNames()},
		{"a queue factory that builds no queue", eventtest.BuildsNoPark, eventtest.ParkSectionNames()},
		{"a queue factory that begins no unit", eventtest.OpensNoParkUnit, eventtest.ParkSectionNames()},
	} {
		t.Run(one.what, func(t *testing.T) {
			output, code := ranTheApplicationCase(t, one.what)
			if code == 0 {
				t.Fatalf("a run over %s exited 0, and a harness that cannot ask its questions must not certify anything:\n%s", one.what, output)
			}
			if !strings.Contains(output, one.says) {
				t.Errorf("a run over %s never named what was missing, so an implementer is told a run failed and not which hook:\n%s", one.what, output)
			}
			for _, section := range one.sections {
				if strings.Contains(output, "eventtest: "+section+": ") {
					t.Errorf("a run over %s reported a verdict for the %s section, so the refusal is not before any section runs:\n%s", one.what, section, output)
				}
			}
		})
	}
}

func runsTheAdmissionCase(t *testing.T, what string) {
	switch what {
	case "a ledger factory that builds no ledger":
		eventtest.RunLedger(t, eventtest.LedgerFactory{})
	case "a ledger factory that answers no ledger":
		factory := ledgerFactory(t, newMemoryLedger(t), nil)
		factory.New = func(*testing.T) receipt.Ledger { return nil }
		eventtest.RunLedger(t, factory)
	case "a ledger factory that begins no unit":
		factory := ledgerFactory(t, newMemoryLedger(t), nil)
		factory.Begin = nil
		eventtest.RunLedger(t, factory)
	case "an ownership factory that builds no row":
		eventtest.RunGenerations(t, eventtest.GenerationsFactory{Closes: eventtest.LockingRead})
	case "an ownership factory that begins no unit":
		factory := generationsFactory(t, newMemoryGenerations(), nil)
		factory.Begin = nil
		eventtest.RunGenerations(t, factory)
	case "an ownership factory that states no closure":
		factory := generationsFactory(t, newMemoryGenerations(), nil)
		factory.Closes = eventtest.ClosureUnstated
		eventtest.RunGenerations(t, factory)
	case "a queue factory that builds no queue":
		eventtest.RunPark(t, eventtest.ParkFactory{})
	case "a queue factory that begins no unit":
		factory := parkFactory(t, newMemoryPark(), nil)
		factory.Begin = nil
		eventtest.RunPark(t, factory)
	default:
		t.Fatalf("%q names no admission case of this test", what)
	}
}

func ranTheApplicationCase(t *testing.T, what string) (string, int) {
	t.Helper()
	child := exec.Command(os.Args[0], "-test.run", "^TestAnApplicationFactoryThatCanCertifyNothingFailsBeforeASectionRuns$", "-test.v", "-test.count=1")
	child.Env = append(os.Environ(), admissionCase+"="+what)
	output, err := child.CombinedOutput()
	if err == nil {
		return string(output), 0
	}
	exit, is := err.(*exec.ExitError)
	if !is {
		t.Fatalf("this binary could not be run as a child of itself: %v\n%s", err, output)
	}
	return string(output), exit.ExitCode()
}

// The three factories a harness admits, so the eight refusals above are refusals
// of what is missing rather than of the shape of a factory: each of these
// certifies every section it has.
func TestTheThreeApplicationFactoriesThisPackageWritesAreAdmitted(t *testing.T) {
	if certified := eventtest.Certified(eventtest.CertifyLedger(t, ledgerFactory(t, newMemoryLedger(t), nil))); certified != 7 {
		t.Errorf("the reference ledger certified %d of seven sections", certified)
	}
	if certified := eventtest.Certified(eventtest.CertifyGenerations(t, generationsFactory(t, newMemoryGenerations(), nil))); certified != 5 {
		t.Errorf("the reference ownership row certified %d of five sections", certified)
	}
	if certified := eventtest.Certified(eventtest.CertifyPark(t, parkFactory(t, newMemoryPark().bounded(3, 2), nil))); certified != 6 {
		t.Errorf("the reference queue certified %d of six sections", certified)
	}
}
