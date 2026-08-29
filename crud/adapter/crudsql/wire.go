package crudsql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/catalog"
	"github.com/frostgrove/vv/crud/sqlfault"
)

// An Engine names the server that is answering.
//
// [crud.Dialect] says how to write SQL and not who is reading it — crud.MySQL
// targets MySQL and MariaDB both — so a name is what the classifier keys on and
// a dialect cannot supply it ([[D-046]]).
//
// It is a string with the four values `utils/vvdb`.Engine uses, so a
// configuration read through vvdb converts rather than being mapped:
//
//	source, err := crudsql.Wired(ctx, crudsql.Engine(configuration.Db.Engine), db)
//
// The constants are prefixed because the four constructors already hold the
// bare names, and those are what a caller who knows its engine at compile time
// should keep reaching for.
type Engine string

const (
	EnginePostgres Engine = "postgres"
	EngineMySQL    Engine = "mysql"
	EngineMariaDB  Engine = "mariadb"
	EngineSQLite   Engine = "sqlite"
)

// ErrEngine reports an engine that is empty or not one of the four. It is the
// same shape `vvdb` uses for the same mistake, so an application that validates
// its configuration and one that opens a source branch on one sentinel.
var ErrEngine = errors.New("crudsql: unknown engine")

// For binds a handle to the engine named, rather than to one of the four
// constructors chosen in source.
//
// The switch has to exist somewhere: a configuration file names an engine and a
// program has to turn that string into a call. Written once here, an application
// that gains SQLite for its tests does not gain a switch of its own that will
// disagree with this one about what "mariadb" means — the distinction that
// exists because MariaDB and MySQL answer a failed CHECK with two different
// numbers ([[D-046]]).
func For(engine Engine, database *sql.DB, options ...Option) (DB, error) {
	switch engine {
	case EnginePostgres:
		return Postgres(database, options...), nil
	case EngineMySQL:
		return MySQL(database, options...), nil
	case EngineMariaDB:
		return MariaDB(database, options...), nil
	case EngineSQLite:
		return SQLite(database, options...), nil
	default:
		return DB{}, fmt.Errorf("%w: %q is not one of postgres, mysql, mariadb, sqlite", ErrEngine, engine)
	}
}

// Wired is [For] with the whole error subsystem in place.
//
// Three pieces have to be wired for a refused write to say what was wrong with
// it, and leaving any of them out is silent — which is why this is one call
// rather than three lines every application copies:
//
//  1. the classifier, so a duplicate address is the code "unique" rather than
//     `ERROR: duplicate key value violates unique constraint "uq_users_email"
//     (SQLSTATE 23505)` — a driver sentence carrying the schema, which is
//     useless to a client and more than it should be told ([[D-044]]);
//  2. the catalog, so the classifier can answer which columns that constraint
//     covers. **PostgreSQL names the constraint and the table on a unique
//     violation and no column at all**, so without it there is nothing to
//     translate and the violation arrives with no field. That is the piece that
//     is easy to miss: everything looks wired, the status is right, and the form
//     has no input to mark;
//  3. `faults.Enrich` on each repository, which turns the column into the model
//     field. That one is per-repository and stays the caller's.
//
// The schema is read here, at start-up, and not lazily: a lazy loader cannot
// fail at start-up, and a schema lookup that quietly returns nothing is how the
// field disappears again ([[D-041]]).
//
// The read goes through a plain source built for that one statement and then
// dropped, because the catalog cannot be read through the source that is waiting
// for it.
func Wired(ctx context.Context, engine Engine, database *sql.DB, options ...Option) (crud.Source, error) {
	plain, err := For(engine, database, options...)
	if err != nil {
		return nil, err
	}

	schema, err := catalog.Load(ctx, plain)
	if err != nil {
		return nil, fmt.Errorf("crudsql: reading the database schema: %w", err)
	}

	classifier := sqlfault.New(string(engine), sqlfault.WithColumns(sqlfault.FromCatalog(schema)))
	return For(engine, database, append(options, WithFaults(classifier))...)
}
