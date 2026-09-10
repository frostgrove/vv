package vvgoose

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/frostgrove/vv/vvdb"
	mysql "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	gooselock "github.com/pressly/goose/v3/lock"
	_ "modernc.org/sqlite"
)

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
			return nil, nil, fmt.Errorf("vvgoose: load migrations from %q: %v; close database: %w", config.Migration.Path, err, closeErr)
		}
		return nil, nil, fmt.Errorf("vvgoose: load migrations from %q: %w", config.Migration.Path, err)
	}
	return provider, database, nil
}

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

// flushScope narrows what a flush drops. The zero value is every schema the
// connected user owns; the two fields are alternatives, never combined.
type flushScope struct {
	defaultOnly bool
	schemas     []string
}

func (scope flushScope) selective() bool {
	return scope.defaultOnly || len(scope.schemas) > 0
}

// flushConfirmer is shown what the flush resolved to and answers whether it may
// proceed. Naming the targets before anything is dropped is the whole point:
// the default scope reaches schemas the caller never mentioned.
type flushConfirmer func(ctx context.Context, targets []string) (bool, error)

// flushResult separates the three ways a flush ends: it dropped what it named,
// it was declined, or the scope matched nothing at all. A caller that cannot
// tell them apart has to stay silent about two of them.
type flushResult struct {
	targets []string
	flushed bool
}

func runFlush(ctx context.Context, raw vvdb.Config, scope flushScope, confirm flushConfirmer) (result flushResult, err error) {
	config := normalizeConfig(&raw)
	if err := config.Validate(); err != nil {
		return result, fmt.Errorf("vvgoose: invalid database config: %w", err)
	}
	if config.Engine != vvdb.Postgres && scope.selective() {
		return result, fmt.Errorf("vvgoose: --schema and --public select PostgreSQL schemas, and %q flushes the database it is connected to", config.Engine)
	}

	primary := config
	primary.Replica = nil
	database, err := vvdb.Open(&primary)
	if err != nil {
		return result, fmt.Errorf("vvgoose: open primary database: %w", err)
	}
	defer joinCloseError(database, &err)

	switch config.Engine {
	case vvdb.Postgres:
		return flushPostgres(ctx, database, scope, confirm)
	case vvdb.MySQL, vvdb.MariaDB:
		return flushConnectedDatabase(ctx, database, confirm, flushMySQL)
	case vvdb.SQLite:
		return flushConnectedDatabase(ctx, database, confirm, flushSQLite)
	default:
		return result, fmt.Errorf("vvgoose: %w: %q", vvdb.ErrEngine, config.Engine)
	}
}

func flushConnectedDatabase(ctx context.Context, database *sql.DB, confirm flushConfirmer, flush func(context.Context, *sql.DB) error) (flushResult, error) {
	result := flushResult{targets: []string{"every object in the connected database"}}
	proceed, err := confirm(ctx, result.targets)
	if err != nil || !proceed {
		return result, err
	}
	if err := flush(ctx, database); err != nil {
		return result, err
	}
	result.flushed = true
	return result, nil
}

func flushPostgres(ctx context.Context, database *sql.DB, scope flushScope, confirm flushConfirmer) (flushResult, error) {
	var result flushResult
	defaultSchema, err := postgresDefaultSchema(ctx, database)
	if err != nil {
		return result, err
	}
	result.targets, err = postgresFlushTargets(ctx, database, scope, defaultSchema)
	if err != nil {
		return result, err
	}
	if len(result.targets) == 0 {
		return result, nil
	}
	proceed, err := confirm(ctx, result.targets)
	if err != nil || !proceed {
		return result, err
	}
	for _, schema := range result.targets {
		quoted := quoteRuntimeIdentifier(vvdb.Postgres, schema)
		if _, err := database.ExecContext(ctx, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
			return result, fmt.Errorf("vvgoose: drop PostgreSQL schema %q: %w", schema, err)
		}
		if schema != defaultSchema {
			continue
		}
		if _, err := database.ExecContext(ctx, "CREATE SCHEMA "+quoted); err != nil {
			return result, fmt.Errorf("vvgoose: recreate PostgreSQL schema %q: %w", schema, err)
		}
	}
	result.flushed = true
	return result, nil
}

func postgresDefaultSchema(ctx context.Context, database *sql.DB) (string, error) {
	var schema string
	if err := database.QueryRowContext(ctx, "SELECT COALESCE(current_schema(), '')").Scan(&schema); err != nil {
		return "", fmt.Errorf("vvgoose: find the PostgreSQL schema to flush: %w", err)
	}
	if schema == "" || postgresSystemSchema(schema) {
		return "", fmt.Errorf("vvgoose: refusing to flush PostgreSQL system schema %q", schema)
	}
	return schema, nil
}

func postgresSystemSchema(name string) bool {
	return name == "information_schema" || strings.HasPrefix(name, "pg_")
}

// postgresFlushTargets resolves the scope against what the database actually
// holds. A schema an extension owns is refused rather than dropped: PostGIS and
// TimescaleDB keep catalogues in schemas of their own, and dropping one breaks
// the extension in a way no migration puts back. Extension ownership is read
// from pg_depend rather than guessed from the name.
func postgresFlushTargets(ctx context.Context, database *sql.DB, scope flushScope, defaultSchema string) ([]string, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT namespace.nspname,
		       EXISTS (
		           SELECT 1
		           FROM pg_depend AS dependency
		           WHERE dependency.classid = 'pg_namespace'::regclass
		             AND dependency.objid = namespace.oid
		             AND dependency.deptype = 'e'
		       )
		FROM pg_namespace AS namespace
		WHERE namespace.nspname <> 'information_schema'
		  AND namespace.nspname NOT LIKE 'pg\_%'
		ORDER BY namespace.nspname`)
	if err != nil {
		return nil, fmt.Errorf("vvgoose: list PostgreSQL schemas to flush: %w", err)
	}
	defer rows.Close()
	var schemas []postgresSchema
	for rows.Next() {
		var schema postgresSchema
		if err := rows.Scan(&schema.name, &schema.extensionOwned); err != nil {
			return nil, fmt.Errorf("vvgoose: read PostgreSQL schemas to flush: %w", err)
		}
		schemas = append(schemas, schema)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("vvgoose: read PostgreSQL schemas to flush: %w", err)
	}
	return resolveFlushTargets(scope, schemas, defaultSchema)
}

type postgresSchema struct {
	name           string
	extensionOwned bool
}

func resolveFlushTargets(scope flushScope, schemas []postgresSchema, defaultSchema string) ([]string, error) {
	if scope.defaultOnly {
		return []string{defaultSchema}, nil
	}
	owned := make(map[string]bool, len(schemas))
	var droppable []string
	for _, schema := range schemas {
		owned[schema.name] = schema.extensionOwned
		if !schema.extensionOwned {
			droppable = append(droppable, schema.name)
		}
	}
	if len(scope.schemas) == 0 {
		return droppable, nil
	}
	var targets []string
	for _, name := range scope.schemas {
		if postgresSystemSchema(name) {
			return nil, fmt.Errorf("vvgoose: refusing to flush PostgreSQL system schema %q", name)
		}
		extensionOwned, exists := owned[name]
		if extensionOwned {
			return nil, fmt.Errorf("vvgoose: schema %q belongs to an extension, and dropping it would break that extension", name)
		}
		if exists {
			targets = append(targets, name)
		}
	}
	return targets, nil
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
