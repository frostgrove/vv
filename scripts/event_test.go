package scripts

import "testing"

const eventExtension = "github.com/frostgrove/vv/event"

// A fact history is one capability and nothing else in the root module has it,
// so the extension is optional by the import graph rather than by a paragraph.
func TestNoBaseSubsystemDependsOnTheEventExtension(t *testing.T) {
	noBaseSubsystemDependsOn(t, eventExtension)
}

// What the vocabulary costs is two contract packages and no subsystem: `crud`
// for the classes a transport already maps and the data-source comparison a
// backing is, `errs` for the fault an over-a-bound refusal carries. Both are
// named here rather than computed from `./crud`'s own graph, which is how the
// tenancy version spells the same rule: `crud` does not reach `errs`, so that
// spelling refuses the error contract this vocabulary is written in and gets
// loosened on its first run.
//
// A store is a package beside the core and costs the core alone. A package that
// is not charged here fails rather than being skipped, which is the half a
// written table cannot do: the table is what the next store is added beside.
// Charging is a map from the package to what it may add on top of the
// vocabulary, and a row is where that dependency is argued. `eventpg` is the
// one that adds anything: a store that speaks SQL reaches
// `crud/adapter/crudsql` for the caller's transaction, and the closure of that
// one allowance is `crud`, `crud/catalog`, `crud/sqlfault`, `errs`,
// `errs/sqlerr` and `utils` — everything a PostgreSQL store needs and nothing
// more, which is why `health`, `port` and `runtime` stay outside it.
func TestNoEventPackageCostsMoreThanTheSeamItNames(t *testing.T) {
	costsNoMoreThanItNames(t, extensionCost{
		prefix:    eventExtension,
		root:      "event",
		contracts: []string{"./crud", "./errs"},
		charged: map[string]string{
			eventExtension + "/eventmemory": "",
			eventExtension + "/eventpg":     "./crud/adapter/crudsql",
			eventExtension + "/eventtest":   "",
		},
		core: func(reached string) string {
			return "the vocabulary reaches " + reached + " — a deployment that wants a fact history and no subsystem of ours compiles it anyway"
		},
		uncharged: func(path string) string {
			return path + " is a package of the extension and says nothing about what it costs — the vocabulary and one store each is the whole layout, and what a package costs is written down here"
		},
		overreach: func(path, reached, allowance string) string {
			if allowance == "" {
				return path + " reaches " + reached + ", and its row here says it costs the vocabulary and nothing else"
			}
			return path + " reaches " + reached + ", and its row here says it costs the vocabulary and " + allowance
		},
	})
}

func TestMerelyImportingTheEventExtensionStartsNothing(t *testing.T) {
	startsNothing(t, eventExtension, "event")
}
