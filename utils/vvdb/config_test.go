package vvdb_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/frostgrove/vv/utils/vvdb"
)

func primary() vvdb.Config {
	return vvdb.Config{
		Engine: vvdb.Postgres, Host: "primary.internal", Port: 5432,
		User: "vv", Password: "s3cret", Name: "app", SSLMode: "require",
		Params: map[string]string{"application_name": "orders"},
		Pool:   vvdb.Pool{MaxOpen: 20},
	}
}

func TestAReplicaInheritsEverythingItDoesNotRestate(t *testing.T) {
	config := primary()
	config.Replica = &vvdb.Config{Host: "replica.internal"}

	r, ok := config.ReadReplica()
	if !ok {
		t.Fatal("a declared replica should be returned")
	}
	if r.Host != "replica.internal" {
		t.Errorf("the replica should keep its own host, got %q", r.Host)
	}
	// The credentials are the failure this inheritance exists to prevent: two
	// copies drift the day one of them is rotated.
	if r.User != "vv" || r.Password != "s3cret" || r.Name != "app" || r.SSLMode != "require" {
		t.Errorf("the replica should inherit what it did not restate, got %+v", r)
	}
	if r.Params["application_name"] != "orders" {
		t.Errorf("params should be inherited too, got %v", r.Params)
	}
	if r.Replica != nil {
		t.Error("a replica of a replica is not a thing this describes")
	}
}

func TestAReplicaOverridesRatherThanMerges(t *testing.T) {
	config := primary()
	config.Replica = &vvdb.Config{Host: "replica.internal", User: "readonly", Params: map[string]string{"application_name": "orders-ro"}}

	r, _ := config.ReadReplica()
	if r.User != "readonly" {
		t.Errorf("a field the replica states is the replica's, got %q", r.User)
	}
	if r.Password != "s3cret" {
		t.Errorf("a field it does not state is still inherited, got %q", r.Password)
	}
	if r.Params["application_name"] != "orders-ro" {
		t.Errorf("a param the replica states wins, got %v", r.Params)
	}
	if config.Params["application_name"] != "orders" {
		t.Error("merging must not write into the primary's own map")
	}
}

func TestAReplicaMergesPoolFieldsAndDoesNotAliasPrimaryParams(t *testing.T) {
	config := primary()
	config.Pool = vvdb.Pool{MaxOpen: 20, MaxIdle: 5, MaxLifetime: time.Minute}
	config.Replica = &vvdb.Config{
		Host: "replica.internal",
		Pool: vvdb.Pool{MaxOpen: 8},
	}
	r, _ := config.ReadReplica()
	if r.Pool.MaxOpen != 8 || r.Pool.MaxIdle != 5 || r.Pool.MaxLifetime != time.Minute {
		t.Fatalf("replica pool = %+v, want its max_open plus inherited limits", r.Pool)
	}
	r.Params["application_name"] = "replica"
	if config.Params["application_name"] != "orders" {
		t.Fatalf("replica params mutated the primary config: %+v", config.Params)
	}
}

func TestMigrationConfigurationValidatesDeclarationWithoutInspectingTheFilesystem(t *testing.T) {
	config := primary()
	// The migration command may create this directory later, and the ordinary
	// server binary need not ship migration sources at all. Config validation is
	// therefore deliberately about the declaration rather than current disk
	// state.
	config.Migration = vvdb.Migration{
		Path:   t.TempDir() + "/not-created",
		Models: []string{".", "./src/app"},
		Table:  "audit.goose_versions",
	}
	if err := config.Validate(); err != nil {
		t.Fatalf("a valid migration declaration whose directory is not created yet = %v", err)
	}

	// A Config assembled in Go does not pass through cleanenv's env-default
	// tags. Its zero value remains valid and is resolved by the migration tool.
	config.Migration = vvdb.Migration{}
	if err := config.Validate(); err != nil {
		t.Fatalf("zero migration configuration should select downstream defaults: %v", err)
	}
}

func TestMigrationConfigurationRefusesBlankPathsAndSQLSyntaxAsHistoryTable(t *testing.T) {
	for _, tc := range []struct {
		name      string
		migration vvdb.Migration
		want      error
		field     string
	}{
		{"blank path", vvdb.Migration{Path: " \t"}, vvdb.ErrMissing, "migration.path"},
		{"blank model directory", vvdb.Migration{Models: []string{".", ""}}, vvdb.ErrMissing, "migration.models[1]"},
		{"leading digit", vvdb.Migration{Table: "2goose"}, vvdb.ErrUnsupported, "migration.table"},
		{"upper case", vvdb.Migration{Table: "GooseVersions"}, vvdb.ErrUnsupported, "migration.table"},
		{"reserved word", vvdb.Migration{Table: "table"}, vvdb.ErrUnsupported, "migration.table"},
		{"dialect keyword returning", vvdb.Migration{Table: "returning"}, vvdb.ErrUnsupported, "migration.table"},
		{"dialect keyword nothing", vvdb.Migration{Table: "nothing"}, vvdb.ErrUnsupported, "migration.table"},
		{"punctuation", vvdb.Migration{Table: "goose-version"}, vvdb.ErrUnsupported, "migration.table"},
		{"empty qualifier", vvdb.Migration{Table: "public..goose"}, vvdb.ErrUnsupported, "migration.table"},
		{"too many qualifiers", vvdb.Migration{Table: "cluster.public.goose"}, vvdb.ErrUnsupported, "migration.table"},
		{"sql syntax", vvdb.Migration{Table: "goose; DROP TABLE users"}, vvdb.ErrUnsupported, "migration.table"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := primary()
			config.Migration = tc.migration
			err := config.Validate()
			if !errors.Is(err, tc.want) || !strings.Contains(err.Error(), tc.field) {
				t.Fatalf("Validate() = %v, want %v naming %s", err, tc.want, tc.field)
			}
		})
	}
}

func TestMigrationConfigurationIsPrimaryOnlyAndDoesNotLeakIntoAReplica(t *testing.T) {
	for _, tc := range []struct {
		name    string
		replica *vvdb.Config
	}{
		{"field replica", &vvdb.Config{Host: "replica.internal"}},
		{"raw dsn replica", &vvdb.Config{DSN: "postgres://readonly@replica.internal/app"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := primary()
			config.Migration = vvdb.Migration{
				Path:   "./migrations",
				Models: []string{"./src/app"},
				Table:  "goose_db_version",
			}
			config.Replica = tc.replica
			if err := config.Validate(); err != nil {
				t.Fatalf("primary migration plus ordinary replica should be valid: %v", err)
			}
			r, ok := config.ReadReplica()
			if !ok {
				t.Fatal("ordinary replica was not returned")
			}
			if r.Migration.Path != "" || len(r.Migration.Models) != 0 || r.Migration.Table != "" {
				t.Fatalf("primary migration configuration leaked into replica: %+v", r.Migration)
			}
		})
	}
}

func TestAReplicaCannotDeclareItsOwnMigrationConfiguration(t *testing.T) {
	for _, replica := range []*vvdb.Config{
		{Migration: vvdb.Migration{Path: "./replica-migrations"}},
		{Host: "replica.internal", Migration: vvdb.Migration{Table: "replica_versions"}},
	} {
		config := primary()
		config.Replica = replica
		err := config.Validate()
		if !errors.Is(err, vvdb.ErrUnsupported) || !strings.Contains(err.Error(), "replica.migration") {
			t.Fatalf("Validate() = %v, want a named primary-only migration refusal", err)
		}
		if _, ok := config.ReadReplica(); ok {
			t.Fatal("ReadReplica offered a replica with its own migration configuration")
		}
	}
}

func TestMigrationConfigurationDoesNotConflictWithAnOpaqueDSN(t *testing.T) {
	config := vvdb.Config{
		Engine: vvdb.Postgres,
		DSN:    "postgres://vv:secret@primary.internal/app",
		Migration: vvdb.Migration{
			Path:  "./migrations",
			Table: "goose_db_version",
		},
	}
	got, err := vvdb.DSN(&config)
	if err != nil {
		t.Fatalf("migration metadata is outside the connection string and must not conflict with dsn: %v", err)
	}
	if got != string(config.DSN) {
		t.Fatalf("DSN() = %q, want opaque string unchanged", got)
	}
}

func TestAReplicaGivenAWholeDSNInheritsTheHandlePolicyNotConnectionFacts(t *testing.T) {
	config := primary()
	config.Driver = "postgres"
	config.Pool = vvdb.Pool{MaxOpen: 20, MaxIdle: 5, MaxLifetime: time.Minute}
	config.Replica = &vvdb.Config{DSN: "postgres://ro:pw@replica.internal:5432/app"}

	r, _ := config.ReadReplica()
	if r.Host != "" || r.User != "" {
		t.Errorf("a finished string cannot be merged into; the fields would only contradict it, got %+v", r)
	}
	if r.Engine != vvdb.Postgres {
		t.Errorf("the engine is still needed to pick a driver, got %q", r.Engine)
	}
	if r.Driver != "postgres" {
		t.Errorf("the replica must inherit the primary database/sql driver, got %q", r.Driver)
	}
	if r.Pool.MaxOpen != 20 || r.Pool.MaxIdle != 5 || r.Pool.MaxLifetime != time.Minute || r.Pool.ConnectTimeout != 0 {
		t.Errorf("the raw replica DSN should inherit only non-DSN pool policy, got %+v", r.Pool)
	}
	got, err := vvdb.DSN(r)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(config.Replica.DSN) {
		t.Errorf("the replica string should be used as given, got %s", got)
	}
}

func TestReadReplicaDoesNotOfferFieldsBesideAnOpaquePrimaryDSN(t *testing.T) {
	config := vvdb.Config{
		Engine: vvdb.Postgres,
		DSN:    "postgres://primary.internal/orders",
		Replica: &vvdb.Config{
			Host: "replica.internal",
		},
	}
	if _, ok := config.ReadReplica(); ok {
		t.Fatal("a field replica cannot be derived from an opaque primary DSN")
	}
	if err := config.Validate(); !errors.Is(err, vvdb.ErrConflict) {
		t.Fatalf("Validate() = %v, want the named opaque-DSN conflict", err)
	}
}

func TestNoReplicaIsNotAnEmptyReplica(t *testing.T) {
	config := primary()
	if _, ok := config.ReadReplica(); ok {
		t.Fatal("a config with no replica must not answer with a usable one — opening it would be a second connection to the primary")
	}
}

func TestAReplicaOfAnotherEngineIsRefused(t *testing.T) {
	config := primary()
	config.Replica = &vvdb.Config{Engine: vvdb.MySQL, Host: "replica.internal"}
	if err := config.Validate(); !errors.Is(err, vvdb.ErrConflict) {
		t.Fatalf("the two would generate different SQL for the same repository; got %v", err)
	}
}

func TestTypedPostgresRefusesLibPQRatherThanLosingItsSingleConfigurationSource(t *testing.T) {
	config := primary()
	config.Driver = "postgres"
	if err := config.Validate(); !errors.Is(err, vvdb.ErrUnsupported) {
		t.Fatalf("typed postgres with lib/pq = %v, want the named unsupported driver", err)
	}

	// A raw DSN is intentionally different: it is the escape hatch where the
	// caller owns every lib/pq option and its interaction with the environment.
	config = vvdb.Config{Engine: vvdb.Postgres, Driver: "postgres", DSN: "postgres://db.internal/orders"}
	if err := config.Validate(); err != nil {
		t.Fatalf("raw lib/pq DSN = %v, want the explicit escape hatch", err)
	}

	// The driver name is not a security proof. An alias can point at lib/pq just
	// as easily, so typed PostgreSQL accepts the one documented pgx name only.
	config = primary()
	config.Driver = "company-postgres"
	if err := config.Validate(); !errors.Is(err, vvdb.ErrUnsupported) {
		t.Fatalf("typed postgres with a custom driver alias = %v, want pgx-only refusal", err)
	}
}

func TestReplicaTopologyIsClosedRatherThanSilentlyRewritten(t *testing.T) {
	config := primary()
	config.Replica = &vvdb.Config{}
	if err := config.Validate(); !errors.Is(err, vvdb.ErrMissing) || !strings.Contains(err.Error(), "replica") {
		t.Fatalf("empty replica Validate() = %v, want a named refusal", err)
	}
	if _, ok := config.ReadReplica(); ok {
		t.Fatal("an empty replica must not derive a second primary configuration")
	}

	config.Replica = &vvdb.Config{Host: "replica", Replica: &vvdb.Config{Host: "third"}}
	if err := config.Validate(); !errors.Is(err, vvdb.ErrUnsupported) || !strings.Contains(err.Error(), "replica.replica") {
		t.Fatalf("nested replica Validate() = %v, want a named topology refusal", err)
	}
}

func TestAReplicaIsValidatedAsItWillBeOpened(t *testing.T) {
	// The control case: the replica fragment on its own has no database name,
	// and validating the fragment rather than the merge would call it invalid.
	config := primary()
	config.Replica = &vvdb.Config{Host: "replica.internal"}
	if err := config.Validate(); err != nil {
		t.Fatalf("a replica that inherits its name is valid: %v", err)
	}

	config.Replica = &vvdb.Config{Host: "replica.internal", SSLMode: "nonsense"}
	if err := config.Validate(); err == nil {
		t.Error("a replica with an impossible setting should stop start-up like any other")
	} else if !strings.Contains(err.Error(), "replica") {
		t.Errorf("the message should say which of the two servers is wrong: %v", err)
	}
}

func TestAFieldReplicaCannotPretendToInheritAnOpaquePrimaryDSN(t *testing.T) {
	config := vvdb.Config{
		Engine:  vvdb.Postgres,
		DSN:     "postgres://vv:secret@primary.internal:5432/app",
		Replica: &vvdb.Config{Host: "replica.internal"},
	}
	if err := config.Validate(); !errors.Is(err, vvdb.ErrConflict) || !strings.Contains(err.Error(), "replica.dsn") {
		t.Fatalf("Validate() = %v, want a named refusal rather than a derived replica with an empty database name", err)
	}
}

func TestValidateNamesTheFieldThatIsWrong(t *testing.T) {
	for _, tc := range []struct {
		config vvdb.Config
		says   string
	}{
		{vvdb.Config{Engine: vvdb.Postgres, Host: "h"}, "name"},
		{vvdb.Config{Engine: vvdb.SQLite}, "path"},
		{vvdb.Config{Engine: vvdb.Postgres, Name: "app", DSN: "postgres://x/y", Host: "h"}, "host"},
		{vvdb.Config{Engine: vvdb.Postgres, DSN: "postgres://x/y", Pool: vvdb.Pool{ConnectTimeout: time.Second}}, "pool.connect_timeout"},
	} {
		err := tc.config.Validate()
		if err == nil {
			t.Fatalf("%+v was accepted", tc.config)
		}
		if !strings.Contains(err.Error(), tc.says) {
			t.Errorf("the message should name %q so the operator knows which line to edit: %v", tc.says, err)
		}
	}
}

func TestValidateRefusesImplicitServerIdentityAndImpossiblePool(t *testing.T) {
	for _, tc := range []struct {
		name   string
		config vvdb.Config
		want   error
	}{
		{"server without host", vvdb.Config{Engine: vvdb.Postgres, Name: "app"}, vvdb.ErrMissing},
		{"password without user", vvdb.Config{Engine: vvdb.Postgres, Host: "db", Name: "app", Password: "secret"}, vvdb.ErrConflict},
		{"negative max open", vvdb.Config{Engine: vvdb.Postgres, Host: "db", Name: "app", Pool: vvdb.Pool{MaxOpen: -1}}, vvdb.ErrUnsupported},
		{"idle above open", vvdb.Config{Engine: vvdb.Postgres, Host: "db", Name: "app", Pool: vvdb.Pool{MaxOpen: 2, MaxIdle: 3}}, vvdb.ErrConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.config.Validate(); !errors.Is(err, tc.want) {
				t.Fatalf("Validate() = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestDriverNameDefaultsPerEngineAndIsOverridable(t *testing.T) {
	for _, tc := range []struct {
		config vvdb.Config
		want   string
	}{
		{vvdb.Config{Engine: vvdb.Postgres}, "pgx"},
		{vvdb.Config{Engine: vvdb.MySQL}, "mysql"},
		{vvdb.Config{Engine: vvdb.MariaDB}, "mysql"},
		{vvdb.Config{Engine: vvdb.SQLite}, "sqlite"},
		{vvdb.Config{Engine: vvdb.Postgres, Driver: "postgres"}, "postgres"},
	} {
		if got := vvdb.DriverName(&tc.config); got != tc.want {
			t.Errorf("%s with driver %q should open with %q, got %q", tc.config.Engine, tc.config.Driver, tc.want, got)
		}
	}
}
