package vvgoose

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"os"

	"github.com/frostgrove/vv/utils/vvdb"
	mysql "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	gooselock "github.com/pressly/goose/v3/lock"
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
	info, err := os.Stat(cfg.Migration.Path)
	if err != nil {
		return nil, nil, fmt.Errorf("vvgoose: migration directory %q: %w", cfg.Migration.Path, err)
	}
	if !info.IsDir() {
		return nil, nil, fmt.Errorf("vvgoose: migration path %q is not a directory", cfg.Migration.Path)
	}

	primary, err := providerDatabaseConfig(cfg)
	if err != nil {
		return nil, nil, err
	}
	db, err := vvdb.Open(primary)
	if err != nil {
		return nil, nil, fmt.Errorf("vvgoose: open primary database: %w", err)
	}

	providerOptions := []goose.ProviderOption{
		goose.WithTableName(cfg.Migration.Table),
		goose.WithDisableGlobalRegistry(true),
	}
	locker, err := providerLockerOption(cfg.Engine, cfg.Migration.Table)
	if err != nil {
		return nil, nil, errors.Join(err, db.Close())
	}
	if locker != nil {
		providerOptions = append(providerOptions, locker)
	}

	provider, err := goose.NewProvider(
		dialect,
		db,
		os.DirFS(cfg.Migration.Path),
		providerOptions...,
	)
	if err != nil {
		closeErr := db.Close()
		if closeErr != nil {
			// Do not wrap ErrNoMigrations in this branch: callers intentionally
			// turn that one condition into a no-op, but a failed close must remain
			// observable instead of being swallowed with it.
			return nil, nil, fmt.Errorf("vvgoose: load migrations from %q: %v; close database: %w", cfg.Migration.Path, err, closeErr)
		}
		return nil, nil, fmt.Errorf("vvgoose: load migrations from %q: %w", cfg.Migration.Path, err)
	}
	return provider, db, nil
}

// providerDatabaseConfig makes the connection suitable for Goose without
// mutating the application's ordinary database config. Goose may execute a
// StatementBegin block as one string; go-sql-driver/mysql refuses that unless
// multiStatements is enabled.
func providerDatabaseConfig(cfg vvdb.Config) (vvdb.Config, error) {
	primary := cfg
	primary.Replica = nil
	if cfg.Engine != vvdb.MySQL && cfg.Engine != vvdb.MariaDB {
		return primary, nil
	}

	if primary.DSN != "" {
		parsed, err := mysql.ParseDSN(primary.DSN)
		if err != nil {
			return vvdb.Config{}, fmt.Errorf("vvgoose: parse MySQL DSN: %w", err)
		}
		parsed.MultiStatements = true
		parsed.ParseTime = true
		primary.DSN = parsed.FormatDSN()
		return primary, nil
	}

	params := make(vvdb.Params, len(primary.Params)+1)
	for key, value := range primary.Params {
		params[key] = value
	}
	params["multiStatements"] = "true"
	primary.Params = params
	return primary, nil
}

func providerLockerOption(engine vvdb.Engine, table string) (goose.ProviderOption, error) {
	switch engine {
	case vvdb.Postgres:
		locker, err := gooselock.NewPostgresSessionLocker()
		if err != nil {
			return nil, fmt.Errorf("vvgoose: create PostgreSQL migration locker: %w", err)
		}
		return goose.WithSessionLocker(locker), nil
	case vvdb.MySQL, vvdb.MariaDB:
		sum := sha256.Sum256([]byte(table))
		return goose.WithSessionLocker(mysqlSessionLocker{name: fmt.Sprintf("vvgoose:%x", sum[:20])}), nil
	default:
		// SQLite serializes schema writers through the database file itself. The
		// Goose lock package has no SQLite session-lock primitive.
		return nil, nil
	}
}

type mysqlSessionLocker struct{ name string }

func (l mysqlSessionLocker) SessionLock(ctx context.Context, conn *sql.Conn) error {
	var acquired sql.NullInt64
	if err := conn.QueryRowContext(ctx, "SELECT GET_LOCK(?, 300)", l.name).Scan(&acquired); err != nil {
		return fmt.Errorf("vvgoose: acquire MySQL migration lock: %w", err)
	}
	if !acquired.Valid || acquired.Int64 != 1 {
		return fmt.Errorf("vvgoose: MySQL migration lock %q was not acquired", l.name)
	}
	return nil
}

func (l mysqlSessionLocker) SessionUnlock(ctx context.Context, conn *sql.Conn) error {
	var released sql.NullInt64
	if err := conn.QueryRowContext(ctx, "SELECT RELEASE_LOCK(?)", l.name).Scan(&released); err != nil {
		return fmt.Errorf("vvgoose: release MySQL migration lock: %w", err)
	}
	if !released.Valid || released.Int64 != 1 {
		return fmt.Errorf("vvgoose: MySQL migration lock %q was not released", l.name)
	}
	return nil
}

var _ gooselock.SessionLocker = mysqlSessionLocker{}

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
