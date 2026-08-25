package vvdb

import (
	"errors"
	"fmt"
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
	// default for the engine — "pgx", "mysql", "sqlite". It is a field rather
	// than a constant because the name belongs to whichever driver the consumer
	// blank-imported: lib/pq registers "postgres" where pgx registers "pgx",
	// and both are PostgreSQL.
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
	// Params are added to the connection string as they are, after whatever
	// escaping the engine's parser needs. A key this package already writes is
	// overridden by the one here.
	Params map[string]string `yaml:"params"`

	Pool Pool `yaml:"pool"`

	// Replica is a second server for reads that may be served stale. It
	// inherits every field left empty here, so a replica that differs only by
	// hostname is one line. See [Config.ReadReplica] and crud.ReadWrite.
	Replica *Config `yaml:"replica"`

	// DSN is the escape hatch: a connection string somebody else produced,
	// used exactly as given. It refuses to work half way — set it and leave
	// every field it would override empty, or [Config.Validate] fails and
	// names both.
	DSN string `yaml:"dsn" env:"DB_DSN"`
}

// A Pool sizes the connections. The four limits are database/sql's and pgx
// reads them too; ConnectTimeout is the only one that travels in the
// connection string, because that is where every driver looks for it.
type Pool struct {
	MaxOpen        int           `yaml:"max_open" env:"DB_POOL_MAX_OPEN"`
	MaxIdle        int           `yaml:"max_idle" env:"DB_POOL_MAX_IDLE"`
	MaxLifetime    time.Duration `yaml:"max_lifetime" env:"DB_POOL_MAX_LIFETIME"`
	MaxIdleTime    time.Duration `yaml:"max_idle_time" env:"DB_POOL_MAX_IDLE_TIME"`
	ConnectTimeout time.Duration `yaml:"connect_timeout" env:"DB_CONNECT_TIMEOUT"`
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
	if c.DSN != "" {
		if f := c.fieldsBesideDSN(); f != "" {
			return fmt.Errorf("%w: dsn is set and so is %s — one of them would be ignored", ErrConflict, f)
		}
	} else if err := c.validateFields(); err != nil {
		return err
	}
	if c.Replica != nil {
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
	case len(c.Params) != 0:
		return "params"
	}
	return ""
}

func (c Config) validateFields() error {
	if c.Engine == SQLite {
		if c.Path == "" {
			return fmt.Errorf("%w: path, which is the file sqlite opens", ErrMissing)
		}
		if c.Host != "" || c.User != "" || c.Password != "" || c.Name != "" {
			return fmt.Errorf("%w: sqlite has no host, user, password or database name — it has a path", ErrUnsupported)
		}
		if c.SSLMode != "" {
			return fmt.Errorf("%w: sqlite is a file, not a connection, and has no sslmode", ErrUnsupported)
		}
		return nil
	}
	if c.Name == "" {
		return fmt.Errorf("%w: name, the database to connect to", ErrMissing)
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
	if c.Replica == nil {
		return Config{}, false
	}
	r := *c.Replica
	// A replica named by a whole DSN inherits nothing: the string is complete
	// by definition, and merging fields into it could only contradict it.
	if r.DSN != "" {
		if r.Engine == "" {
			r.Engine = c.Engine
		}
		r.Replica = nil
		return r, true
	}
	base := c
	base.Replica = nil
	base.DSN = ""
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
	if r.Pool != (Pool{}) {
		base.Pool = r.Pool
	}
	if len(r.Params) > 0 {
		merged := make(map[string]string, len(base.Params)+len(r.Params))
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
