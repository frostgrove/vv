// Package receipt records what an operation did, beside the append it did it
// with, in the caller's own transaction. It answers one question: a command was
// issued, the connection died before the response arrived, and the caller is
// holding nothing but the identity it minted before it started — did that
// operation happen, and if it did, what did it write?
//
// The word is not event.Commit's. A commit is the token an append hands back
// inside the process that made it, and it does not survive the process; a
// receipt is a durable row in the application's own table, written under a key
// the caller chose, and it is the only thing a second process can find. The
// framework declares the row's shape and reads it back; the application owns the
// table, the statements and the sweep, behind Ledger — the shape
// projection.Park and projection.Generations already have.
//
// It lives in the event store's own database and is written through the caller's
// own transaction, which is what makes the row and the events one commit. That
// is checked rather than documented: Claim compares the ledger's transaction
// with the store's and refuses unless they are one. A receipt that is not atomic
// with its append is a row that outlives a rollback and reports an operation
// that never happened.
//
// Nothing here is a store capability. No store gains a table, a migration or a
// schema version, a deployment that does not want receipts deploys nothing, and
// every store — including one this repository never saw — gets receipts the day
// its application writes a Ledger.
//
// The order is claim, decide, append, complete, and Once is the spelling that
// makes it unwritable in any other order. The claim comes first because a
// receipt written after the append is too late: two retries at two expected
// versions both append and then collide at the receipt, with both sets of events
// already in the log.
//
// Two things it deliberately does not do. It does not replay the domain
// decision: on Repeated the caller answers its client from the range the first
// attempt wrote, and nothing here re-proposes the same facts at whatever version
// the store now holds. And it does not turn an absent row into a rollback — a
// resolver on a second connection cannot tell a transaction that rolled back
// from one that is still open, so that state is named Unresolved rather than
// guessed at.
//
// Nothing in this package opens, commits or rolls back a transaction, starts a
// goroutine, reads a clock or writes a line. Every instant it compares comes
// from the ledger's own database clock, except ResolveSpec.Issued, which is the
// caller's and cannot be anything else.
package receipt
