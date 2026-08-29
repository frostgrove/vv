// Package app is the composition root: the parts of a program's start-up that
// every service writes and most write slightly wrong.
//
// Two things live here today, and they are the two that turned out to be shared
// between an HTTP surface and a seed command:
//
//   - [Ordered], because a set of contributions collected from independent
//     modules has no order of its own, and "the guard runs before the handler"
//     is not something to leave to registration order;
//   - [Seeder] and [Runner], because seed data is contributed the same way and
//     has the same problem, plus one of its own: it has to be runnable twice.
//
// # What this package may never become
//
// It holds a list of things and the order to run them in. It does not hold a way
// to *find* one. No component is looked up by its type here — no
// `map[reflect.Type]any`, no `Get[T]()`, no `...any` that is type-switched — and
// [[D-037]] is the decision that says why, with the three steps by which a
// component list becomes a container.
//
// That refusal is not a refusal of dependency injection. An application that
// wants a container brings its own, and `app/appfx` is the binding for the one
// that has come up: it hands these types to uber/fx, which holds the graph.
// The distinction is that the graph is the consumer's, in a module they chose to
// import, and not something every consumer of this library gets whether or not
// they asked ([[D-074]]).
//
// # Why it is not `utils`
//
// [[D-058]] draws that boundary at a single line: nothing under `utils/` imports
// a subsystem. This imports `port`, for the logger seam every line in this
// library goes through ([[D-062]]), and a package that needs a subsystem is not
// a utility.
package app
