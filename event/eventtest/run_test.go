package eventtest_test

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/frostgrove/vv/event"
	"github.com/frostgrove/vv/event/eventtest"
)

// Run is the one function a store's own package calls and the only one another
// module can reach, and everything it does past dispatching sections is a report
// on the *testing.T it was handed. That report cannot be watched from a test
// that stays green — a failure anywhere under a T fails the T that started it —
// so the run happens one process out, in this same binary: the child runs the
// case the environment names and the parent reads the exit status and what was
// said. Without this, deleting every t.Error in Run leaves the whole tree green
// and a store that corrupts a page is certified.
const reportingCase = "EVENTTEST_REPORTING_CASE"

type reporting struct {
	what   string
	build  func(*testing.T) eventtest.Factory
	fails  bool
	reads  bool
	says   []string
	silent []string
}

func TestTheRunReportsWhatItFound(t *testing.T) {
	if name := os.Getenv(reportingCase); name != "" {
		eventtest.Run(t, namedReporting(t, name).build(t))
		return
	}
	parent := t.Name()
	for _, one := range reportings(t) {
		t.Run(one.what, func(t *testing.T) {
			output := ranTheCase(t, parent, one)
			for _, said := range one.says {
				if !strings.Contains(output, said) {
					t.Errorf("a run over %s never reported %q, so nothing in this suite would notice if it stopped reporting it at all", one.what, said)
				}
			}
			for _, quiet := range one.silent {
				if strings.Contains(output, quiet) {
					t.Errorf("a run over %s reported %q, which is the answer for a run that asked nothing", one.what, quiet)
				}
			}
			if reached := reachedASection(output); reached != one.reads {
				t.Errorf("a run over %s %s, where it must %s", one.what, reportedOn(reached), reportedOn(one.reads))
			}
		})
	}
}

func reportedOn(reached bool) string {
	if reached {
		return "reported a verdict for a section"
	}
	return "be refused at the door with no section reported"
}

func reachedASection(output string) bool {
	for _, name := range eventtest.SectionNames() {
		if strings.Contains(output, "eventtest: "+name+": ") {
			return true
		}
	}
	return false
}

func ranTheCase(t *testing.T, parent string, one reporting) string {
	t.Helper()
	child := exec.Command(os.Args[0], "-test.run", "^"+parent+"$", "-test.v")
	child.Env = append(os.Environ(), reportingCase+"="+one.what)
	output, err := child.CombinedOutput()
	if failed := err != nil; failed != one.fails {
		t.Errorf("a run over %s left the test it was handed %s, where a store like this must leave it %s",
			one.what, marked(failed), marked(one.fails))
		t.Log(string(output))
	}
	return string(output)
}

func marked(failed bool) string {
	if failed {
		return "failed"
	}
	return "green"
}

func namedReporting(t *testing.T, what string) reporting {
	t.Helper()
	for _, one := range reportings(t) {
		if one.what == what {
			return one
		}
	}
	t.Fatalf("%q names no case of this test", what)
	return reporting{}
}

// The store each case is a run over, and what the run must say about it. The
// first is the control every other row is read against: a store that satisfies
// the contract leaves a green run, so a failure below is the store's and not the
// harness's.
func reportings(t *testing.T) []reporting {
	t.Helper()
	beginless := stagingFactory(false, nil)
	beginless.Begin = nil
	unstated := stagingFactory(false, func(store event.Store) event.Store { return sayingNothing{store} })
	return []reporting{
		{
			what:   "a store that satisfies the contract",
			build:  func(*testing.T) eventtest.Factory { return stagingFactory(false, nil) },
			reads:  true,
			silent: []string{eventtest.CertifiedNothing},
		},
		{
			what:  "a store that fails one section",
			build: func(t *testing.T) eventtest.Factory { return stagingFactory(false, defectOn(t, "lifecycle")) },
			fails: true,
			reads: true,
		},
		{
			what: "a section whose factory hook leaves the run",
			build: func(*testing.T) eventtest.Factory {
				factory := stagingFactory(false, nil)
				factory.Begin = func(t *testing.T, _ context.Context, _ event.Store) (context.Context, eventtest.Tx) {
					t.SkipNow()
					return nil, nil
				}
				return factory
			},
			fails: true,
			reads: true,
			says:  []string{eventtest.NoVerdictFrom("transactions")},
		},
		{
			what:  "a store that refuses every operation",
			build: func(*testing.T) eventtest.Factory { return refusingFactory() },
			fails: true,
			reads: true,
			says:  []string{eventtest.CertifiedNothing},
		},
		{
			what:  "a store that claims transactions and a factory that begins none",
			build: func(*testing.T) eventtest.Factory { return beginless },
			fails: true,
			says:  []string{"eventtest: " + eventtest.Missing(newStagingStore(t, false, limits()).Capabilities(), beginless)},
		},
		{
			what:  "a store that states nothing about its transactions",
			build: func(*testing.T) eventtest.Factory { return unstated },
			fails: true,
			says:  []string{"eventtest: " + eventtest.Missing(sayingNothing{newStagingStore(t, false, limits())}.Capabilities(), unstated)},
		},
		{
			what:  "a factory that builds no store",
			build: func(*testing.T) eventtest.Factory { return eventtest.Factory{} },
			fails: true,
			says:  []string{eventtest.BuildsNoStore},
		},
		{
			what: "a factory that answers no store",
			build: func(*testing.T) eventtest.Factory {
				return eventtest.Factory{New: func(*testing.T) event.Store { return nil }}
			},
			fails: true,
			says:  []string{eventtest.AnswersNoStore},
		},
		{
			what:  "a store whose MaxKey leaves this suite no room for its own names",
			build: func(*testing.T) eventtest.Factory { return stagingFactoryAt(narrowestKey(), false, nil) },
			fails: true,
			says:  []string{eventtest.NarrowKey(narrowestKey().MaxKey)},
		},
	}
}

func narrowestKey() event.Limits {
	published := limits()
	published.MaxKey = 20
	return published
}

func defectOn(t *testing.T, section string) func(event.Store) event.Store {
	t.Helper()
	for _, defect := range eventtest.Defects() {
		if defect.Section == section && defect.Over != nil {
			return defect.Over
		}
	}
	t.Fatalf("this suite's defect inventory holds no decorator that breaks the %s section", section)
	return nil
}
