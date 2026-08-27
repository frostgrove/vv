package vvdb

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// An Engine is one database this package can describe. The set is closed: a
// spelling nobody declared is refused rather than guessed at, because the guess
// that costs the most — reading an unknown engine as the default one — connects
// successfully to the wrong server.
type Engine string

const (
	Postgres Engine = "postgres"
	MySQL    Engine = "mysql"
	MariaDB  Engine = "mariadb"
	SQLite   Engine = "sqlite"
)

// Sentinels. Compare with errors.Is; every error this package returns wraps one
// of them and adds the field it is about.
var (
	// ErrEngine reports an engine that is empty or not one of the four.
	ErrEngine = errors.New("vvdb: unknown engine")
	// ErrMissing reports a field the engine cannot do without.
	ErrMissing = errors.New("vvdb: missing")
	// ErrConflict reports a DSN set beside the fields it would override.
	ErrConflict = errors.New("vvdb: two sources of truth")
	// ErrUnsupported reports a setting the engine cannot express.
	ErrUnsupported = errors.New("vvdb: unsupported by this engine")
)

// A Config is one database, described once. The same document works for every
// engine: what differs between them is the string this package builds out of
// it, not the keys an operator types.
//
// The tags are cleanenv's, so utils/vvcfg loads this struct with no glue:
//
//	type Config struct {
//	    Addr string      `yaml:"addr" env:"ADDR"`
//	    DB   vvdb.Config `yaml:"db"`
//	}
type Config struct {
	Engine Engine `yaml:"engine" env:"DB_ENGINE"`
	// Driver is the database/sql driver name to open with. Empty means the
	// default for the engine — "pgx", "mysql", "sqlite". PostgreSQL's typed
	// configuration is deliberately pgx-only: lib/pq combines empty URI values
	// with PG* and ~/.pgpass, defeating this struct's single-source guarantee.
	// Use an explicit raw DSN for a driver that needs its own vocabulary.
	Driver string `yaml:"driver" env:"DB_DRIVER"`

	Host     string `yaml:"host" env:"DB_HOST"`
	Port     int    `yaml:"port" env:"DB_PORT"`
	User     string `yaml:"user" env:"DB_USER"`
	Password string `yaml:"password" env:"DB_PASSWORD"`
	Name     string `yaml:"name" env:"DB_NAME"`

	// SSLMode is spelled in PostgreSQL's vocabulary — disable, require,
	// verify-ca, verify-full — and translated for the others. See [TLSMode].
	SSLMode string `yaml:"sslmode" env:"DB_SSLMODE"`
	// Path is the file SQLite opens, and is meaningless anywhere else.
	Path string `yaml:"path" env:"DB_PATH"`
	// Pragmas are SQLite settings that must be sent repeatedly by the default
	// modernc driver, for example ["journal_mode=WAL", "busy_timeout=5000"].
	// vvdb translates these named settings for the two documented SQLite driver
	// names; Params remains for one-off, driver-specific connection parameters.
	Pragmas SQLitePragmas `yaml:"pragmas" env:"DB_SQLITE_PRAGMAS"`
	// Params are added to the connection string as they are, after whatever
	// escaping the engine's parser needs. They are for driver-specific settings;
	// a value may not contradict the typed connection fact that is also set.
	// In the environment, use URL-query syntax, for example
	// DB_PARAMS='application_name=orders,worker&statement_timeout=5s'. That
	// keeps commas and every other escaped query value representable.
	Params Params `yaml:"params" env:"DB_PARAMS"`

	Pool      Pool      `yaml:"pool"`
	Migration Migration `yaml:"migration"`

	// Replica is a second server for reads that may be served stale. It
	// inherits every field left empty here, so a replica that differs only by
	// hostname is one line. An opaque primary DSN cannot be safely inherited and
	// changed: use Replica.DSN in that case. utils/vvcfg recognizes the
	// DB_REPLICA_* counterparts of these fields as environment overrides. See
	// [Config.ReadReplica] and crud.ReadWrite.
	Replica *Config `yaml:"replica"`

	// DSN is the escape hatch: a connection string somebody else produced,
	// used exactly as given. It refuses to work half way — set it and leave
	// every field it would override empty, or [Config.Validate] fails and
	// names both.
	DSN string `yaml:"dsn" env:"DB_DSN"`
}

// Migration describes the application-owned migration layout. It lives in
// vvdb so the database and its migrations are configured in one block, while
// the Goose dependency itself stays in the separate utils/vvgoose module.
//
// Models names directories vvgoose scans for Go structs when generating a SQL
// migration. An empty list means the current module. Table is Goose's migration
// history table, not an application table.
type Migration struct {
	Path   string   `yaml:"path" env:"DB_MIGRATION_PATH" env-default:"./migrations"`
	Models []string `yaml:"models" env:"DB_MIGRATION_MODELS" env-default:"."`
	Table  string   `yaml:"table" env:"DB_MIGRATION_TABLE" env-default:"goose_db_version"`
}

// Validate checks the declarative part of the migration configuration. It does
// not touch the filesystem: the same database block is commonly loaded by the
// server binary, where migration sources need not be present, and a creation
// command is allowed to create Path itself.
//
// Empty fields are valid. They are the zero-value spelling of the defaults in
// the tags above for a Config assembled in Go rather than loaded by vvcfg.
func (m Migration) Validate() error {
	if m.Path != "" && strings.TrimSpace(m.Path) == "" {
		return fmt.Errorf("%w: migration.path is blank", ErrMissing)
	}
	for i, dir := range m.Models {
		if strings.TrimSpace(dir) == "" {
			return fmt.Errorf("%w: migration.models[%d] is blank", ErrMissing, i)
		}
	}
	if m.Table != "" && !validMigrationTable(m.Table) {
		return fmt.Errorf("%w: migration.table %q must be an identifier or schema.identifier", ErrUnsupported, m.Table)
	}
	return nil
}

// validMigrationTable accepts the lower-case, unquoted identifier subset
// shared by all four engines. Goose interpolates its history table into SQL
// rather than binding or quoting it. In PostgreSQL an upper-case name would be
// folded during CREATE TABLE but compared verbatim during Goose's existence
// check, so accepting it would make every later command try to recreate the
// history table. One qualifier is useful for a PostgreSQL schema and a MySQL
// database; more than one is not understood by Goose's own table lookup.
func validMigrationTable(name string) bool {
	parts := strings.Split(name, ".")
	if len(parts) == 0 || len(parts) > 2 {
		return false
	}
	for _, part := range parts {
		if part == "" || !identifierStart(part[0]) {
			return false
		}
		for i := 1; i < len(part); i++ {
			if !identifierPart(part[i]) {
				return false
			}
		}
	}
	return true
}

func identifierStart(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z'
}

func identifierPart(c byte) bool { return identifierStart(c) || c >= '0' && c <= '9' }

// A Pool sizes the connections. The four limits are database/sql's and pgx
// reads them too; ConnectTimeout is the only one that travels in the
// connection string, because that is where every driver looks for it.
type Pool struct {
	MaxOpen        int           `yaml:"max_open" env:"DB_POOL_MAX_OPEN"`
	MaxIdle        int           `yaml:"max_idle" env:"DB_POOL_MAX_IDLE"`
	MaxLifetime    time.Duration `yaml:"max_lifetime" env:"DB_POOL_MAX_LIFETIME"`
	MaxIdleTime    time.Duration `yaml:"max_idle_time" env:"DB_POOL_MAX_IDLE_TIME"`
	ConnectTimeout time.Duration `yaml:"connect_timeout" env:"DB_POOL_CONNECT_TIMEOUT,DB_CONNECT_TIMEOUT"`
}

// Params is the open-ended, driver-specific portion of a database
// configuration. Its SetValue method gives DB_PARAMS one unambiguous
// environment representation: an URL query, rather than cleanenv's
// comma-separated map notation. A literal comma is therefore a value, and
// reserved URL characters use normal percent escaping.
type Params map[string]string

// SetValue parses the DB_PARAMS URL-query representation.
func (p *Params) SetValue(raw string) error {
	values, err := url.ParseQuery(raw)
	if err != nil {
		return fmt.Errorf("params must be an URL query: %w", err)
	}
	out := make(Params, len(values))
	for key, vals := range values {
		if key == "" {
			return fmt.Errorf("params has an empty key")
		}
		if len(vals) != 1 {
			return fmt.Errorf("params.%s is repeated; each parameter must have one value", key)
		}
		out[key] = vals[0]
	}
	*p = out
	return nil
}

// SQLitePragmas is the portable SQLite pragma list. YAML gives it a natural
// sequence; DB_SQLITE_PRAGMAS is a comma-separated list because pragma values
// themselves are simple scalar settings, e.g.
// DB_SQLITE_PRAGMAS='journal_mode=WAL,busy_timeout=5000'.
type SQLitePragmas []string

// SetValue parses DB_SQLITE_PRAGMAS and DB_REPLICA_PRAGMAS.
func (p *SQLitePragmas) SetValue(raw string) error {
	if raw == "" {
		*p = nil
		return nil
	}
	items := strings.Split(raw, ",")
	out := make(SQLitePragmas, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			return fmt.Errorf("pragmas has an empty entry")
		}
		out = append(out, item)
	}
	*p = out
	return nil
}

// Validate keeps pragmas declarative and portable across the two SQLite
// drivers vvdb documents. Arbitrary SQL does not belong in a configuration
// value; the accepted settings cover connection durability and lock behaviour.
func (p SQLitePragmas) Validate() error {
	allowed := map[string]bool{
		"journal_mode": true,
		"busy_timeout": true,
		"foreign_keys": true,
		"synchronous":  true,
		"cache_size":   true,
		"temp_store":   true,
	}
	seen := make(map[string]bool, len(p))
	for _, raw := range p {
		name, value, ok := strings.Cut(raw, "=")
		name, value = strings.ToLower(strings.TrimSpace(name)), strings.TrimSpace(value)
		if !ok || name == "" || value == "" {
			return fmt.Errorf("%w: sqlite pragma %q must be name=value", ErrUnsupported, raw)
		}
		if !allowed[name] {
			return fmt.Errorf("%w: sqlite pragma %q is not one of journal_mode, busy_timeout, foreign_keys, synchronous, cache_size or temp_store", ErrUnsupported, name)
		}
		if seen[name] {
			return fmt.Errorf("%w: sqlite pragma %q is declared more than once", ErrConflict, name)
		}
		seen[name] = true
		if !validPragmaValue(name, value) {
			return fmt.Errorf("%w: sqlite pragma %q has an invalid value", ErrUnsupported, raw)
		}
	}
	return nil
}

func validPragmaValue(name, value string) bool {
	value = strings.ToLower(value)
	in := func(values ...string) bool {
		for _, candidate := range values {
			if value == candidate {
				return true
			}
		}
		return false
	}
	switch name {
	case "journal_mode":
		return in("delete", "truncate", "persist", "memory", "wal", "off")
	case "foreign_keys":
		return in("on", "off", "true", "false", "1", "0")
	case "synchronous":
		return in("off", "normal", "full", "extra", "0", "1", "2", "3")
	case "temp_store":
		return in("default", "file", "memory", "0", "1", "2")
	case "busy_timeout":
		n, err := strconv.ParseInt(value, 10, 64)
		return err == nil && n >= 0
	case "cache_size":
		_, err := strconv.ParseInt(value, 10, 64)
		return err == nil
	default:
		return false
	}
}

// defaultPort is the port an engine listens on when the config names none.
func defaultPort(e Engine) int {
	switch e {
	case Postgres:
		return 5432
	case MySQL, MariaDB:
		return 3306
	}
	return 0
}

// DriverName is the database/sql driver [Open] uses: the Driver field, or the
// usual name for the engine.
//
// The PostgreSQL default is "pgx" and not "postgres". Both exist, both are
// registered by an import the consumer wrote, and the wrong one is a loud
// failure from sql.Open rather than a quiet one — so the default is the driver
// this repository's own adapters and examples use.
func DriverName(c Config) string {
	if c.Driver != "" {
		return c.Driver
	}
	switch c.Engine {
	case Postgres:
		return "pgx"
	case MySQL, MariaDB:
		return "mysql"
	case SQLite:
		return "sqlite"
	}
	return ""
}

// known reports whether the engine is one of the four.
func known(e Engine) bool {
	switch e {
	case Postgres, MySQL, MariaDB, SQLite:
		return true
	}
	return false
}

// Validate refuses a configuration that cannot mean what it says. It is called
// by [DSN] and by [Open], so a caller who forgets it still cannot get a wrong
// connection; it is exported because vvcfg.Load calls Validate on the struct it
// loaded, and an application whose config embeds this one forwards to it.
func (c Config) Validate() error {
	if !known(c.Engine) {
		return fmt.Errorf("%w: %q, want one of postgres, mysql, mariadb, sqlite", ErrEngine, c.Engine)
	}
	if err := c.Pool.Validate(); err != nil {
		return err
	}
	if err := c.Migration.Validate(); err != nil {
		return err
	}
	if c.DSN != "" {
		if f := c.fieldsBesideDSN(); f != "" {
			return fmt.Errorf("%w: dsn is set and so is %s — one of them would be ignored", ErrConflict, f)
		}
	} else if err := c.validateFields(); err != nil {
		return err
	}
	if c.Replica != nil {
		if c.Engine == SQLite {
			return fmt.Errorf("%w: replica is not available for sqlite", ErrUnsupported)
		}
		if !migrationEmpty(c.Replica.Migration) {
			return fmt.Errorf("%w: replica.migration belongs to the primary database", ErrUnsupported)
		}
		if replicaEmpty(*c.Replica) {
			return fmt.Errorf("%w: replica is declared but names no difference from the primary", ErrMissing)
		}
		if c.Replica.Replica != nil {
			return fmt.Errorf("%w: replica.replica, only one primary and one read replica are supported", ErrUnsupported)
		}
		if c.DSN != "" && c.Replica.DSN == "" {
			return fmt.Errorf("%w: replica fields cannot inherit from opaque dsn; set replica.dsn instead", ErrConflict)
		}
		if c.Replica.DSN != "" {
			if f := c.Replica.fieldsBesideDSN(); f != "" {
				return fmt.Errorf("replica: %w: dsn is set and so is %s — one of them would be ignored", ErrConflict, f)
			}
		}
		r, _ := c.ReadReplica()
		if r.Engine != c.Engine {
			return fmt.Errorf("%w: replica engine %q differs from %q — a replica of another engine is not a replica",
				ErrConflict, r.Engine, c.Engine)
		}
		// The merged replica is what will be opened, so it is what is checked.
		// Validating the fragment as written would pass a replica missing
		// every field it inherits.
		if err := r.Validate(); err != nil {
			return fmt.Errorf("replica: %w", err)
		}
	}
	return nil
}

// fieldsBesideDSN names the first field that contradicts a DSN, or "".
func (c Config) fieldsBesideDSN() string {
	switch {
	case c.Host != "":
		return "host"
	case c.Port != 0:
		return "port"
	case c.User != "":
		return "user"
	case c.Password != "":
		return "password"
	case c.Name != "":
		return "name"
	case c.SSLMode != "":
		return "sslmode"
	case c.Path != "":
		return "path"
	case len(c.Pragmas) != 0:
		return "pragmas"
	case len(c.Params) != 0:
		return "params"
	case c.Pool.ConnectTimeout != 0:
		return "pool.connect_timeout"
	}
	return ""
}

func (c Config) validateFields() error {
	if c.Engine == SQLite {
		if c.Path == "" {
			return fmt.Errorf("%w: path, which is the file sqlite opens", ErrMissing)
		}
		if c.Host != "" || c.Port != 0 || c.User != "" || c.Password != "" || c.Name != "" {
			return fmt.Errorf("%w: sqlite has no host, port, user, password or database name — it has a path", ErrUnsupported)
		}
		if c.SSLMode != "" {
			return fmt.Errorf("%w: sqlite is a file, not a connection, and has no sslmode", ErrUnsupported)
		}
		if c.Pool.ConnectTimeout != 0 {
			return fmt.Errorf("%w: pool.connect_timeout is for a server connection, not sqlite", ErrUnsupported)
		}
		if err := c.Pragmas.Validate(); err != nil {
			return err
		}
		return c.validateParams()
	}
	if len(c.Pragmas) != 0 {
		return fmt.Errorf("%w: pragmas are sqlite settings, and %q is not sqlite", ErrUnsupported, c.Engine)
	}
	if c.Engine == Postgres && c.Driver != "" && c.Driver != "pgx" {
		return fmt.Errorf("%w: typed postgres configuration requires driver pgx; use Config.DSN for %q", ErrUnsupported, c.Driver)
	}
	if c.Name == "" {
		return fmt.Errorf("%w: name, the database to connect to", ErrMissing)
	}
	if c.Host == "" {
		return fmt.Errorf("%w: host, the database server to connect to", ErrMissing)
	}
	if c.Port < 0 || c.Port > 65535 {
		return fmt.Errorf("%w: port %d is outside the TCP range 1..65535", ErrUnsupported, c.Port)
	}
	if strings.HasPrefix(c.Host, "[") && strings.HasSuffix(c.Host, "]") {
		return fmt.Errorf("%w: host %q is an already-bracketed IPv6 literal; use the bare address", ErrUnsupported, c.Host)
	}
	if c.Password != "" && c.User == "" {
		return fmt.Errorf("%w: password is set without user, so a driver could authenticate as the process user instead", ErrConflict)
	}
	if c.Path != "" {
		return fmt.Errorf("%w: path is sqlite's, and %q is not sqlite", ErrUnsupported, c.Engine)
	}
	if _, err := tlsParam(c.Engine, c.SSLMode); err != nil {
		return err
	}
	if c.Engine != Postgres && strings.Contains(c.User, ":") {
		// The MySQL driver splits user from password at the *first* colon, so a
		// colon in the user silently moves half the name into the password.
		return fmt.Errorf("%w: a mysql user name cannot contain ':' — the driver reads everything after it as the password", ErrUnsupported)
	}
	return c.validateParams()
}

// validateParams keeps connection facts single-sourced. Params is deliberately
// broad for driver-specific settings, but it must not be able to override a
// typed field after vvdb has validated and rendered it.
func (c Config) validateParams() error {
	if c.Engine == SQLite {
		for key := range c.Params {
			name := strings.ToLower(strings.TrimSpace(key))
			if name == "_pragma" || name == "pragma" || name == "_journal_mode" ||
				name == "_busy_timeout" || name == "_foreign_keys" ||
				name == "_synchronous" || name == "_cache_size" || name == "_temp_store" {
				return fmt.Errorf("%w: params.%s is a driver pragma; declare it in pragmas so vvdb can validate it", ErrUnsupported, key)
			}
		}
	}
	if c.Engine == Postgres {
		for _, key := range []string{"service", "servicefile"} {
			if _, ok := c.Params[key]; ok {
				return fmt.Errorf("%w: params.%s makes a second configuration document; use Config.DSN when a libpq service is intentional", ErrUnsupported, key)
			}
		}
	}
	reserved := map[string]string{}
	switch c.Engine {
	case Postgres:
		for key, field := range map[string]string{
			"host":     "host",
			"port":     "port",
			"user":     "user",
			"password": "password",
			"dbname":   "name",
			"database": "name",
		} {
			reserved[key] = field
		}
		// PostgresDSN renders these defaults even when the corresponding
		// typed field is zero. Reserving them conditionally would let Params
		// silently replace the rendered default with a second source of truth.
		reserved["sslmode"] = "sslmode"
		reserved["connect_timeout"] = "pool.connect_timeout"
	case MySQL, MariaDB:
		if c.SSLMode != "" {
			reserved["tls"] = "sslmode"
		}
		if c.Pool.ConnectTimeout != 0 {
			reserved["timeout"] = "pool.connect_timeout"
		}
	}
	for key, field := range reserved {
		if _, ok := c.Params[key]; ok {
			return fmt.Errorf("%w: params.%s conflicts with %s", ErrConflict, key, field)
		}
	}
	return nil
}

// ReadReplica returns the replica as it will be opened: this config with the
// replica's non-empty fields laid over it. The second result is false when
// there is no replica.
//
// Inheritance is the point. A replica differs from its primary by hostname and
// nothing else nine times out of ten, and repeating the credentials is how the
// two drift apart the day one of them is rotated.
func (c Config) ReadReplica() (Config, bool) {
	if c.Replica == nil || replicaEmpty(*c.Replica) || c.Replica.Replica != nil || !migrationEmpty(c.Replica.Migration) {
		return Config{}, false
	}
	// A field-level replica needs the primary's connection facts. An opaque
	// primary deliberately has none to inherit, and Validate reports this as a
	// named conflict; do not return a tempting but unusable partial replica to
	// callers that inspect ReadReplica directly.
	if c.DSN != "" && c.Replica.DSN == "" {
		return Config{}, false
	}
	r := *c.Replica
	// A raw replica DSN owns every connection fact in the string. Driver choice
	// and the database/sql pool are outside that string, so they keep inheriting
	// from the primary. ConnectTimeout is the sole pool setting encoded in a
	// DSN, and is intentionally not inherited into one.
	if r.DSN != "" {
		base := c
		base.Replica = nil
		base.Migration = Migration{}
		base.DSN = r.DSN
		base.Host, base.Port = "", 0
		base.User, base.Password, base.Name = "", "", ""
		base.SSLMode, base.Path = "", ""
		base.Pragmas = nil
		base.Params = nil
		base.Pool = mergePool(base.Pool, r.Pool)
		base.Pool.ConnectTimeout = r.Pool.ConnectTimeout
		if r.Engine != "" {
			base.Engine = r.Engine
		}
		if r.Driver != "" {
			base.Driver = r.Driver
		}
		return base, true
	}
	base := c
	base.Replica = nil
	base.Migration = Migration{}
	base.DSN = ""
	base.Params = cloneParams(c.Params)
	base.Pragmas = clonePragmas(c.Pragmas)
	if r.Engine != "" {
		base.Engine = r.Engine
	}
	if r.Driver != "" {
		base.Driver = r.Driver
	}
	if r.Host != "" {
		base.Host = r.Host
	}
	if r.Port != 0 {
		base.Port = r.Port
	}
	if r.User != "" {
		base.User = r.User
	}
	if r.Password != "" {
		base.Password = r.Password
	}
	if r.Name != "" {
		base.Name = r.Name
	}
	if r.SSLMode != "" {
		base.SSLMode = r.SSLMode
	}
	if r.Path != "" {
		base.Path = r.Path
	}
	if len(r.Pragmas) != 0 {
		base.Pragmas = clonePragmas(r.Pragmas)
	}
	base.Pool = mergePool(base.Pool, r.Pool)
	if len(r.Params) > 0 {
		merged := make(Params, len(base.Params)+len(r.Params))
		for k, v := range base.Params {
			merged[k] = v
		}
		for k, v := range r.Params {
			merged[k] = v
		}
		base.Params = merged
	}
	return base, true
}

func replicaEmpty(c Config) bool {
	return c.Engine == "" && c.Driver == "" && c.Host == "" && c.Port == 0 &&
		c.User == "" && c.Password == "" && c.Name == "" && c.SSLMode == "" &&
		c.Path == "" && len(c.Pragmas) == 0 && len(c.Params) == 0 && c.Pool == (Pool{}) &&
		migrationEmpty(c.Migration) && c.DSN == "" && c.Replica == nil
}

func migrationEmpty(m Migration) bool {
	return m.Path == "" && len(m.Models) == 0 && m.Table == ""
}

func cloneParams(in Params) Params {
	if len(in) == 0 {
		return nil
	}
	out := make(Params, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func clonePragmas(in SQLitePragmas) SQLitePragmas {
	return append(SQLitePragmas(nil), in...)
}

// mergePool inherits every setting the replica did not state. Pool uses value
// fields, so zero is the documented "unset" spelling; a non-zero replica value
// is the only portable declaration that can replace its primary's value.
func mergePool(base, override Pool) Pool {
	if override.MaxOpen != 0 {
		base.MaxOpen = override.MaxOpen
	}
	if override.MaxIdle != 0 {
		base.MaxIdle = override.MaxIdle
	}
	if override.MaxLifetime != 0 {
		base.MaxLifetime = override.MaxLifetime
	}
	if override.MaxIdleTime != 0 {
		base.MaxIdleTime = override.MaxIdleTime
	}
	if override.ConnectTimeout != 0 {
		base.ConnectTimeout = override.ConnectTimeout
	}
	return base
}

// ApplyEnvironment overlays the documented DB_REPLICA_* variables on Replica.
// It is called automatically by vvcfg.Load, including when Config is nested in
// an application's top-level configuration. cleanenv handles Config's scalar
// DB_* tags, but intentionally does not allocate or descend into pointer
// structs, so the replica needs this small, explicit bridge.
//
// The variables mirror the YAML names with a DB_REPLICA_ prefix:
// DB_REPLICA_HOST, DB_REPLICA_PORT, DB_REPLICA_USER,
// DB_REPLICA_PASSWORD, DB_REPLICA_NAME, DB_REPLICA_SSLMODE,
// DB_REPLICA_PATH, DB_REPLICA_DSN, DB_REPLICA_PARAMS and
// DB_REPLICA_POOL_{MAX_OPEN,MAX_IDLE,MAX_LIFETIME,MAX_IDLE_TIME,CONNECT_TIMEOUT}.
func (c *Config) ApplyEnvironment() error { return c.ApplyEnvironmentPrefix("") }

// ApplyEnvironmentPrefix is the env-prefix-aware form vvcfg.Load invokes for a
// nested Config. A field tagged `env-prefix:"ANALYTICS_"` therefore uses
// ANALYTICS_DB_REPLICA_HOST rather than accidentally applying DB_REPLICA_HOST
// to every database block in one process.
func (c *Config) ApplyEnvironmentPrefix(prefix string) error {
	if c == nil {
		return nil
	}
	// cleanenv has already overlaid the scalar DB_* values when this hook runs.
	// A whole DSN is the one deliberate override of field-form configuration: it
	// replaces those connection facts as a group, instead of leaving a YAML host
	// beside DB_DSN and refusing an environment-only deployment at startup.
	if _, set := os.LookupEnv(prefix + "DB_DSN"); set {
		c.clearFieldsBesideDSN()
	}
	r := Config{}
	if c.Replica != nil {
		r = *c.Replica
		r.Params = cloneParams(r.Params)
	}

	found := false
	stringField := func(name string, dst *string) {
		name = prefix + name
		if value, ok := os.LookupEnv(name); ok {
			*dst = value
			found = true
		}
	}
	intField := func(name string, dst *int) error {
		name = prefix + name
		value, ok := os.LookupEnv(name)
		if !ok {
			return nil
		}
		n, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("parsing %s: %w", name, err)
		}
		*dst = n
		found = true
		return nil
	}
	durationField := func(name string, dst *time.Duration) error {
		name = prefix + name
		value, ok := os.LookupEnv(name)
		if !ok {
			return nil
		}
		d, err := time.ParseDuration(value)
		if err != nil {
			return fmt.Errorf("parsing %s: %w", name, err)
		}
		*dst = d
		found = true
		return nil
	}

	if value, ok := os.LookupEnv(prefix + "DB_REPLICA_ENGINE"); ok {
		r.Engine = Engine(value)
		found = true
	}
	stringField("DB_REPLICA_DRIVER", &r.Driver)
	stringField("DB_REPLICA_HOST", &r.Host)
	if err := intField("DB_REPLICA_PORT", &r.Port); err != nil {
		return err
	}
	stringField("DB_REPLICA_USER", &r.User)
	stringField("DB_REPLICA_PASSWORD", &r.Password)
	stringField("DB_REPLICA_NAME", &r.Name)
	stringField("DB_REPLICA_SSLMODE", &r.SSLMode)
	stringField("DB_REPLICA_PATH", &r.Path)
	if value, set := os.LookupEnv(prefix + "DB_REPLICA_DSN"); set {
		r.clearFieldsBesideDSN()
		r.DSN = value
		found = true
	}
	if value, ok := os.LookupEnv(prefix + "DB_REPLICA_PARAMS"); ok {
		if err := r.Params.SetValue(value); err != nil {
			return fmt.Errorf("parsing %sDB_REPLICA_PARAMS: %w", prefix, err)
		}
		found = true
	}
	if value, ok := os.LookupEnv(prefix + "DB_REPLICA_PRAGMAS"); ok {
		if err := r.Pragmas.SetValue(value); err != nil {
			return fmt.Errorf("parsing %sDB_REPLICA_PRAGMAS: %w", prefix, err)
		}
		found = true
	}
	if err := intField("DB_REPLICA_POOL_MAX_OPEN", &r.Pool.MaxOpen); err != nil {
		return err
	}
	if err := intField("DB_REPLICA_POOL_MAX_IDLE", &r.Pool.MaxIdle); err != nil {
		return err
	}
	if err := durationField("DB_REPLICA_POOL_MAX_LIFETIME", &r.Pool.MaxLifetime); err != nil {
		return err
	}
	if err := durationField("DB_REPLICA_POOL_MAX_IDLE_TIME", &r.Pool.MaxIdleTime); err != nil {
		return err
	}
	if err := durationField("DB_REPLICA_POOL_CONNECT_TIMEOUT", &r.Pool.ConnectTimeout); err != nil {
		return err
	}
	if found {
		c.Replica = &r
	}
	return nil
}

// clearFieldsBesideDSN leaves the engine/driver and portable pool sizing in
// place, but removes facts an opaque DSN owns. It is only used for an explicit
// environment DSN overlay; a Config assembled in Go still receives the normal
// conflict refusal from Validate.
func (c *Config) clearFieldsBesideDSN() {
	c.Host, c.Port, c.User, c.Password, c.Name, c.SSLMode, c.Path = "", 0, "", "", "", "", ""
	c.Params, c.Pragmas = nil, nil
	c.Pool.ConnectTimeout = 0
}

// Validate refuses a pool that contradicts itself before an adapter translates
// it. It is public so adapters that accept Pool directly keep the exact same
// invariant as Config.Validate.
func (p Pool) Validate() error {
	if p.MaxOpen < 0 {
		return fmt.Errorf("%w: pool.max_open cannot be negative", ErrUnsupported)
	}
	if p.MaxOpen > 0 && p.MaxIdle > p.MaxOpen {
		return fmt.Errorf("%w: pool.max_idle %d exceeds pool.max_open %d", ErrConflict, p.MaxIdle, p.MaxOpen)
	}
	if p.MaxLifetime < 0 {
		return fmt.Errorf("%w: pool.max_lifetime cannot be negative", ErrUnsupported)
	}
	if p.MaxIdleTime < 0 {
		return fmt.Errorf("%w: pool.max_idle_time cannot be negative", ErrUnsupported)
	}
	if p.ConnectTimeout < 0 {
		return fmt.Errorf("%w: pool.connect_timeout cannot be negative", ErrUnsupported)
	}
	return nil
}
