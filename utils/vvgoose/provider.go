package vvgoose

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"

	"github.com/frostgrove/vv/utils/vvdb"
	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"
)

// newProvider builds one isolated Goose provider over the configured primary
// database. A read replica is deliberately not opened: schema changes belong
// to the primary and replication is responsible for carrying them downstream.
//
// The returned database belongs to the caller and must be closed. Returning it
// separately keeps that ownership visible; Provider.Close would close the same
// handle but obscures who created it.
func newProvider(raw vvdb.Config) (*goose.Provider, *sql.DB, error) {
	cfg := normalizeConfig(raw)
	if err := cfg.Validate(); err != nil {
		return nil, nil, fmt.Errorf("vvgoose: invalid database config: %w", err)
	}

	dialect, err := dialectFor(cfg.Engine)
	if err != nil {
		return nil, nil, err
	}
	if err := os.MkdirAll(cfg.Migration.Path, 0o755); err != nil {
		return nil, nil, fmt.Errorf("vvgoose: create migration directory %q: %w", cfg.Migration.Path, err)
	}

	primary := cfg
	primary.Replica = nil
	db, err := vvdb.Open(primary)
	if err != nil {
		return nil, nil, fmt.Errorf("vvgoose: open primary database: %w", err)
	}

	provider, err := goose.NewProvider(
		dialect,
		db,
		os.DirFS(cfg.Migration.Path),
		goose.WithTableName(cfg.Migration.Table),
		goose.WithDisableGlobalRegistry(true),
	)
	if err != nil {
		return nil, nil, errors.Join(
			fmt.Errorf("vvgoose: load migrations from %q: %w", cfg.Migration.Path, err),
			db.Close(),
		)
	}
	return provider, db, nil
}

func dialectFor(engine vvdb.Engine) (goose.Dialect, error) {
	switch engine {
	case vvdb.Postgres:
		return goose.DialectPostgres, nil
	case vvdb.MySQL, vvdb.MariaDB:
		return goose.DialectMySQL, nil
	case vvdb.SQLite:
		return goose.DialectSQLite3, nil
	default:
		return goose.DialectCustom, fmt.Errorf("vvgoose: %w: %q", vvdb.ErrEngine, engine)
	}
}

func runMigrate(ctx context.Context, cfg vvdb.Config) (results []*goose.MigrationResult, err error) {
	provider, db, err := newProvider(cfg)
	if err != nil {
		if errors.Is(err, goose.ErrNoMigrations) {
			return nil, nil
		}
		return nil, err
	}
	defer joinCloseError(db, &err)

	return provider.Up(ctx)
}

// runFresh rolls every known migration back and applies the complete set
// again. This follows Goose reset semantics: it executes Down sections rather
// than dropping arbitrary tables that are not owned by migrations.
func runFresh(ctx context.Context, cfg vvdb.Config) (results []*goose.MigrationResult, err error) {
	provider, db, err := newProvider(cfg)
	if err != nil {
		if errors.Is(err, goose.ErrNoMigrations) {
			return nil, nil
		}
		return nil, err
	}
	defer joinCloseError(db, &err)

	down, err := provider.DownTo(ctx, 0)
	results = append(results, down...)
	if err != nil {
		return results, err
	}
	up, err := provider.Up(ctx)
	results = append(results, up...)
	return results, err
}

func runStatus(ctx context.Context, cfg vvdb.Config) (statuses []*goose.MigrationStatus, err error) {
	provider, db, err := newProvider(cfg)
	if err != nil {
		if errors.Is(err, goose.ErrNoMigrations) {
			return nil, nil
		}
		return nil, err
	}
	defer joinCloseError(db, &err)

	return provider.Status(ctx)
}

func runRollback(ctx context.Context, cfg vvdb.Config, count int) (results []*goose.MigrationResult, err error) {
	if count < 1 {
		return nil, fmt.Errorf("vvgoose: rollback count must be at least 1, got %d", count)
	}

	provider, db, err := newProvider(cfg)
	if err != nil {
		if errors.Is(err, goose.ErrNoMigrations) {
			return nil, nil
		}
		return nil, err
	}
	defer joinCloseError(db, &err)

	for range count {
		result, downErr := provider.Down(ctx)
		if errors.Is(downErr, goose.ErrNoNextVersion) {
			return results, nil
		}
		if result != nil {
			results = append(results, result)
		}
		if downErr != nil {
			return results, downErr
		}
	}
	return results, nil
}

func joinCloseError(db *sql.DB, target *error) {
	*target = errors.Join(*target, db.Close())
}
