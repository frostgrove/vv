//go:build integration

package eventpg

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/frostgrove/vv/event"
	"github.com/frostgrove/vv/event/eventtest"
)

const (
	downgradeVariable = "EVENTPG_DOWNGRADE"
	reportedAs        = "eventtest: "
	wideRun           = "^TestTheStoreSatisfiesTheContract$"
	narrowRun         = "^TestTheStoreSatisfiesTheContractAtNarrowerLimits$"
	checkpointRun     = "^TestTheCheckpointStoreSatisfiesTheContract$"
)

const (
	certified = "passed"
	declined  = "not certified — this store does not promise monotone visibility"
)

type certification struct {
	section string
	verdict string
}

// What a conformance run of this store must report, section by section, in the
// order the suite dispatches them. It is written down rather than counted by eye
// because eventtest reports a section that *failed* with t.Error and one it
// could not certify with only t.Log: a Factory hook that stops answering — a
// moved driver init, an arm that stopped firing, a hook somebody simplified —
// downgrades a whole section from passed to not certified, every case in it
// stops running, and go test prints ok. Nineteen sections and one declined
// claim; anything else is a gate proceeding silently red.
func census() []certification {
	return []certification{
		{"binding", certified},
		{"stream identity", certified},
		{"expected version", certified},
		{"dense versions", certified},
		{"global order", certified},
		{"conservation", certified},
		{"stream paging", certified},
		{"global paging", certified},
		{"resumption", certified},
		{"bounds", certified},
		{"payload ownership", certified},
		{"refusal classes", certified},
		{"cancellation", certified},
		{"lifecycle", certified},
		{"concurrency", certified},
		{"transactions", certified},
		{"durability", certified},
		{"shared backing", certified},
		{"monotone visibility", declined},
		{"store failure classification", certified},
	}
}

// What a conformance run of the checkpoint store must report, section by
// section. Without it a run whose transactions section reported not certified —
// the section that certifies the one-unit half of the whole phase — prints ok
// and passes, because thirteen others certified and the suite's own third
// anti-vacuity rule does not fire.
func checkpointCensus() []certification {
	return []certification{
		{"binding", certified},
		{"absence", certified},
		{"round trip", certified},
		{"fence", certified},
		{"forget", certified},
		{"names", certified},
		{"bounds", certified},
		{"refusal classes", certified},
		{"lifecycle", certified},
		{"concurrency", certified},
		{"transactions", certified},
		{"durability", certified},
		{"topology", certified},
		{"topology handoff", certified},
	}
}

// Every configuration, because a hook is withdrawn from the factory each of them
// is built from. Each is driven as a subprocess of this binary: a run cannot
// read the log of the test it is, and the verdicts are t.Log lines.
func TestBothConformanceRunsCertifyEverySectionButTheOneThisStoreDeclines(t *testing.T) {
	for _, run := range []struct {
		what    string
		pattern string
		want    []certification
	}{
		{"at this store's own limits", wideRun, census()},
		{"at narrower ones", narrowRun, census()},
		{"over the checkpoint store", checkpointRun, checkpointCensus()},
	} {
		t.Run(run.what, func(t *testing.T) {
			output, code := runs(t, run.pattern, os.Environ())
			if code != 0 {
				t.Fatalf("the conformance run %s exited %d, so there is no census to read:\n%s", run.what, code, output)
			}
			if err := certifies(run.want, censusOf(output)); err != nil {
				t.Fatalf("the conformance run %s is not the run this store is certified by: %v\n%s", run.what, err, output)
			}
		})
	}
}

// The census driven rather than described. Each row withdraws one hook the way a
// regression withdraws it, and each must leave the run **green** and the census
// **red**: a row that turns the run red is a downgrade the ordinary gate already
// reports and says nothing about whether this census counts anything.
func TestADowngradedSectionIsCaughtByTheCensusAndByNothingElse(t *testing.T) {
	held := downgrades()
	if err := sized(held, withdrawals, "withdrawn hooks"); err != nil {
		t.Fatal(err)
	}
	if err := sized(held[:withdrawals-1], withdrawals, "withdrawn hooks"); err == nil {
		t.Fatal("an inventory one row shorter passed the assertion that pins the size, so what is pinned is whatever the slice holds")
	}

	for _, one := range held {
		t.Run(one.name, func(t *testing.T) {
			output, code := runs(t, one.run, append(os.Environ(), downgradeVariable+"="+one.name))
			if code != 0 {
				t.Fatalf("the %s run exited %d, so go test reports this downgrade and the census below is not what catches it:\n%s", one.name, code, output)
			}
			reported := censusOf(output)
			if slices.Contains(reported[one.section], certified) {
				t.Fatalf("the %s run certified the %s section anyway, so this row withdraws nothing:\n%s", one.name, one.section, output)
			}
			if err := certifies(one.want, reported); err == nil {
				t.Fatalf("the census passed the %s run, whose %s section was reported %v, so it counts nothing:\n%s", one.name, one.section, reported[one.section], output)
			}
		})
	}
}

func certifies(want []certification, reported map[string][]string) error {
	for _, one := range want {
		said := reported[one.section]
		if len(said) != 1 {
			return fmt.Errorf("the %s section was reported %d times in a run that dispatches every section once: %v", one.section, len(said), said)
		}
		if said[0] != one.verdict {
			return fmt.Errorf("the %s section was reported %q where a certified run reports %q", one.section, said[0], one.verdict)
		}
	}
	if len(reported) != len(want) {
		return fmt.Errorf("a run reported %d sections where this store is certified on %d, so the suite it was measured against is not the one this census was written for: %v", len(reported), len(want), reported)
	}
	return nil
}

// Every verdict a run reported, kept per section rather than last-one-wins: a
// section dispatched twice reports twice, and a map that overwrites would read
// that as the ordinary run.
func censusOf(output string) map[string][]string {
	reported := map[string][]string{}
	for _, line := range strings.Split(output, "\n") {
		if section, said, is := verdictIn(line); is {
			reported[section] = append(reported[section], said)
		}
	}
	return reported
}

// A verdict line as the report writes one: the section, the word, and an em dash
// before the reason where there is one. The reason is cut off before the word is
// looked for, because a reason ending in one of the four words would otherwise
// name a section nobody dispatched.
func verdictIn(line string) (string, string, bool) {
	at := strings.Index(line, reportedAs)
	if at < 0 {
		return "", "", false
	}
	said := strings.TrimSpace(line[at+len(reportedAs):])
	head, _, _ := strings.Cut(said, " — ")
	for _, word := range []string{"passed", "not certified", "failed", "not reported"} {
		if section, is := strings.CutSuffix(head, ": "+word); is && section != "" {
			return section, said[len(section)+len(": "):], true
		}
	}
	return "", "", false
}

// The other half of the harness, and the half go test cannot see. A store defect
// fails a section; a hook that stops answering downgrades one. Every row here is
// a hook the factory really supplies, withdrawn where a regression would leave
// it, and the section that goes down with it.
type withdrawal struct {
	name    string
	section string
	run     string
	want    []certification
	from    func(eventtest.Factory) eventtest.Factory
	over    func(eventtest.CheckpointFactory) eventtest.CheckpointFactory
}

const withdrawals = 5

// A checkpoint hook cannot be withdrawn here — a claimed capability with no hook
// is fatal before a section runs, which is the point of that gate — so the two
// checkpoint rows withdraw a claim instead. Each leaves the run green and the
// census red, which is the property this file's own name is about.
func downgrades() []withdrawal {
	return []withdrawal{
		{name: "no-fail-hook", section: "store failure classification", run: narrowRun, want: census(),
			from: func(factory eventtest.Factory) eventtest.Factory {
				factory.Fail = nil
				return factory
			}},
		{name: "no-unconfirmed-failure", section: "store failure classification", run: narrowRun, want: census(),
			from: withheld(event.Unconfirmed)},
		{name: "no-unparsable-cursor", section: "resumption", run: narrowRun, want: census(),
			from: func(factory eventtest.Factory) eventtest.Factory {
				factory.Unparsable = nil
				return factory
			}},
		{name: "no-persistence-claim", section: "durability", run: checkpointRun, want: checkpointCensus(),
			over: unclaiming(event.CheckpointCapabilities{Transactions: event.Supported, Persistence: event.Unsupported})},
		{name: "no-transactions-claim", section: "transactions", run: checkpointRun, want: checkpointCensus(),
			over: unclaiming(event.CheckpointCapabilities{Transactions: event.Unsupported, Persistence: event.Supported})},
	}
}

// A checkpoint store that keeps and joins everything it always did and says it
// does not, which is what a claim somebody narrowed looks like from outside.
func unclaiming(claims event.CheckpointCapabilities) func(eventtest.CheckpointFactory) eventtest.CheckpointFactory {
	return func(factory eventtest.CheckpointFactory) eventtest.CheckpointFactory {
		built, beside := factory.New, factory.Sibling
		factory.New = func(t *testing.T) event.Checkpoints {
			return claimed{Checkpoints: built(t), claims: claims}
		}
		factory.Sibling = func(t *testing.T, c event.Checkpoints) event.Checkpoints {
			return claimed{Checkpoints: beside(t, c), claims: claims}
		}
		return factory
	}
}

type claimed struct {
	event.Checkpoints
	claims event.CheckpointCapabilities
}

func (this claimed) Capabilities() event.CheckpointCapabilities { return this.claims }

// wayTo answering nothing for one outcome, which is what a Fail hook that stopped
// being able to produce it leaves behind: the store is asked, says it cannot, and
// the section it was the last case of is downgraded whole.
func withheld(outcome event.Outcome) func(eventtest.Factory) eventtest.Factory {
	return func(factory eventtest.Factory) eventtest.Factory {
		held := factory.Fail
		factory.Fail = func(t *testing.T, s event.Store, asked event.Outcome) bool {
			return asked != outcome && held(t, s, asked)
		}
		return factory
	}
}

func downgraded(t *testing.T, factory eventtest.Factory) eventtest.Factory {
	t.Helper()
	name := os.Getenv(downgradeVariable)
	if name == "" {
		return factory
	}
	for _, one := range downgrades() {
		if one.name == name && one.from != nil {
			return one.from(factory)
		}
	}
	return factory
}

func downgradedCheckpoints(t *testing.T, factory eventtest.CheckpointFactory) eventtest.CheckpointFactory {
	t.Helper()
	name := os.Getenv(downgradeVariable)
	if name == "" {
		return factory
	}
	for _, one := range downgrades() {
		if one.name == name && one.over != nil {
			return one.over(factory)
		}
	}
	return factory
}
