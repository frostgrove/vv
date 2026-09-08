// Package eventpg keeps an event history in one schema of one PostgreSQL
// database. It is a store beside eventmemory rather than a replacement for it,
// and the two answer the same contract.
//
// The schema is the store's contract and not an implementation detail. A unique
// constraint over (family, key, version) is what makes two events at one
// version unrepresentable whatever a writer believes; a foreign key is what
// makes an event row without a stream row unrepresentable; an identity column
// with INCREMENT 1, CACHE 1 and no CYCLE is what makes the log's order an order
// over time; a trigger refuses UPDATE, DELETE and TRUNCATE on the events table,
// so history is append-only for every writer and not only for this package; and
// a second trigger takes a transaction id before the statement draws its first
// position, which is what a global walk's watermark rests on. The two
// configured bounds are CHECK constraint operands, so the numbers the store
// publishes are the numbers the deployed schema enforces.
//
// Migrating is a deployment's choice and never a side effect of starting: the
// zero SchemaManagement verifies and creates nothing, MigrationStatements is
// the operator's own list, and New performs no I/O at all.
//
// The backing is the database and the schema together, so two store values over
// one schema are one store and two schemas in one database are two — which is
// what makes a cursor minted over one of them refused by the other.
//
// The store owns neither the connection pool nor the transaction. It opens
// nothing, commits nothing and rolls nothing back, and Close closes no *sql.DB.
package eventpg
