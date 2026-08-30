//go:build integration

package integration

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/adapter/crudsql"
	"github.com/frostgrove/vv/crud/catalog"
	"github.com/frostgrove/vv/crud/probe"
	"github.com/frostgrove/vv/crud/sqlrepo"
)

func TestMySQLDatabaseQualifierAndSQLiteAttachedDatabaseAreLive(t *testing.T) {
	ctx := context.Background()

	for _, test := range []struct {
		name     string
		database *sql.DB
		source   crud.Source
	}{
		{"mysql database", myDB, crudsql.MySQL(myDB)},
		{"mariadb database", mariaDB, crudsql.MySQL(mariaDB)},
	} {
		t.Run(test.name, func(t *testing.T) {
			var databaseName string
			if err := test.database.QueryRowContext(ctx, `SELECT DATABASE()`).Scan(&databaseName); err != nil {
				t.Fatal(err)
			}
			if databaseName == "" {
				t.Fatal("connection has no current database")
			}
			if _, err := test.database.ExecContext(ctx, `DROP TABLE IF EXISTS core028_events`); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _, _ = test.database.ExecContext(context.Background(), `DROP TABLE IF EXISTS core028_events`) })
			if _, err := test.database.ExecContext(ctx, `CREATE TABLE core028_events (
				id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
				label VARCHAR(255) NOT NULL
			) ENGINE=InnoDB`); err != nil {
				t.Fatal(err)
			}

			bp, err := sqlrepo.TryDefineInSchema[core028Event, int64, core028EventUpdate](
				databaseName, "core028_events", sqlrepo.IndependentTable())
			if err != nil {
				t.Fatal(err)
			}
			repository := bp.Bind(test.source)
			stored, err := repository.Save(ctx, &core028Event{Label: test.name})
			if err != nil {
				t.Fatal(err)
			}
			if got, err := repository.GetByID(ctx, stored.ID); err != nil || got.Label != test.name {
				t.Fatalf("qualified round trip = %+v, %v", got, err)
			}

			cat, err := catalog.Load(ctx, test.source)
			if err != nil {
				t.Fatal(err)
			}
			qualified := cat.(catalog.QualifiedCatalog)
			ref := crud.TableRef{Schema: databaseName, Name: "core028_events"}
			if table, ok := qualified.TableByRef(ref); !ok || table.Schema != databaseName {
				t.Fatalf("catalog did not resolve current database exactly: %+v", table)
			}
			if _, ok := qualified.TableByRef(crud.TableRef{Schema: "core028_missing_database", Name: "core028_events"}); ok {
				t.Fatal("catalog fell back from a missing database qualifier to the current database")
			}
			meta, err := crud.NewMetaInSchema[core028Event](databaseName, "core028_events")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := probe.Full(cat).(probe.Declarer).Declare(meta); err != nil {
				t.Fatalf("probe refused the current database qualifier: %v", err)
			}
		})
	}

	t.Run("sqlite attached database", func(t *testing.T) {
		database := openSQLite(t)
		attached := filepath.Join(t.TempDir(), "analytics.db")
		if _, err := database.ExecContext(ctx, `ATTACH DATABASE ? AS analytics`, attached); err != nil {
			t.Fatal(err)
		}
		if _, err := database.ExecContext(ctx, `CREATE TABLE analytics.events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			label TEXT NOT NULL
		)`); err != nil {
			t.Fatal(err)
		}
		if _, err := database.ExecContext(ctx, `CREATE TABLE main.events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			label TEXT NOT NULL
		)`); err != nil {
			t.Fatal(err)
		}

		bp, err := sqlrepo.TryDefineInSchema[core028Event, int64, core028EventUpdate](
			"analytics", "events", sqlrepo.IndependentTable())
		if err != nil {
			t.Fatal(err)
		}
		repository := bp.Bind(crudsql.SQLite(database))
		stored, err := repository.Save(ctx, &core028Event{Label: "sqlite attach"})
		if err != nil {
			t.Fatal(err)
		}
		if got, err := repository.GetByID(ctx, stored.ID); err != nil || got.Label != "sqlite attach" {
			t.Fatalf("qualified round trip = %+v, %v", got, err)
		}

		cat, err := catalog.Load(ctx, crudsql.SQLite(database))
		if err != nil {
			t.Fatal(err)
		}
		qualified := cat.(catalog.QualifiedCatalog)
		if table, ok := qualified.TableByRef(crud.TableRef{Schema: "main", Name: "events"}); !ok || table.Schema != "main" {
			t.Fatalf("catalog did not resolve main.events: %+v", table)
		}
		if _, ok := qualified.TableByRef(crud.TableRef{Schema: "analytics", Name: "events"}); ok {
			t.Fatal("SQLite catalog silently merged attached analytics.events into main.events")
		}
		meta, err := crud.NewMetaInSchema[core028Event]("analytics", "events")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := probe.Full(cat).(probe.Declarer).Declare(meta); !errors.Is(err, probe.ErrUnknownTable) {
			t.Fatalf("attached-database probe answered %v, want the typed ErrUnknownTable refusal", err)
		}
	})
}
