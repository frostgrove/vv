// Package sqlerr turns what a database driver reported into what the error
// contract can carry.
//
// A parser takes one flattened driver error — the dialect, the SQLSTATE, the
// engine's own number and the structured fields the driver populated — and
// answers a [github.com/shardit-io/vv/errs.Code] and a
// [github.com/shardit-io/vv/errs.Source], or nothing. It holds no driver
// import, names no driver type and opens no connection. Extraction, which does
// have to name a type or ask by shape, is the adapters' job and lives with
// them.
//
// # Four files because the engines answer four ways
//
// The dialect is part of the key and not decoration ([[D-046]]). PostgreSQL is
// keyed on the SQLSTATE alone — every entry in its corpus records a native
// number of zero. MySQL and MariaDB are keyed on the state and the number
// together, because 1205 and 3819 share HY000 and mean opposite things, and
// because 1366 is HY000 on one and 22007 on the other. SQLite is keyed on the
// number alone read as bytes, and refuses anything carrying a SQLSTATE at all,
// because it has none and never will.
//
// So mysql.go and mariadb.go hold two tables that agree on nine rows and
// disagree on two, written out rather than shared. Merging them is green today
// and wrong the first time either server moves.
//
// # The contract and the fixture
//
// [Classify] and the four dialect tables are the contract. [Corpus], [Case],
// [Err], [Load], [Save] and [Path] are the fixture the tables are written
// against: the captured evidence, checked in under testdata/corpus, one file
// per engine. It lives here rather than in the test module because the parsers
// are unit-tested and a unit test in this module cannot import that one. The
// program that captures it can go the other way, and does.
//
// # Two rules that are not visible in a signature
//
// A parser opens no connection and names no driver type. That is what lets this
// package sit in a module with an empty require block, and it is what make
// check-tiers seals.
//
// A parser reads no message text, no Detail and no Hint ([[D-039]]). Three of
// the four engines localise the message through a session setting, so a parser
// that reads it works on the developer's laptop and misreads on a server
// deployed elsewhere — failing by producing a confident wrong answer rather
// than by erroring. [Err.Message] is carried here so a test can prove it is not
// read.
//
// # These are not an errs.Classifier
//
// A [github.com/shardit-io/vv/errs.Classifier] takes an error and produces a
// whole [github.com/shardit-io/vv/errs.Fault]. A fault needs a Kind, which
// comes from the wired Codes value this package cannot see, and it needs
// Builder.Wrapping, which is the only door to a sentinel. So a classifier is
// written *over* these parsers, in the layer that has both — not by making them
// implement it.
//
// That layer is github.com/shardit-io/vv/sqlfault, which has the parsers, crud
// for the sentinel and catalog for the columns a driver did not name — none of
// which this package may import. Extraction, the half that must name a driver
// type or shape-match one, stays with the adapters.
package sqlerr
