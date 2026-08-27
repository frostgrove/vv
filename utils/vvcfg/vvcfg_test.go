package vvcfg

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/frostgrove/vv/utils/vvdb"
)

type conf struct {
	Name string `yaml:"name" env:"NAME"`
	Port int    `yaml:"port" env:"PORT"`
}

type strictConf struct {
	Port int `yaml:"port"`
}

type databaseConf struct {
	DB vvdb.Config `yaml:"db"`
}

func (c *databaseConf) Validate() error { return c.DB.Validate() }

type twoDatabaseConf struct {
	DB        vvdb.Config `yaml:"db"`
	Analytics vvdb.Config `yaml:"analytics" env-prefix:"ANALYTICS_"`
}

func (c *twoDatabaseConf) Validate() error {
	if err := c.DB.Validate(); err != nil {
		return err
	}
	return c.Analytics.Validate()
}

func (c *strictConf) Validate() error {
	if c.Port == 0 {
		return errors.New("port is required")
	}
	return nil
}

func write(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadReadsThePathItIsGiven(t *testing.T) {
	// The defect this pins: MustLoad took a variadic path and ignored it,
	// reading --config-path instead. A caller passing a path got a different
	// file, silently.
	p := write(t, "name: from-the-argument\nport: 1\n")
	got, err := Load[conf](p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Name != "from-the-argument" {
		t.Fatalf("name = %q: Load did not read the path it was given", got.Name)
	}
}

func TestAMissingFileAndAnUnreadableOneAreDifferentMessages(t *testing.T) {
	_, missing := Load[conf](filepath.Join(t.TempDir(), "nope.yaml"))
	if missing == nil || !strings.Contains(missing.Error(), "no such file") {
		t.Fatalf("a missing file should say so, got %v", missing)
	}
	// The original checked only os.IsNotExist, so anything else fell through to
	// the decoder and surfaced as "failed to read config" — the wrong place to
	// go looking.
	dir := t.TempDir()
	_, isDir := Load[conf](dir)
	if isDir == nil {
		t.Fatal("a directory is not a configuration file")
	}
	if strings.Contains(isDir.Error(), "no such file") {
		t.Fatalf("a directory reported as missing: %v", isDir)
	}
}

func TestValidateRefusesTheProcessAtStartUp(t *testing.T) {
	p := write(t, "port: 0\n")
	_, err := Load[strictConf](p)
	if err == nil {
		t.Fatal("a config that fails its own Validate should not load")
	}
	if !strings.Contains(err.Error(), "port is required") {
		t.Fatalf("the validation error should reach the caller, got %v", err)
	}

	ok := write(t, "port: 8080\n")
	if _, err := Load[strictConf](ok); err != nil {
		t.Fatalf("a valid config should load: %v", err)
	}
}

func TestAConfigWithoutValidateIsLoadedAsIs(t *testing.T) {
	// The control for the test above: without it, a Validate that never runs
	// would look identical to a Validate that always passes.
	p := write(t, "name: x\nport: 0\n")
	if _, err := Load[conf](p); err != nil {
		t.Fatalf("a config with no Validate method has nothing to refuse it: %v", err)
	}
}

func TestVVDBParamsAreAnOrdinaryEnvironmentBackedField(t *testing.T) {
	t.Setenv("DB_PARAMS", "application_name=orders,worker&statement_timeout=5s")
	p := write(t, "engine: postgres\nhost: db.internal\nname: orders\n")
	got, err := Load[vvdb.Config](p)
	if err != nil {
		t.Fatal(err)
	}
	if got.Params["application_name"] != "orders,worker" || got.Params["statement_timeout"] != "5s" {
		t.Fatalf("params = %#v, want environment map values", got.Params)
	}
}

func TestVVDBReplicaEnvironmentOverridesYAMLAndPoolTagsStayRegular(t *testing.T) {
	t.Setenv("DB_REPLICA_HOST", "replica.from.env")
	t.Setenv("DB_REPLICA_POOL_MAX_IDLE", "3")
	t.Setenv("DB_POOL_CONNECT_TIMEOUT", "2s")
	p := write(t, "db:\n  engine: postgres\n  host: primary.internal\n  name: orders\n  pool:\n    max_open: 5\n  replica:\n    host: replica.from.yaml\n")
	got, err := Load[databaseConf](p)
	if err != nil {
		t.Fatal(err)
	}
	if got.DB.Pool.ConnectTimeout != 2*time.Second {
		t.Fatalf("primary pool.connect_timeout = %s, want DB_POOL_CONNECT_TIMEOUT", got.DB.Pool.ConnectTimeout)
	}
	r, ok := got.DB.ReadReplica()
	if !ok {
		t.Fatal("the replica environment should allocate a usable replica")
	}
	if r.Host != "replica.from.env" || r.Pool.MaxIdle != 3 || r.Pool.MaxOpen != 5 {
		t.Fatalf("replica after environment overlay = %+v, want host override and merged pool", r)
	}
}

func TestVVDBReplicaEnvironmentHonoursEachNestedEnvPrefix(t *testing.T) {
	t.Setenv("DB_HOST", "primary.from.env")
	t.Setenv("DB_REPLICA_HOST", "primary-replica.from.env")
	t.Setenv("ANALYTICS_DB_HOST", "analytics.from.env")
	t.Setenv("ANALYTICS_DB_REPLICA_HOST", "analytics-replica.from.env")
	p := write(t, "db:\n  engine: postgres\n  host: primary.from.yaml\n  name: app\n  replica:\n    host: primary-replica.from.yaml\nanalytics:\n  engine: postgres\n  host: analytics.from.yaml\n  name: analytics\n  replica:\n    host: analytics-replica.from.yaml\n")
	got, err := Load[twoDatabaseConf](p)
	if err != nil {
		t.Fatal(err)
	}
	primaryReplica, ok := got.DB.ReadReplica()
	if !ok {
		t.Fatal("primary replica was not loaded")
	}
	analyticsReplica, ok := got.Analytics.ReadReplica()
	if !ok {
		t.Fatal("analytics replica was not loaded")
	}
	if got.DB.Host != "primary.from.env" || primaryReplica.Host != "primary-replica.from.env" {
		t.Fatalf("primary config = %+v replica = %+v, want its unprefixed environment", got.DB, primaryReplica)
	}
	if got.Analytics.Host != "analytics.from.env" || analyticsReplica.Host != "analytics-replica.from.env" {
		t.Fatalf("analytics config = %+v replica = %+v, want its ANALYTICS_ environment", got.Analytics, analyticsReplica)
	}
}

func TestVVDBRawDSNEnvironmentReplacesAFileFormConnection(t *testing.T) {
	t.Setenv("DB_DSN", "postgres://from-env/orders")
	t.Setenv("DB_REPLICA_DSN", "postgres://replica-env/orders")
	p := write(t, "db:\n  engine: postgres\n  host: primary.from.yaml\n  name: orders\n  replica:\n    host: replica.from.yaml\n")
	got, err := Load[databaseConf](p)
	if err != nil {
		t.Fatal(err)
	}
	if got.DB.DSN != "postgres://from-env/orders" || got.DB.Host != "" || got.DB.Name != "" {
		t.Fatalf("primary environment DSN did not replace fields: %+v", got.DB)
	}
	r, ok := got.DB.ReadReplica()
	if !ok || r.DSN != "postgres://replica-env/orders" || r.Host != "" {
		t.Fatalf("replica environment DSN did not replace fields: %+v", r)
	}
}

func TestAutoSupportsAnEnvironmentOnlyVVDBDeclaration(t *testing.T) {
	t.Setenv("CONFIG_PATH", "")
	t.Setenv("DB_ENGINE", "postgres")
	t.Setenv("DB_HOST", "db.from.env")
	t.Setenv("DB_NAME", "orders")
	got, err := Auto[databaseConf](nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.DB.Engine != vvdb.Postgres || got.DB.Host != "db.from.env" || got.DB.Name != "orders" {
		t.Fatalf("environment-only config = %+v", got.DB)
	}
}

func TestFindPrefersTheFlagOverTheEnvironment(t *testing.T) {
	t.Setenv("CONFIG_PATH", "/from/env.yaml")

	got, err := Find([]string{"--config-path", "/from/flag.yaml"})
	if err != nil || got != "/from/flag.yaml" {
		t.Fatalf("the flag should win, got %q (%v)", got, err)
	}

	got, err = Find(nil)
	if err != nil || got != "/from/env.yaml" {
		t.Fatalf("the environment should be the fallback, got %q (%v)", got, err)
	}
}

func TestNoPathAtAllIsAnErrorRatherThanAGuess(t *testing.T) {
	t.Setenv("CONFIG_PATH", "")
	if _, err := Find(nil); !errors.Is(err, ErrNoPath) {
		t.Fatalf("with nothing configured, Find should refuse: %v", err)
	}
	if _, err := Load[conf](""); !errors.Is(err, ErrNoPath) {
		t.Fatalf("Load(\"\") should refuse rather than stat the empty path: %v", err)
	}
}

func TestAutoFindsThenLoads(t *testing.T) {
	p := write(t, "name: via-auto\nport: 2\n")
	got, err := Auto[conf]([]string{"--config-path", p})
	if err != nil {
		t.Fatalf("Auto: %v", err)
	}
	if got.Name != "via-auto" {
		t.Fatalf("name = %q, want via-auto", got.Name)
	}
}

func TestMustPanicsRatherThanReturningNil(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("Must should panic on an error, not hand back a nil config")
		}
	}()
	Must(Load[conf](filepath.Join(t.TempDir(), "nope.yaml")))
}
