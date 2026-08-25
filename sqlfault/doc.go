// Package sqlfault turns a driver error into an errs.Fault.
//
// errs/sqlerr's doc ends by saying a classifier is written *over* its parsers,
// in the layer that has both — not by making them implement it. This is that
// layer: it has the parsers, it has crud for the sentinel, and it has catalog
// for the columns a driver did not name. errs/sqlerr cannot have any of the
// three, because Makefile:TIER0_SEALED holds it to the standard library and
// errs/... alone.
//
// Two adapters ask this package the same questions, and that is the whole
// reason it exists rather than being written twice. The last time one rule was
// implemented in both of them they diverged: a deferred constraint was a 409
// through crudsql.Tx.Commit and a 500 through crudpgx.Tx.Commit, and each
// adapter's tests passed.
//
// # Two gates, and they answer different questions
//
// [Integrity] answers whether the database refused to break a constraint, and
// decides crud.ErrConflict. It is deliberately wider than the parsers: a
// class-23 number nobody provoked, and a SQLite low-byte-19 code whose high byte
// no probe produced, are conflicts here and unclassified there ([[D-046]]).
//
// sqlerr.Classify answers which violation it was, and decides errs.Code. It
// covers classes the first gate refuses on purpose — a value too long is not a
// collision — and refuses keys the first gate accepts.
//
// A fault is built only when a code **and** its kind are both known. errs.Kind's
// zero value is KindInternal, so a fault built from an unwired vocabulary would
// claim 500 for a duplicate key; refusing to build is the only answer that
// cannot lie.
//
// # What no arm may read
//
// [sqlerr.Err.Message], Fields["Detail"] and Fields["Hint"]. Three of the four
// engines localise all three through a session setting, so anything read from
// them classifies differently depending on where the server was deployed
// ([[D-039]]). They are carried so a test can prove they are not read.
//
// # The engine string is declared, never derived
//
// [New] takes the engine as a plain string and the caller supplies it as a
// literal. Nothing here inspects a crud.Dialect: crud.Dialect.Name answers
// "mysql" for MariaDB, and a type switch over the dialect types is the same
// derivation written differently — it answers "mysql" for a MariaDB server too,
// and sends 4025 through MySQL's table. [[D-046]] forbids the derivation, so the
// constructor is the declaration. The only reachable failure is then *no* code,
// never a wrong one. A consumer who wants it measured has one measurement in the
// tree: catalog.Catalog.Dialect.
//
// # Not a contract package
//
// This is deliberately not on Makefile:TIER0. It has one implementation, which
// is an implementation and not a contract ([[D-048]]). The contract a third
// party writes against is errs.Classifier, which this satisfies.
//
// The name carries a prefix because "fault" alone would sit beside errs.Fault in
// every file here — each one imports errs and returns a *errs.Fault — and a
// reader would have to hold two meanings of one word. It is the same
// construction as errs/sqlerr, one layer up ([[D-035]]).
package sqlfault
