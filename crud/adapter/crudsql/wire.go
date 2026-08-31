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

type Engine string

const (
	EnginePostgres Engine = "postgres"
	EngineMySQL    Engine = "mysql"
	EngineMariaDB  Engine = "mariadb"
	EngineSQLite   Engine = "sqlite"
)

var ErrEngine = errors.New("crudsql: unknown engine")

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
