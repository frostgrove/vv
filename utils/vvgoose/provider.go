package vvgoose

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"

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
	config := normalizeConfig(&raw)
	if err := config.Validate(); err != nil {
		return nil, nil, fmt.Errorf("vvgoose: invalid database config: %w", err)
	}

	dialect, err := dialectFor(config.Engine)
	if err != nil {
		return nil, nil, err
	}
	info, err := os.Stat(config.Migration.Path)
	if err != nil {
		return nil, nil, fmt.Errorf("vvgoose: migration directory %q: %w", config.Migration.Path, err)
	}
	if !info.IsDir() {
		return nil, nil, fmt.Errorf("vvgoose: migration path %q is not a directory", config.Migration.Path)
	}

	primary, err := providerDatabaseConfig(config)
	if err != nil {
		return nil, nil, err
	}
	database, err := vvdb.Open(&primary)
	if err != nil {
		return nil, nil, fmt.Errorf("vvgoose: open primary database: %w", err)
	}

	providerOptions := []goose.ProviderOption{
		goose.WithTableName(config.Migration.Table),
		goose.WithDisableGlobalRegistry(true),
	}
	locker, err := providerLockerOption(config.Engine, config.Migration.Table)
	if err != nil {
		return nil, nil, errors.Join(err, database.Close())
	}
	if locker != nil {
		providerOptions = append(providerOptions, locker)
	}

	provider, err := goose.NewProvider(
		dialect,
		database,
		os.DirFS(config.Migration.Path),
		providerOptions...,
	)
	if err != nil {
		closeErr := database.Close()
		if closeErr != nil {
			// Do not wrap ErrNoMigrations in this branch: callers intentionally
			// turn that one condition into a no-op, but a failed close must remain
			// observable instead of being swallowed with it.
			return nil, nil, fmt.Errorf("vvgoose: load migrations from %q: %v; close database: %w", config.Migration.Path, err, closeErr)
		}
		return nil, nil, fmt.Errorf("vvgoose: load migrations from %q: %w", config.Migration.Path, err)
	}
	return provider, database, nil
}

// providerDatabaseConfig makes the connection suitable for Goose without
// mutating the application's ordinary database config. Goose may execute a
// StatementBegin block as one string; go-sql-driver/mysql refuses that unless
// multiStatements is enabled. Goose scans its history timestamp into time.Time,
// so parseTime must also stay on even when a raw DSN disabled it.
func providerDatabaseConfig(config vvdb.Config) (vvdb.Config, error) {
	primary := config
	primary.Replica = nil
	if config.Engine != vvdb.MySQL && config.Engine != vvdb.MariaDB {
		return primary, nil
	}

	if primary.DSN != "" {
		parsed, err := mysql.ParseDSN(string(primary.DSN))
		if err != nil {
			return vvdb.Config{}, vvdb.RedactError("vvgoose: mysql driver rejected the migration DSN", err)
		}
		parsed.MultiStatements = true
		parsed.ParseTime = true
		primary.DSN = vvdb.Secret(parsed.FormatDSN())
		return primary, nil
	}

	params := make(vvdb.Params, len(primary.Params)+1)
	for key, value := range primary.Params {
		params[key] = value
	}
	params["multiStatements"] = "true"
	params["parseTime"] = "true"
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

func (this mysqlSessionLocker) SessionLock(ctx context.Context, conn *sql.Conn) error {
	var acquired sql.NullInt64
	if err := conn.QueryRowContext(ctx, "SELECT GET_LOCK(?, 300)", this.name).Scan(&acquired); err != nil {
		return fmt.Errorf("vvgoose: acquire MySQL migration lock: %w", err)
	}
	if !acquired.Valid || acquired.Int64 != 1 {
		return fmt.Errorf("vvgoose: MySQL migration lock %q was not acquired", this.name)
	}
	return nil
}

func (this mysqlSessionLocker) SessionUnlock(ctx context.Context, conn *sql.Conn) error {
	var released sql.NullInt64
	if err := conn.QueryRowContext(ctx, "SELECT RELEASE_LOCK(?)", this.name).Scan(&released); err != nil {
		return fmt.Errorf("vvgoose: release MySQL migration lock: %w", err)
	}
	if !released.Valid || released.Int64 != 1 {
		return fmt.Errorf("vvgoose: MySQL migration lock %q was not released", this.name)
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

func runMigrate(ctx context.Context, config vvdb.Config) (results []*goose.MigrationResult, err error) {
	provider, database, err := newProvider(config)
	if err != nil {
		if errors.Is(err, goose.ErrNoMigrations) {
			return nil, nil
		}
		return nil, err
	}
	defer joinCloseError(database, &err)

	return provider.Up(ctx)
}

// runFresh rolls every known migration back and applies the complete set
// again. This follows Goose reset semantics: it executes Down sections rather
// than dropping arbitrary tables that are not owned by migrations.
func runFresh(ctx context.Context, config vvdb.Config) (results []*goose.MigrationResult, err error) {
	provider, database, err := newProvider(config)
	if err != nil {
		if errors.Is(err, goose.ErrNoMigrations) {
			return nil, nil
		}
		return nil, err
	}
	defer joinCloseError(database, &err)

	down, err := provider.DownTo(ctx, 0)
	results = append(results, down...)
	if err != nil {
		return results, err
	}
	up, err := provider.Up(ctx)
	results = append(results, up...)
	return results, err
}

// runFlush drops all application objects from the active development database
// without consulting migration files or Goose history. It is the recovery
// hatch for a history table that names a migration file that no longer exists.
// It intentionally does not run Up afterwards: the caller can inspect the
// empty database, or run migrate explicitly.
func runFlush(ctx context.Context, raw vvdb.Config) (err error) {
	config := normalizeConfig(&raw)
	if err := config.Validate(); err != nil {
		return fmt.Errorf("vvgoose: invalid database config: %w", err)
	}

	primary := config
	primary.Replica = nil
	database, err := vvdb.Open(&primary)
	if err != nil {
		return fmt.Errorf("vvgoose: open primary database: %w", err)
	}
	defer joinCloseError(database, &err)

	switch config.Engine {
	case vvdb.Postgres:
		return flushPostgres(ctx, database)
	case vvdb.MySQL, vvdb.MariaDB:
		return flushMySQL(ctx, database)
	case vvdb.SQLite:
		return flushSQLite(ctx, database)
	default:
		return fmt.Errorf("vvgoose: %w: %q", vvdb.ErrEngine, config.Engine)
	}
}

func flushPostgres(ctx context.Context, database *sql.DB) error {
	var schema string
	if err := database.QueryRowContext(ctx, "SELECT current_schema()").Scan(&schema); err != nil {
		return fmt.Errorf("vvgoose: find PostgreSQL schema to flush: %w", err)
	}
	if schema == "" || schema == "information_schema" || strings.HasPrefix(schema, "pg_") {
		return fmt.Errorf("vvgoose: refusing to flush PostgreSQL system schema %q", schema)
	}

	quoted := quoteRuntimeIdentifier(vvdb.Postgres, schema)
	if _, err := database.ExecContext(ctx, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
		return fmt.Errorf("vvgoose: drop PostgreSQL schema %q: %w", schema, err)
	}
	if _, err := database.ExecContext(ctx, "CREATE SCHEMA "+quoted); err != nil {
		return fmt.Errorf("vvgoose: recreate PostgreSQL schema %q: %w", schema, err)
	}
	return nil
}

func flushMySQL(ctx context.Context, database *sql.DB) (err error) {
	conn, err := database.Conn(ctx)
	if err != nil {
		return fmt.Errorf("vvgoose: acquire MySQL flush connection: %w", err)
	}
	defer func() { err = errors.Join(err, conn.Close()) }()

	if _, err = conn.ExecContext(ctx, "SET FOREIGN_KEY_CHECKS = 0"); err != nil {
		return fmt.Errorf("vvgoose: disable MySQL foreign-key checks: %w", err)
	}
	defer func() {
		if _, restoreErr := conn.ExecContext(ctx, "SET FOREIGN_KEY_CHECKS = 1"); restoreErr != nil {
			err = errors.Join(err, fmt.Errorf("vvgoose: restore MySQL foreign-key checks: %w", restoreErr))
		}
	}()

	rows, err := conn.QueryContext(ctx, `
		SELECT table_name, table_type
		FROM information_schema.tables
		WHERE table_schema = DATABASE()
		ORDER BY CASE table_type WHEN 'VIEW' THEN 0 ELSE 1 END, table_name`)
	if err != nil {
		return fmt.Errorf("vvgoose: list MySQL objects to flush: %w", err)
	}
	objects, err := scanFlushObjects(rows)
	if err != nil {
		return err
	}
	if err := dropMySQLObjects(ctx, conn, objects); err != nil {
		return err
	}

	rows, err = conn.QueryContext(ctx, `
		SELECT routine_name, routine_type
		FROM information_schema.routines
		WHERE routine_schema = DATABASE()
		ORDER BY routine_name`)
	if err != nil {
		return fmt.Errorf("vvgoose: list MySQL routines to flush: %w", err)
	}
	objects, err = scanFlushObjects(rows)
	if err != nil {
		return err
	}
	if err := dropMySQLObjects(ctx, conn, objects); err != nil {
		return err
	}

	rows, err = conn.QueryContext(ctx, `
		SELECT event_name, 'EVENT'
		FROM information_schema.events
		WHERE event_schema = DATABASE()
		ORDER BY event_name`)
	if err != nil {
		return fmt.Errorf("vvgoose: list MySQL events to flush: %w", err)
	}
	objects, err = scanFlushObjects(rows)
	if err != nil {
		return err
	}
	if err := dropMySQLObjects(ctx, conn, objects); err != nil {
		return err
	}
	return nil
}

func dropMySQLObjects(ctx context.Context, conn *sql.Conn, objects []flushObject) error {
	for _, object := range objects {
		statement, label := "", strings.ToLower(object.kind)
		switch object.kind {
		case "BASE TABLE":
			statement = "DROP TABLE IF EXISTS "
		case "VIEW":
			statement = "DROP VIEW IF EXISTS "
		case "PROCEDURE":
			statement = "DROP PROCEDURE IF EXISTS "
		case "FUNCTION":
			statement = "DROP FUNCTION IF EXISTS "
		case "EVENT":
			statement = "DROP EVENT IF EXISTS "
		default:
			return fmt.Errorf("vvgoose: unsupported MySQL object %q while flushing", object.kind)
		}
		if _, err := conn.ExecContext(ctx, statement+quoteRuntimeIdentifier(vvdb.MySQL, object.name)); err != nil {
			return fmt.Errorf("vvgoose: drop MySQL %s %q: %w", label, object.name, err)
		}
	}
	return nil
}

func flushSQLite(ctx context.Context, database *sql.DB) (err error) {
	conn, err := database.Conn(ctx)
	if err != nil {
		return fmt.Errorf("vvgoose: acquire SQLite flush connection: %w", err)
	}
	defer func() { err = errors.Join(err, conn.Close()) }()

	if _, err = conn.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
		return fmt.Errorf("vvgoose: disable SQLite foreign keys: %w", err)
	}
	defer func() {
		if _, restoreErr := conn.ExecContext(ctx, "PRAGMA foreign_keys = ON"); restoreErr != nil {
			err = errors.Join(err, fmt.Errorf("vvgoose: restore SQLite foreign keys: %w", restoreErr))
		}
	}()

	rows, err := conn.QueryContext(ctx, `
		SELECT name, type
		FROM sqlite_master
		WHERE type IN ('table', 'view', 'trigger') AND name NOT LIKE 'sqlite_%'
		ORDER BY CASE type WHEN 'trigger' THEN 0 WHEN 'view' THEN 1 ELSE 2 END, name`)
	if err != nil {
		return fmt.Errorf("vvgoose: list SQLite objects to flush: %w", err)
	}
	objects, err := scanFlushObjects(rows)
	if err != nil {
		return err
	}
	for _, object := range objects {
		statement := "DROP " + strings.ToUpper(object.kind) + " IF EXISTS "
		if _, err := conn.ExecContext(ctx, statement+quoteRuntimeIdentifier(vvdb.SQLite, object.name)); err != nil {
			return fmt.Errorf("vvgoose: drop SQLite %s %q: %w", object.kind, object.name, err)
		}
	}
	return nil
}

type flushObject struct {
	name string
	kind string
}

func scanFlushObjects(rows *sql.Rows) ([]flushObject, error) {
	defer rows.Close()

	var objects []flushObject
	for rows.Next() {
		var object flushObject
		if err := rows.Scan(&object.name, &object.kind); err != nil {
			return nil, fmt.Errorf("vvgoose: read database object to flush: %w", err)
		}
		objects = append(objects, object)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("vvgoose: list database objects to flush: %w", err)
	}
	return objects, nil
}

func quoteRuntimeIdentifier(engine vvdb.Engine, name string) string {
	if engine == vvdb.MySQL || engine == vvdb.MariaDB {
		return "`" + strings.ReplaceAll(name, "`", "``") + "`"
	}
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func runStatus(ctx context.Context, config vvdb.Config) (statuses []*goose.MigrationStatus, err error) {
	provider, database, err := newProvider(config)
	if err != nil {
		if errors.Is(err, goose.ErrNoMigrations) {
			return nil, nil
		}
		return nil, err
	}
	defer joinCloseError(database, &err)

	return provider.Status(ctx)
}

func runRollback(ctx context.Context, config vvdb.Config, count int) (results []*goose.MigrationResult, err error) {
	if count < 1 {
		return nil, fmt.Errorf("vvgoose: rollback count must be at least 1, got %d", count)
	}

	provider, database, err := newProvider(config)
	if err != nil {
		if errors.Is(err, goose.ErrNoMigrations) {
			return nil, nil
		}
		return nil, err
	}
	defer joinCloseError(database, &err)

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

func joinCloseError(database *sql.DB, target *error) {
	*target = errors.Join(*target, database.Close())
}
