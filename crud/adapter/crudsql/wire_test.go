package crudsql

import (
	"errors"
	"testing"

	"github.com/frostgrove/vv/crud"
)

// The engine switch is the one place a configuration string becomes a call, and
// getting it wrong is silent in a particular way: MariaDB and MySQL share a
// driver, a dialect and a wire protocol, and answer a failed CHECK with two
// different numbers. An application that resolved "mariadb" to the MySQL
// constructor would look entirely correct until a CHECK failed.

func TestEveryEngineResolvesToItsOwnClassifier(t *testing.T) {
	for _, tc := range []struct {
		engine  Engine
		dialect crud.Dialect
	}{
		{EnginePostgres, crud.Postgres{}},
		{EngineMySQL, crud.MySQL{}},
		{EngineMariaDB, crud.MySQL{}},
		{EngineSQLite, crud.SQLite{}},
	} {
		t.Run(string(tc.engine), func(t *testing.T) {
			db, err := For(tc.engine, nil)
			if err != nil {
				t.Fatalf("%q is one of the four and did not resolve: %v", tc.engine, err)
			}
			if got, want := db.Dialect().Name(), tc.dialect.Name(); got != want {
				t.Fatalf("%q writes %s SQL, want %s", tc.engine, got, want)
			}
			// MariaDB and MySQL share a dialect, so the check above cannot tell
			// them apart. What differs is the engine the classifier keys on, and
			// that pair is the one a switch collapses by accident.
			named, ok := db.faults.(interface{ Engine() string })
			if !ok || named.Engine() != string(tc.engine) {
				t.Fatalf("%q resolved to a classifier for %v, so a refused write is classified against the wrong engine",
					tc.engine, db.faults)
			}
		})
	}
}

func TestAnEngineNobodySupportsIsRefusedRatherThanGuessed(t *testing.T) {
	for _, engine := range []Engine{"", "postgresql", "PostgreSQL", "cockroach"} {
		if _, err := For(engine, nil); !errors.Is(err, ErrEngine) {
			t.Fatalf("%q was accepted as an engine (%v); a typo that resolves to a default writes to the right database with the wrong classifier", engine, err)
		}
	}
}
