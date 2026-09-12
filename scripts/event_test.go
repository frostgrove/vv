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
// vocabulary, and a row is where that dependency is argued. Two rows add
// anything at all.
//
// `eventpg` reaches `vvdb/lock/locksql` for its migration lock, and that one
// allowance carries `crud/adapter/crudsql` inside its own closure, because the
// lock is taken through the same adapter the caller's transaction arrives on.
// The rest of the closure is `crud`, `vvdb/lock`, `crud/catalog`,
// `crud/sqlfault`, `errs`, `errs/sqlerr` and `utils` — everything a PostgreSQL
// store needs and nothing more, which is why `health` and `port` stay outside
// it. Charging the adapter directly and the lock separately is not available:
// a row names one allowance, and the lock is the one that needs arguing.
//
// `projection` reaches `runtime`, because a consumer that follows a log
// continuously is a background activity the process owns and this repository has
// one contract for those: a runner the host supervises. Its closure is `runtime`
// alone. `health` stays outside it because readiness is a seam and an importance
// is the composition root's to name, and `port` stays outside it because this
// package writes no line at all — a halt reaches an operator through `Ready` and
// every transition through the `Observer`.
//
// `receipt` is a durable record beside an append, so it costs `event` for the
// `Commit`, the `Authority` and the `Stream` it records, and `crud`/`errs`
// through the vocabulary's own contracts; it reaches no store, no subsystem and
// no driver, which is why it is charged at zero and why the `Ledger` is an
// interface rather than an implementation.
func TestNoEventPackageCostsMoreThanTheSeamItNames(t *testing.T) {
	costsNoMoreThanItNames(t, extensionCost{
		prefix:    eventExtension,
		root:      "event",
		contracts: []string{"./crud", "./errs"},
		charged: map[string]string{
			eventExtension + "/eventmemory": "",
			eventExtension + "/eventpg":     "./vvdb/lock/locksql",
			eventExtension + "/eventtest":   "",
			eventExtension + "/projection":  "./runtime",
			eventExtension + "/receipt":     "",
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
