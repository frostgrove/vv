// Package vvdb turns one configuration struct into a connection to PostgreSQL,
// MySQL, MariaDB or SQLite.
//
// It is the piece that was missing from every example in this repository: the
// DSN was a string constant, and swapping an engine meant editing Go. Here the
// same YAML describes any of the four, and the difference between them —
// which port, which escaping, which spelling of "use TLS" — is this package's
// problem rather than the reader's.
//
// # Who opens the connection
//
// The application does. vv "does not own the caller's connection or
// transaction", and this package does not change that: it hands back a
// *sql.DB, and the hop into the framework stays a visible line.
//
//	sqlDB := vvdb.MustOpen(&cfg.DB)
//	repo  := Products.Bind(crudsql.Postgres(sqlDB))
//
// Nothing here imports crud, errs or any other part of vv, and nothing here is
// reachable from the repository seam. A service with no vv in it can take this
// package on its own ([[D-057]]).
//
// # Three levels
//
//   - [PostgresDSN], [MySQLDSN], [MariaDBDSN], [SQLiteDSN] and [DSN] build the
//     connection string and open nothing.
//   - [Open] and [MustOpen] hand back a *sql.DB with the pool already sized.
//     The driver is the consumer's blank import, which is why this package has
//     no dependency and needs no module of its own.
//   - utils/vvdb/dbpgx does the same for a *pgxpool.Pool, which is not database/sql
//     and therefore is a module.
//
// # What it refuses
//
// An engine it does not know, a DSN set beside the fields that DSN would
// override, a setting an engine cannot express. All three are start-up
// failures with the field named. A configuration that is wrong should stop the
// process before traffic arrives, not surface later as a connection to the
// wrong database ([[D-021]]).
package vvdb
