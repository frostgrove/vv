// Package catalog reads a database's own schema once, at declaration time, and
// answers questions about it afterwards without touching the connection again.
//
// A [Catalog] is one physical database's tables, columns, primary keys, unique
// constraints, unique indexes, foreign keys and check constraints, as that
// server described them. [Load] is the thing that can fail; every lookup after
// it does no I/O and takes no context, which is what lets a feature that needs a
// schema refuse to start rather than fail on its first bad write ([[D-021]],
// [[D-041]]).
//
// # Keyed on the handle
//
// One process holds several databases and two of them can disagree about what
// users_email_key means ([[UC-012]]). A [Set] keys a catalog on
// [crud.KeyOf] — the identity crud.Identified.DataSource reports, or the source
// itself when it cannot name one — and compares keys with
// [crud.SameDataSource]. The rule is reused rather than restated: a second
// implementation that drifted from crud.ExecutorFor's would let a repository run
// a statement on one connection while consulting a schema read from another.
//
// # Nil is not empty
//
// A nil slice means the engine was not asked or could not say. An empty one
// means it was asked and reported none. The pair matters most on
// [Constraint.Columns]: reading "not known" as "no columns" turns a constraint
// nothing can reproduce into one that looks trivially reproducible. The engines
// supply both sides of it — PostgreSQL reads a CHECK's columns out of conkey and
// the MySQL family hands back the clause and no columns — and
// TestEachEngineReportsWhatTheProbeWillNeed asserts each against the other.
//
// Constraint.Partial follows the same rule from the other side. Partial true
// with an empty Predicate means *partial, and the predicate is not recoverable
// as a value* — which is what SQLite reports, since the WHERE clause exists only
// inside the index's DDL text. It is a different fact from Partial false, and a
// consumer that conflates them replays a key the database only applies to some
// rows.
//
// Every accessor is nil-safe. A (*Table)(nil) answers false rather than
// panicking: a component wired wrong degrades instead of taking the process
// down.
//
// # Two rules a signature cannot carry
//
// This is not a migration tool, a DDL model, a query planner, or a Go-side
// re-implementation of the database's rules. Nothing here parses DDL text; a
// definition is carried verbatim and read by nobody. Two implementations of one
// constraint disagree eventually, and the one in the database is the one that is
// right ([[D-041]]).
//
// And nothing this package holds may reach a response body. A constraint name, a
// table, a column, a CHECK expression — [[D-044]] names all four, and
// crud/http/crudhttp copies an error's text into every body below 500 today, so a
// Load failure that quoted a constraint name would be a live disclosure.
// [ErrIntrospection] wraps the driver's error and says nothing itself.
//
// # Two names that mean something else one import path away
//
// [[D-035]] names packages and does not forbid these, and both are worth knowing
// before reading a file that holds one of each:
//
//   - catalog.Kind is what a constraint *is* — primary key, unique, foreign key,
//     check. errs.Kind is a transport class, and sqlerr.KindRetryable is an
//     untyped corpus label.
//   - [Load] reads a live schema. sqlerr.Load reads a checked-in JSON corpus off
//     disk.
package catalog
