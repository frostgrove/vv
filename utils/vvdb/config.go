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

type Engine string

const (
	Postgres Engine = "postgres"
	MySQL    Engine = "mysql"
	MariaDB  Engine = "mariadb"
	SQLite   Engine = "sqlite"
)

var (
	ErrEngine = errors.New("vvdb: unknown engine")

	ErrMissing = errors.New("vvdb: missing")

	ErrConflict = errors.New("vvdb: two sources of truth")

	ErrUnsupported = errors.New("vvdb: unsupported by this engine")
)

type Config struct {
	Engine Engine `yaml:"engine" env:"DB_ENGINE"`

	Driver string `yaml:"driver" env:"DB_DRIVER"`

	Host     string `yaml:"host" env:"DB_HOST"`
	Port     int    `yaml:"port" env:"DB_PORT"`
	User     string `yaml:"user" env:"DB_USER"`
	Password Secret `yaml:"password" env:"DB_PASSWORD"`
	Name     string `yaml:"name" env:"DB_NAME"`

	SSLMode string `yaml:"sslmode" env:"DB_SSLMODE"`

	Path string `yaml:"path" env:"DB_PATH"`

	Pragmas SQLitePragmas `yaml:"pragmas" env:"DB_SQLITE_PRAGMAS"`

	Params Params `yaml:"params" env:"DB_PARAMS"`

	Pool      Pool      `yaml:"pool"`
	Migration Migration `yaml:"migration"`

	Replica *Config `yaml:"replica"`

	DSN Secret `yaml:"dsn" env:"DB_DSN"`
}

type Migration struct {
	Path   string   `yaml:"path" env:"DB_MIGRATION_PATH" env-default:"./migrations"`
	Models []string `yaml:"models" env:"DB_MIGRATION_MODELS" env-default:"."`
	Table  string   `yaml:"table" env:"DB_MIGRATION_TABLE" env-default:"goose_db_version"`
}

func (this *Migration) Validate() error {
	if this == nil {
		return nil
	}
	if this.Path != "" && strings.TrimSpace(this.Path) == "" {
		return fmt.Errorf("%w: migration.path is blank", ErrMissing)
	}
	for i, dir := range this.Models {
		if strings.TrimSpace(dir) == "" {
			return fmt.Errorf("%w: migration.models[%d] is blank", ErrMissing, i)
		}
	}
	if this.Table != "" && !validMigrationTable(this.Table) {
		return fmt.Errorf("%w: migration.table %q must be a non-reserved lower-case identifier or schema.identifier", ErrUnsupported, this.Table)
	}
	return nil
}

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
		if migrationReservedWords[part] {
			return false
		}
	}
	return true
}

func identifierStart(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z'
}

func identifierPart(c byte) bool { return identifierStart(c) || c >= '0' && c <= '9' }

var migrationReservedWords = map[string]bool{
	"add": true, "all": true, "alter": true, "analyze": true, "and": true, "any": true,
	"as": true, "asc": true, "authorization": true, "between": true, "bigint": true,
	"binary": true, "blob": true, "boolean": true, "both": true, "by": true, "call": true,
	"cascade": true, "case": true, "cast": true, "char": true, "check": true, "column": true,
	"constraint": true, "create": true, "cross": true, "current_date": true,
	"current_time": true, "current_timestamp": true, "database": true, "default": true,
	"delete": true, "desc": true, "distinct": true, "double": true, "drop": true, "else": true,
	"end": true, "escape": true, "exists": true, "false": true, "fetch": true, "float": true,
	"for": true, "foreign": true, "from": true, "full": true, "grant": true, "group": true,
	"having": true, "in": true, "index": true, "inner": true, "insert": true, "int": true,
	"integer": true, "intersect": true, "into": true, "is": true, "join": true, "key": true,
	"left": true, "like": true, "limit": true, "lock": true, "natural": true, "not": true,
	"nothing": true, "null": true, "numeric": true, "of": true, "offset": true, "on": true, "or": true,
	"order": true, "outer": true, "primary": true, "references": true, "right": true,
	"returning": true, "row": true, "schema": true, "select": true, "set": true, "table": true, "then": true,
	"to": true, "true": true, "union": true, "unique": true, "update": true, "user": true,
	"using": true, "values": true, "varchar": true, "view": true, "when": true, "where": true,
	"with": true,
}

type Pool struct {
	MaxOpen        int           `yaml:"max_open" env:"DB_POOL_MAX_OPEN"`
	MaxIdle        int           `yaml:"max_idle" env:"DB_POOL_MAX_IDLE"`
	MaxLifetime    time.Duration `yaml:"max_lifetime" env:"DB_POOL_MAX_LIFETIME"`
	MaxIdleTime    time.Duration `yaml:"max_idle_time" env:"DB_POOL_MAX_IDLE_TIME"`
	ConnectTimeout time.Duration `yaml:"connect_timeout" env:"DB_POOL_CONNECT_TIMEOUT,DB_CONNECT_TIMEOUT"`
}

type Params map[string]string

func (this *Params) SetValue(raw string) error {
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
	*this = out
	return nil
}

type SQLitePragmas []string

func (this *SQLitePragmas) SetValue(raw string) error {
	if raw == "" {
		*this = nil
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
	*this = out
	return nil
}

func (this SQLitePragmas) Validate() error {
	allowed := map[string]bool{
		"journal_mode": true,
		"busy_timeout": true,
		"foreign_keys": true,
		"synchronous":  true,
		"cache_size":   true,
		"temp_store":   true,
	}
	seen := make(map[string]bool, len(this))
	for _, raw := range this {
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

func defaultPort(e Engine) int {
	switch e {
	case Postgres:
		return 5432
	case MySQL, MariaDB:
		return 3306
	}
	return 0
}

func DriverName(c *Config) string {
	if c == nil {
		return ""
	}
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

func known(e Engine) bool {
	switch e {
	case Postgres, MySQL, MariaDB, SQLite:
		return true
	}
	return false
}

func (this *Config) Validate() error {
	if this == nil {
		return fmt.Errorf("%w: config", ErrMissing)
	}
	if !known(this.Engine) {
		return fmt.Errorf("%w: %q, want one of postgres, mysql, mariadb, sqlite", ErrEngine, this.Engine)
	}
	if err := this.Pool.Validate(); err != nil {
		return err
	}
	if err := this.Migration.Validate(); err != nil {
		return err
	}
	if this.DSN != "" {
		if f := this.fieldsBesideDSN(); f != "" {
			return fmt.Errorf("%w: dsn is set and so is %s — one of them would be ignored", ErrConflict, f)
		}
	} else if err := this.validateFields(); err != nil {
		return err
	}
	if this.Replica != nil {
		if this.Engine == SQLite {
			return fmt.Errorf("%w: replica is not available for sqlite", ErrUnsupported)
		}
		if !migrationEmpty(this.Replica.Migration) {
			return fmt.Errorf("%w: replica.migration belongs to the primary database", ErrUnsupported)
		}
		if replicaEmpty(*this.Replica) {
			return fmt.Errorf("%w: replica is declared but names no difference from the primary", ErrMissing)
		}
		if this.Replica.Replica != nil {
			return fmt.Errorf("%w: replica.replica, only one primary and one read replica are supported", ErrUnsupported)
		}
		if this.DSN != "" && this.Replica.DSN == "" {
			return fmt.Errorf("%w: replica fields cannot inherit from opaque dsn; set replica.dsn instead", ErrConflict)
		}
		if this.Replica.DSN != "" {
			if f := this.Replica.fieldsBesideDSN(); f != "" {
				return fmt.Errorf("replica: %w: dsn is set and so is %s — one of them would be ignored", ErrConflict, f)
			}
		}
		r, _ := this.ReadReplica()
		if r.Engine != this.Engine {
			return fmt.Errorf("%w: replica engine %q differs from %q — a replica of another engine is not a replica",
				ErrConflict, r.Engine, this.Engine)
		}

		if err := r.Validate(); err != nil {
			return fmt.Errorf("replica: %w", err)
		}
	}
	return nil
}

func (this *Config) fieldsBesideDSN() string {
	switch {
	case this.Host != "":
		return "host"
	case this.Port != 0:
		return "port"
	case this.User != "":
		return "user"
	case this.Password != "":
		return "password"
	case this.Name != "":
		return "name"
	case this.SSLMode != "":
		return "sslmode"
	case this.Path != "":
		return "path"
	case len(this.Pragmas) != 0:
		return "pragmas"
	case len(this.Params) != 0:
		return "params"
	case this.Pool.ConnectTimeout != 0:
		return "pool.connect_timeout"
	}
	return ""
}

func (this *Config) validateFields() error {
	if this.Engine == SQLite {
		if this.Path == "" {
			return fmt.Errorf("%w: path, which is the file sqlite opens", ErrMissing)
		}
		if this.Host != "" || this.Port != 0 || this.User != "" || this.Password != "" || this.Name != "" {
			return fmt.Errorf("%w: sqlite has no host, port, user, password or database name — it has a path", ErrUnsupported)
		}
		if this.SSLMode != "" {
			return fmt.Errorf("%w: sqlite is a file, not a connection, and has no sslmode", ErrUnsupported)
		}
		if this.Pool.ConnectTimeout != 0 {
			return fmt.Errorf("%w: pool.connect_timeout is for a server connection, not sqlite", ErrUnsupported)
		}
		if err := this.Pragmas.Validate(); err != nil {
			return err
		}
		return this.validateParams()
	}
	if len(this.Pragmas) != 0 {
		return fmt.Errorf("%w: pragmas are sqlite settings, and %q is not sqlite", ErrUnsupported, this.Engine)
	}
	if this.Engine == Postgres && this.Driver != "" && this.Driver != "pgx" {
		return fmt.Errorf("%w: typed postgres configuration requires driver pgx; use Config.DSN for %q", ErrUnsupported, this.Driver)
	}
	if this.Name == "" {
		return fmt.Errorf("%w: name, the database to connect to", ErrMissing)
	}
	if this.Host == "" {
		return fmt.Errorf("%w: host, the database server to connect to", ErrMissing)
	}
	if this.Port < 0 || this.Port > 65535 {
		return fmt.Errorf("%w: port %d is outside the TCP range 1..65535", ErrUnsupported, this.Port)
	}
	if strings.HasPrefix(this.Host, "[") && strings.HasSuffix(this.Host, "]") {
		return fmt.Errorf("%w: host %q is an already-bracketed IPv6 literal; use the bare address", ErrUnsupported, this.Host)
	}
	if strings.HasPrefix(this.Host, "/") && this.SSLMode != "disable" {
		return fmt.Errorf("%w: a unix socket cannot perform hostname-verified TLS; set sslmode to disable explicitly", ErrUnsupported)
	}
	if this.Password != "" && this.User == "" {
		return fmt.Errorf("%w: password is set without user, so a driver could authenticate as the process user instead", ErrConflict)
	}
	if this.Path != "" {
		return fmt.Errorf("%w: path is sqlite's, and %q is not sqlite", ErrUnsupported, this.Engine)
	}
	if _, err := tlsParam(this.Engine, this.SSLMode); err != nil {
		return err
	}
	if this.Engine != Postgres && strings.Contains(this.User, ":") {
		return fmt.Errorf("%w: a mysql user name cannot contain ':' — the driver reads everything after it as the password", ErrUnsupported)
	}
	return this.validateParams()
}

func (this *Config) validateParams() error {
	if this.Engine == SQLite {
		for key := range this.Params {
			name := strings.ToLower(strings.TrimSpace(key))
			if name == "_pragma" || name == "pragma" || name == "_journal_mode" ||
				name == "_busy_timeout" || name == "_foreign_keys" ||
				name == "_synchronous" || name == "_cache_size" || name == "_temp_store" {
				return fmt.Errorf("%w: params.%s is a driver pragma; declare it in pragmas so vvdb can validate it", ErrUnsupported, key)
			}
		}
	}
	if this.Engine == Postgres {
		if timezone, declared := this.Params["timezone"]; declared && strings.TrimSpace(timezone) == "" {
			return fmt.Errorf("%w: params.timezone must be non-empty when declared", ErrMissing)
		}
		for _, key := range []string{"service", "servicefile"} {
			if _, ok := this.Params[key]; ok {
				return fmt.Errorf("%w: params.%s makes a second configuration document; use Config.DSN when a libpq service is intentional", ErrUnsupported, key)
			}
		}
	}
	reserved := map[string]string{}
	switch this.Engine {
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

		reserved["sslmode"] = "sslmode"
		reserved["connect_timeout"] = "pool.connect_timeout"
	case MySQL, MariaDB:
		reserved["tls"] = "sslmode"

		reserved["allowFallbackToPlaintext"] = "sslmode"
		if this.Pool.ConnectTimeout != 0 {
			reserved["timeout"] = "pool.connect_timeout"
		}
	}
	for key, field := range reserved {
		if _, ok := this.Params[key]; ok {
			return fmt.Errorf("%w: params.%s conflicts with %s", ErrConflict, key, field)
		}
	}
	return nil
}

func (this *Config) ReadReplica() (*Config, bool) {
	if this == nil || this.Replica == nil || replicaEmpty(*this.Replica) || this.Replica.Replica != nil || !migrationEmpty(this.Replica.Migration) {
		return nil, false
	}

	if this.DSN != "" && this.Replica.DSN == "" {
		return nil, false
	}
	r := *this.Replica

	if r.DSN != "" {
		base := *this
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
		return &base, true
	}
	base := *this
	base.Replica = nil
	base.Migration = Migration{}
	base.DSN = ""
	base.Params = cloneParams(this.Params)
	base.Pragmas = clonePragmas(this.Pragmas)
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
	return &base, true
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

func (this *Config) ApplyEnvironment() error { return this.ApplyEnvironmentPrefix("") }

func (this *Config) ApplyEnvironmentPrefix(prefix string) error {
	if this == nil {
		return nil
	}

	if _, set := os.LookupEnv(prefix + "DB_DSN"); set {
		this.clearFieldsBesideDSN()
	}
	r := Config{}
	if this.Replica != nil {
		r = *this.Replica
		r.Params = cloneParams(r.Params)
	}

	found := false
	stringField := func(name string, destination *string) {
		name = prefix + name
		if value, ok := os.LookupEnv(name); ok {
			*destination = value
			found = true
		}
	}
	intField := func(name string, destination *int) error {
		name = prefix + name
		value, ok := os.LookupEnv(name)
		if !ok {
			return nil
		}
		n, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("parsing %s: %w", name, err)
		}
		*destination = n
		found = true
		return nil
	}
	durationField := func(name string, destination *time.Duration) error {
		name = prefix + name
		value, ok := os.LookupEnv(name)
		if !ok {
			return nil
		}
		d, err := time.ParseDuration(value)
		if err != nil {
			return fmt.Errorf("parsing %s: %w", name, err)
		}
		*destination = d
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
	if value, ok := os.LookupEnv(prefix + "DB_REPLICA_PASSWORD"); ok {
		r.Password = Secret(value)
		found = true
	}
	stringField("DB_REPLICA_NAME", &r.Name)
	stringField("DB_REPLICA_SSLMODE", &r.SSLMode)
	stringField("DB_REPLICA_PATH", &r.Path)
	if value, set := os.LookupEnv(prefix + "DB_REPLICA_DSN"); set {
		r.clearFieldsBesideDSN()
		r.DSN = Secret(value)
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
		this.Replica = &r
	}
	return nil
}

func (this *Config) clearFieldsBesideDSN() {
	this.Host, this.Port, this.User, this.Password, this.Name, this.SSLMode, this.Path = "", 0, "", "", "", "", ""
	this.Params, this.Pragmas = nil, nil
	this.Pool.ConnectTimeout = 0
}

func (this *Pool) Validate() error {
	if this == nil {
		return fmt.Errorf("%w: pool", ErrMissing)
	}
	if this.MaxOpen < 0 {
		return fmt.Errorf("%w: pool.max_open cannot be negative", ErrUnsupported)
	}
	if this.MaxOpen > 0 && this.MaxIdle > this.MaxOpen {
		return fmt.Errorf("%w: pool.max_idle %d exceeds pool.max_open %d", ErrConflict, this.MaxIdle, this.MaxOpen)
	}
	if this.MaxLifetime < 0 {
		return fmt.Errorf("%w: pool.max_lifetime cannot be negative", ErrUnsupported)
	}
	if this.MaxIdleTime < 0 {
		return fmt.Errorf("%w: pool.max_idle_time cannot be negative", ErrUnsupported)
	}
	if this.ConnectTimeout < 0 {
		return fmt.Errorf("%w: pool.connect_timeout cannot be negative", ErrUnsupported)
	}
	return nil
}
