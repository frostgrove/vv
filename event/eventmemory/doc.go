// Package eventmemory keeps an event history in memory. It is a complete
// implementation of the store contract and not a test double: expected-version
// admission, dense versions, strictly increasing positions, one-append
// atomicity and transactions with staged writes.
//
// A Log is what a store writes to, so the Log is the backing. Two stores over
// one Log are one store, and they cannot disagree about the two numbers the
// data depends on, because neither of them owns those numbers.
//
// A transaction stages its records and takes a claim on every stream it writes
// to. A conflict is therefore reported from Append — against the committed
// version plus what this transaction has already staged for that stream —
// and never from a commit, which is where a SQL store's unique index reports
// it and where a caller branches on it. A claim is refused at once rather than
// waited for, so two transactions taking two streams in opposite orders refuse
// each other instead of deadlocking, and a transaction held open across a
// network call starves every other writer of its streams. A transaction that is
// never committed and never rolled back holds its claims and its staged records
// for the life of the process: nothing reclaims them, and every later append to
// those streams is refused as a conflict that will never clear.
//
// A Tx may be used from more than one goroutine, and a context may carry one
// transaction per log: an append or a read on a transaction another goroutine
// has just finished is refused, and a transaction of another log neither
// shadows this store's own nor is mistaken for it.
//
// A position is assigned at commit, inside the one critical section that
// publishes, so commit order is position order and the newest position is the
// watermark. A rollback discards the staged records and advances the position
// counter by the number it staged: those positions are burned, the gap stays,
// and nothing is ever reissued.
//
// Nothing survives the process, which is what Persistence: Unsupported says.
package eventmemory
