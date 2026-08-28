package vvdb

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// DSN builds the connection string for the engine the config names.
//
// "DSN" covers both shapes on purpose. PostgreSQL's is a URI and MySQL's is not
// one — `user:pass@tcp(host:3306)/db` parses by no URI rule at all — and data
// source name is the only word that fits both.
func DSN(c *Config) (string, error) {
	if c == nil {
		return "", fmt.Errorf("%w: config", ErrMissing)
	}
	if err := c.Validate(); err != nil {
		return "", err
	}
	switch c.Engine {
	case Postgres:
		return PostgresDSN(c)
	case MySQL:
		return MySQLDSN(c)
	case MariaDB:
		return MariaDBDSN(c)
	case SQLite:
		return SQLiteDSN(c)
	}
	return "", fmt.Errorf("%w: %q, want one of postgres, mysql, mariadb, sqlite", ErrEngine, c.Engine)
}

// The four builders take the engine from the function they are, not from the
// config, so a caller who knows which server it is talking to does not have to
// say it twice.
//
// They are four functions and not one switch because MySQL and MariaDB share a
// driver, a wire protocol and every rule below, and are still two declarations:
// the same reasoning that gives crudsql four constructors where three would
// compile ([[D-046]]).

// PostgresDSN builds `postgres://user:password@host:port/name?...`.
//
// The whole string goes through net/url, which is the only reason a password
// containing '@' or '/' reaches the server intact.
func PostgresDSN(cfg *Config) (string, error) {
	c, dsn, err := prepare(cfg, Postgres)
	if err != nil || dsn != "" {
		return dsn, err
	}
	// A libpq service is a second, opaque document. pgx consults it before the
	// URI's ordinary settings, so refuse it loudly instead of silently making
	// PGSERVICE part of an otherwise declarative Config. Raw DSN remains the
	// explicit escape hatch for applications that intentionally use a service.
	if os.Getenv("PGSERVICE") != "" {
		return "", fmt.Errorf("%w: PGSERVICE is set; use Config.DSN when a libpq service is intentional", ErrConflict)
	}
	if os.Getenv("PGSSLNEGOTIATION") != "" {
		// sslnegotiation is pgx-specific, so emitting an empty query key would
		// make the database/sql lib/pq driver reject an otherwise portable URI.
		return "", fmt.Errorf("%w: PGSSLNEGOTIATION is set; put the intended driver setting in Config.Params", ErrConflict)
	}

	u := url.URL{Scheme: "postgres", Path: "/" + c.Name}
	switch {
	case c.User != "" && c.Password != "":
		u.User = url.UserPassword(c.User, c.Password)
	case c.User != "":
		u.User = url.User(c.User)
	}

	q := url.Values{}
	// pgx merges its process defaults and PG* environment after parsing only the
	// fields absent from a URI. Render every connection fact vvdb owns,
	// including empty credentials and the ordinary defaults, so the named config
	// rather than a shell or ~/.pgpass decides the connection.
	q.Set("user", c.User)
	q.Set("password", c.Password)
	q.Set("dbname", c.Name)
	sslmode := c.SSLMode
	if sslmode == "" {
		sslmode = "prefer"
	}
	q.Set("sslmode", sslmode)
	q.Set("connect_timeout", "0")
	q.Set("passfile", "")
	q.Set("sslcert", "")
	q.Set("sslkey", "")
	q.Set("sslrootcert", "")
	q.Set("sslpassword", "")
	q.Set("sslsni", "")
	q.Set("target_session_attrs", "any")
	q.Set("application_name", "")
	q.Set("timezone", "")
	q.Set("options", "")
	host, port := c.Host, c.Port
	if port == 0 {
		port = defaultPort(Postgres)
	}
	if strings.HasPrefix(host, "/") {
		// A unix socket is a directory, and a directory in the host position of
		// a URI is not one. pgx reads it out of the query.
		q.Set("host", host)
		q.Set("port", strconv.Itoa(port))
	} else {
		if host == "" {
			host = "localhost"
		}
		u.Host = net.JoinHostPort(host, strconv.Itoa(port))
		q.Set("host", host)
		q.Set("port", strconv.Itoa(port))
	}
	if s := seconds(c.Pool.ConnectTimeout); s != "" {
		q.Set("connect_timeout", s)
	}
	for k, v := range c.Params {
		q.Set(k, v)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// MySQLDSN builds `user:password@tcp(host:port)/name?...`.
func MySQLDSN(c *Config) (string, error) { return mysqlish(c, MySQL) }

// MariaDBDSN builds the same shape as [MySQLDSN]. They are two functions
// because MariaDB and MySQL are two engines, and the first thing that differs
// between them — a CHECK constraint's error number already does ([[D-046]]) —
// should have somewhere to be written.
func MariaDBDSN(c *Config) (string, error) { return mysqlish(c, MariaDB) }

func mysqlish(cfg *Config, e Engine) (string, error) {
	c, dsn, err := prepare(cfg, e)
	if err != nil || dsn != "" {
		return dsn, err
	}

	var b strings.Builder
	if c.User != "" {
		b.WriteString(c.User)
		if c.Password != "" {
			// Not escaped, and it does not need to be: the driver takes the
			// last '@' before the last '/', and both of those are ours.
			b.WriteByte(':')
			b.WriteString(c.Password)
		}
		b.WriteByte('@')
	}
	host, port := c.Host, c.Port
	if port == 0 {
		port = defaultPort(e)
	}
	if strings.HasPrefix(host, "/") {
		b.WriteString("unix(" + host + ")")
	} else {
		if host == "" {
			host = "localhost"
		}
		b.WriteString("tcp(" + net.JoinHostPort(host, strconv.Itoa(port)) + ")")
	}
	// The driver PathUnescapes the database name, so a name it would decode
	// has to arrive encoded.
	b.WriteByte('/')
	b.WriteString(url.PathEscape(c.Name))

	q := url.Values{}
	// parseTime is a default rather than a choice, and it is the one default
	// here that changes what the database returns. Without it a DATETIME
	// arrives as []byte, scanning it into a time.Time field fails, and the
	// failure names the column rather than the missing parameter — which is a
	// long afternoon for whoever meets it first.
	q.Set("parseTime", "true")
	if tls, err := tlsParam(e, c.SSLMode); err != nil {
		return "", err
	} else if tls != "" {
		q.Set("tls", tls)
	}
	if d := c.Pool.ConnectTimeout; d > 0 {
		q.Set("timeout", d.String())
	}
	for k, v := range c.Params {
		q.Set(k, v)
	}
	// Encode escapes the values, and that is load-bearing rather than tidy:
	// the driver finds the database name by scanning back to the *last* '/' in
	// the whole string, so an unescaped `loc=Europe/Moscow` makes it read
	// "Moscow" as the database and fail on everything before it.
	b.WriteByte('?')
	b.WriteString(q.Encode())
	return b.String(), nil
}

// SQLiteDSN builds `file:path?...`. There is no server, so there is no host,
// no user and no TLS — [Config.Validate] refuses all three rather than
// dropping them.
func SQLiteDSN(cfg *Config) (string, error) {
	c, dsn, err := prepare(cfg, SQLite)
	if err != nil || dsn != "" {
		return dsn, err
	}
	q := url.Values{}
	for k, v := range c.Params {
		q.Set(k, v)
	}
	if len(c.Pragmas) > 0 {
		for _, raw := range c.Pragmas {
			name, value, _ := strings.Cut(raw, "=") // Validate already checked it.
			name, value = strings.ToLower(strings.TrimSpace(name)), strings.TrimSpace(value)
			switch DriverName(&c) {
			case "", "sqlite":
				// modernc.org/sqlite consumes one _pragma query item per
				// setting. Values.Add, not Set, is what keeps WAL and a busy
				// timeout together in the URI.
				q.Add("_pragma", name+"("+value+")")
			case "sqlite3":
				// mattn/go-sqlite3 names these connection parameters directly.
				q.Set("_"+name, value)
			default:
				return "", fmt.Errorf("%w: sqlite pragmas are mapped for drivers sqlite and sqlite3; use Params or a raw dsn for %q", ErrUnsupported, DriverName(&c))
			}
		}
	}
	// A SQLite URI has a path and a query just like a server URI. Concatenating
	// c.Path would turn '?' and '#' in an actual filename into URI syntax. Keep
	// SQLite's conventional file:/absolute-path spelling while borrowing URL's
	// path escaper for the part that is a filename rather than URI grammar.
	u := url.URL{Path: c.Path}
	dsn = "file:" + u.EscapedPath()
	if encoded := q.Encode(); encoded != "" {
		dsn += "?" + encoded
	}
	return dsn, nil
}

// prepare settles the two questions every builder asks first: is this config
// usable at all, and did somebody hand us a finished string. A non-empty second
// result is that string and means the builder has nothing left to do.
func prepare(c *Config, e Engine) (Config, string, error) {
	if c == nil {
		return Config{}, "", fmt.Errorf("%w: config", ErrMissing)
	}
	prepared := *c
	prepared.Engine = e
	if err := prepared.Validate(); err != nil {
		return prepared, "", err
	}
	if prepared.DSN != "" {
		return prepared, prepared.DSN, nil
	}
	return prepared, "", nil
}

// tlsParam translates SSLMode into what the MySQL driver understands, and
// answers "" for an engine that reads SSLMode directly.
//
// The vocabulary is PostgreSQL's for every engine, because one config has to
// spell it one way. What MySQL cannot express is refused by name: verify-ca
// needs a tls.Config registered with the driver, and a silent downgrade to
// skip-verify would be an encrypted connection to an unverified server that
// the configuration claims is verified.
func tlsParam(e Engine, mode string) (string, error) {
	if mode == "" {
		return "", nil
	}
	switch mode {
	case "disable", "allow", "prefer", "require", "verify-ca", "verify-full":
	default:
		return "", fmt.Errorf("%w: sslmode %q, want disable, allow, prefer, require, verify-ca or verify-full",
			ErrUnsupported, mode)
	}
	if e == Postgres {
		return "", nil
	}
	switch mode {
	case "disable":
		return "false", nil
	case "allow", "prefer":
		return "preferred", nil
	case "require":
		return "skip-verify", nil
	case "verify-full":
		return "true", nil
	}
	return "", fmt.Errorf("%w: %s cannot express sslmode verify-ca — register a tls.Config with mysql.RegisterTLSConfig and name it in params.tls",
		ErrUnsupported, e)
}

// seconds renders a duration the way libpq's connect_timeout wants it: whole
// seconds, rounded up. Rounding down would turn 500ms into 0, and 0 there does
// not mean "immediately" — it means "wait forever".
func seconds(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	return strconv.Itoa(int((d + time.Second - 1) / time.Second))
}
