package eventtest

import (
	"testing"

	"github.com/frostgrove/vv/event"
)

// What the suite's own tests reach it by, and the reason there is a seam at all:
// the three fixture stores live in this package's external test package, where
// a value a store must return that needed event-internal access would not
// compile, and the inventories they are run against are unexported here. Neither
// half is shipped — this file is a test file, so nothing below is on the
// package's surface.

type Defect struct {
	Name    string
	Section string
	Over    func(event.Store) event.Store
}

func Defects() []Defect {
	held := make([]Defect, 0, len(defects()))
	for _, found := range defects() {
		held = append(held, Defect{Name: found.name, Section: found.section, Over: found.over})
	}
	return held
}

type Verdict struct {
	Section string
	Word    string
	Reason  string
}

// A run whose verdicts are answered rather than reported, so a test can watch a
// section fail without the test that drove it turning red.
func Certify(t *testing.T, factory Factory, named ...string) []Verdict {
	sections := chosen(t, inventory(), named)
	return answered(sweep(t, sections, probing(t, factory, inventory()), quietly))
}

func quietly(t *testing.T, given verdict) { t.Log(given.line()) }

func answered(given []verdict) []Verdict {
	held := make([]Verdict, 0, len(given))
	for _, one := range given {
		held = append(held, Verdict{Section: one.section, Word: one.word.String(), Reason: one.reason})
	}
	return held
}

func chosen[P running](t *testing.T, whole []section[P], named []string) []section[P] {
	if len(named) == 0 {
		return whole
	}
	held := make([]section[P], 0, len(named))
	for _, name := range named {
		found := false
		for _, one := range whole {
			if one.name == name {
				held = append(held, one)
				found = true
			}
		}
		if !found {
			t.Fatalf("eventtest: %q names no section of this suite", name)
		}
	}
	return held
}

func SectionNames() []string {
	held := make([]string, 0, len(inventory()))
	for _, one := range inventory() {
		held = append(held, one.name)
	}
	return held
}

func Certified(verdicts []Verdict) int {
	count := 0
	for _, given := range verdicts {
		if given.Word == passed.String() {
			count++
		}
	}
	return count
}

func Missing(capabilities event.Capabilities, factory Factory) string {
	return missing(capabilities, factory)
}

// The checkpoint half of the same seam, and it exists for the same reason: the
// defect inventory and the section list are unexported here, and the fixture
// checkpoint store they are run against lives in this package's external test
// package, where a value built out of event-internal access would not compile.

type CheckpointDefect struct {
	Name    string
	Section string
	Over    func(event.Checkpoints) event.Checkpoints
}

func CheckpointDefects() []CheckpointDefect {
	held := make([]CheckpointDefect, 0, len(checkpointDefects()))
	for _, found := range checkpointDefects() {
		held = append(held, CheckpointDefect{Name: found.name, Section: found.section, Over: found.over})
	}
	return held
}

func CertifyCheckpoints(t *testing.T, factory CheckpointFactory, named ...string) []Verdict {
	return answered(sweep(t, chosen(t, checkpointInventory(), named), tracking(t, factory), quietly))
}

func CheckpointSectionNames() []string {
	held := make([]string, 0, len(checkpointInventory()))
	for _, one := range checkpointInventory() {
		held = append(held, one.name)
	}
	return held
}

func MissingCheckpointHook(claims event.CheckpointCapabilities, factory CheckpointFactory) string {
	return missingCheckpointHook(claims, factory)
}

const (
	BuildsNoCheckpoints  = buildsNoCheckpoints
	AnswersNoCheckpoints = answersNoCheckpoints
	MintsNoCursor        = mintsNoCursor
	RepeatsItsCursor     = repeatsItsCursor
	InventsItsInstant    = inventsItsInstant
	CollapsesItsInstant  = collapsesItsInstant
)

// What a run reports beyond the section list, so a test one process out can ask
// whether the run it drove said it rather than matching prose it wrote itself.

const (
	BuildsNoStore    = buildsNoStore
	AnswersNoStore   = answersNoStore
	CertifiedNothing = certifiedNothing
)

func NoVerdictFrom(section string) string { return noVerdictFrom(section) }

func NarrowKey(maxKey int) string {
	return narrowKey(maxKey, 2*narrowestRunName+reserve(len(inventory())))
}

// What the three proxies answer before their wrappers turn it into a failed
// test, so the branch each of them exists for can be driven by a test that stays
// green.

func RoundTripAnswers[S, ID, E any](fact *event.Fact[S, ID, E], byRevision ...any) error {
	return roundTrips(fact, byRevision...)
}

func KeysAnswers[S, ID any](a *event.Aggregate[S, ID], ids ...ID) error {
	return keysRender(a, ids...)
}

func FamiliesAnswers(declarations ...event.Declaration) error {
	return familiesDiffer(declarations...)
}
