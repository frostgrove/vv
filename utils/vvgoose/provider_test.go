package vvgoose

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/frostgrove/vv/utils/vvdb"
	mysql "github.com/go-sql-driver/mysql"
	"github.com/pressly/goose/v3"
)

func TestDialectFor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		engine vvdb.Engine
		want   goose.Dialect
	}{
		{engine: vvdb.Postgres, want: goose.DialectPostgres},
		{engine: vvdb.MySQL, want: goose.DialectMySQL},
		{engine: vvdb.MariaDB, want: goose.DialectMySQL},
		{engine: vvdb.SQLite, want: goose.DialectSQLite3},
	}
	for _, test := range tests {
		t.Run(string(test.engine), func(t *testing.T) {
			t.Parallel()
			got, err := dialectFor(test.engine)
			if err != nil {
				t.Fatalf("dialectFor(%q): %v", test.engine, err)
			}
			if got != test.want {
				t.Fatalf("dialectFor(%q) = %q, want %q", test.engine, got, test.want)
			}
		})
	}

	if _, err := dialectFor("oracle"); !errors.Is(err, vvdb.ErrEngine) {
		t.Fatalf("dialectFor(oracle) error = %v, want vvdb.ErrEngine", err)
	}
}

func TestSQLiteProviderLifecycle(t *testing.T) {
	t.Parallel()

	cfg := sqliteMigrationConfig(t)
	ctx := context.Background()

	statuses, err := runStatus(ctx, cfg)
	if err != nil {
		t.Fatalf("initial status: %v", err)
	}
	assertMigrationState(t, statuses, goose.StatePending)

	up, err := runMigrate(ctx, cfg)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if len(up) != 1 || up[0].Direction != "up" {
		t.Fatalf("migrate results = %+v, want one up migration", up)
	}

	statuses, err = runStatus(ctx, cfg)
	if err != nil {
		t.Fatalf("status after migrate: %v", err)
	}
	assertMigrationState(t, statuses, goose.StateApplied)

	rolledBack, err := runRollback(ctx, cfg, 1)
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if len(rolledBack) != 1 || rolledBack[0].Direction != "down" {
		t.Fatalf("rollback results = %+v, want one down migration", rolledBack)
	}

	statuses, err = runStatus(ctx, cfg)
	if err != nil {
		t.Fatalf("status after rollback: %v", err)
	}
	assertMigrationState(t, statuses, goose.StatePending)

	// Rolling back an already empty database is an intentional no-op.
	rolledBack, err = runRollback(ctx, cfg, 2)
	if err != nil {
		t.Fatalf("rollback at version zero: %v", err)
	}
	if len(rolledBack) != 0 {
		t.Fatalf("rollback at version zero returned %d results, want none", len(rolledBack))
	}
}

func TestFreshReappliesMigrations(t *testing.T) {
	t.Parallel()

	cfg := sqliteMigrationConfig(t)
	ctx := context.Background()
	if _, err := runMigrate(ctx, cfg); err != nil {
		t.Fatalf("initial migrate: %v", err)
	}

	db, err := vvdb.Open(&cfg)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO users (name) VALUES ('temporary')`); err != nil {
		_ = db.Close()
		t.Fatalf("insert temporary row: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close sqlite: %v", err)
	}

	results, err := runFresh(ctx, cfg)
	if err != nil {
		t.Fatalf("fresh: %v", err)
	}
	if len(results) != 2 || results[0].Direction != "down" || results[1].Direction != "up" {
		t.Fatalf("fresh results = %+v, want down followed by up", results)
	}

	db, err = vvdb.Open(&cfg)
	if err != nil {
		t.Fatalf("reopen sqlite: %v", err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&count); err != nil {
		t.Fatalf("count users after fresh: %v", err)
	}
	if count != 1 {
		t.Fatalf("users after fresh = %d, want the one seeded by the migration", count)
	}
}

func TestFlushDropsEverySQLiteObjectAndMigrationHistory(t *testing.T) {
	t.Parallel()

	cfg := sqliteMigrationConfig(t)
	ctx := context.Background()
	if _, err := runMigrate(ctx, cfg); err != nil {
		t.Fatalf("initial migrate: %v", err)
	}

	db, err := vvdb.Open(&cfg)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE untracked (id INTEGER PRIMARY KEY)`); err != nil {
		_ = db.Close()
		t.Fatalf("create untracked table: %v", err)
	}
	if _, err := db.ExecContext(ctx, `CREATE VIEW user_names AS SELECT name FROM users`); err != nil {
		_ = db.Close()
		t.Fatalf("create untracked view: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close sqlite: %v", err)
	}

	if err := runFlush(ctx, cfg); err != nil {
		t.Fatalf("flush: %v", err)
	}

	db, err = vvdb.Open(&cfg)
	if err != nil {
		t.Fatalf("reopen sqlite: %v", err)
	}
	var objects int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type IN ('table', 'view', 'trigger') AND name NOT LIKE 'sqlite_%'`).Scan(&objects); err != nil {
		_ = db.Close()
		t.Fatalf("count objects after flush: %v", err)
	}
	if objects != 0 {
		_ = db.Close()
		t.Fatalf("objects after flush = %d, want 0", objects)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close flushed sqlite: %v", err)
	}

	results, err := runMigrate(ctx, cfg)
	if err != nil {
		t.Fatalf("migrate after flush: %v", err)
	}
	if len(results) != 1 || results[0].Direction != "up" {
		t.Fatalf("migrate after flush results = %+v, want one up migration", results)
	}
}

func TestRollbackRejectsNonPositiveCount(t *testing.T) {
	t.Parallel()

	for _, count := range []int{-1, 0} {
		if _, err := runRollback(context.Background(), vvdb.Config{}, count); err == nil {
			t.Fatalf("runRollback count %d succeeded, want an error", count)
		}
	}
}

func TestRollbackHonoursCount(t *testing.T) {
	t.Parallel()

	cfg := sqliteMigrationConfig(t)
	const second = `-- +goose Up
CREATE TABLE teams (id INTEGER PRIMARY KEY);

-- +goose Down
DROP TABLE teams;
`
	if err := os.WriteFile(filepath.Join(cfg.Migration.Path, "20260827000001_create_teams.sql"), []byte(second), 0o644); err != nil {
		t.Fatal(err)
	}
	if results, err := runMigrate(context.Background(), cfg); err != nil || len(results) != 2 {
		t.Fatalf("migrate two files = %v, %v", results, err)
	}
	results, err := runRollback(context.Background(), cfg, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 || results[0].Source.Version != 20260827000001 || results[1].Source.Version != 20260827000000 {
		t.Fatalf("rollback results = %+v, want both versions newest first", results)
	}
}

func TestAnExistingDirectoryWithNoMigrationFilesHasNothingToDo(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	migrations := filepath.Join(root, "migrations")
	if err := os.Mkdir(migrations, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := vvdb.Config{
		Engine: vvdb.SQLite,
		Path:   filepath.Join(root, "app.sqlite"),
		Migration: vvdb.Migration{
			Path: migrations,
		},
	}
	ctx := context.Background()
	if statuses, err := runStatus(ctx, cfg); err != nil || len(statuses) != 0 {
		t.Fatalf("empty status = %v, %v; want no migrations", statuses, err)
	}
	if results, err := runMigrate(ctx, cfg); err != nil || len(results) != 0 {
		t.Fatalf("empty migrate = %v, %v; want nothing to do", results, err)
	}
	if _, err := os.Stat(cfg.Migration.Path); err != nil {
		t.Fatalf("migration directory disappeared: %v", err)
	}
}

func TestRuntimeCommandsRefuseAMissingMigrationDirectory(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	cfg := vvdb.Config{
		Engine: vvdb.SQLite,
		Path:   filepath.Join(root, "app.sqlite"),
		Migration: vvdb.Migration{
			Path: filepath.Join(root, "misspelled-migrations"),
		},
	}
	if _, err := runMigrate(context.Background(), cfg); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing migration path error = %v, want os.ErrNotExist", err)
	}
	if _, err := os.Stat(cfg.Migration.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("runtime command created a typoed path: %v", err)
	}
}

func TestProviderEnablesMySQLMultiStatementsWithoutMutatingTheApplicationConfig(t *testing.T) {
	t.Parallel()
	structured := vvdb.Config{
		Engine: vvdb.MySQL,
		Host:   "localhost",
		User:   "app",
		Name:   "app",
		Params: vvdb.Params{"multiStatements": "false"},
	}
	raw := vvdb.Config{
		Engine: vvdb.MySQL,
		DSN:    "app:secret@tcp(localhost:3306)/app?multiStatements=false",
	}
	for name, cfg := range map[string]vvdb.Config{"structured": structured, "raw dsn": raw} {
		t.Run(name, func(t *testing.T) {
			got, err := providerDatabaseConfig(cfg)
			if err != nil {
				t.Fatal(err)
			}
			dsn, err := vvdb.DSN(&got)
			if err != nil {
				t.Fatal(err)
			}
			parsed, err := mysql.ParseDSN(dsn)
			if err != nil {
				t.Fatal(err)
			}
			if !parsed.MultiStatements {
				t.Fatalf("provider DSN = %q, multiStatements is disabled", dsn)
			}
		})
	}
	if structured.Params["multiStatements"] != "false" || raw.DSN != "app:secret@tcp(localhost:3306)/app?multiStatements=false" {
		t.Fatal("provider preparation mutated the application's database config")
	}
}

func sqliteMigrationConfig(t *testing.T) vvdb.Config {
	t.Helper()
	root := t.TempDir()
	migrations := filepath.Join(root, "migrations")
	if err := os.Mkdir(migrations, 0o755); err != nil {
		t.Fatalf("make migrations directory: %v", err)
	}
	const script = `-- +goose Up
CREATE TABLE users (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL
);
INSERT INTO users (name) VALUES ('seed');

-- +goose Down
DROP TABLE users;
`
	if err := os.WriteFile(filepath.Join(migrations, "20260827000000_create_users.sql"), []byte(script), 0o644); err != nil {
		t.Fatalf("write migration: %v", err)
	}
	return vvdb.Config{
		Engine: vvdb.SQLite,
		Path:   filepath.Join(root, "app.sqlite"),
		Migration: vvdb.Migration{
			Path:   migrations,
			Models: []string{"."},
			Table:  "goose_db_version",
		},
	}
}

func assertMigrationState(t *testing.T, statuses []*goose.MigrationStatus, want goose.State) {
	t.Helper()
	if len(statuses) != 1 {
		t.Fatalf("status count = %d, want 1", len(statuses))
	}
	if statuses[0].State != want {
		t.Fatalf("migration state = %q, want %q", statuses[0].State, want)
	}
}
