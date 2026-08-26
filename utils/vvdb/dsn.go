package vvdb

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DSN builds the connection string for the engine the config names.
//
// "DSN" covers both shapes on purpose. PostgreSQL's is a URI and MySQL's is not
// one — `user:pass@tcp(host:3306)/db` parses by no URI rule at all — and data
// source name is the only word that fits both.
func DSN(c Config) (string, error) {
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
func PostgresDSN(c Config) (string, error) {
	c, dsn, err := prepare(c, Postgres)
	if err != nil || dsn != "" {
		return dsn, err
	}

	u := url.URL{Scheme: "postgres", Path: "/" + c.Name}
	switch {
	case c.User != "" && c.Password != "":
		u.User = url.UserPassword(c.User, c.Password)
	case c.User != "":
		u.User = url.User(c.User)
	}

	q := url.Values{}
	host, port := c.Host, c.Port
	if port == 0 {
		port = defaultPort(Postgres)
	}
	if strings.HasPrefix(host, "/") {
		// A unix socket is a directory, and a directory in the host position of
		// a URI is not one. libpq reads it out of the query instead, and pgx
		// follows libpq.
		q.Set("host", host)
		q.Set("port", strconv.Itoa(port))
	} else {
		if host == "" {
			host = "localhost"
		}
		u.Host = net.JoinHostPort(host, strconv.Itoa(port))
	}
	if c.SSLMode != "" {
		q.Set("sslmode", c.SSLMode)
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
func MySQLDSN(c Config) (string, error) { return mysqlish(c, MySQL) }

// MariaDBDSN builds the same shape as [MySQLDSN]. They are two functions
// because MariaDB and MySQL are two engines, and the first thing that differs
// between them — a CHECK constraint's error number already does ([[D-046]]) —
// should have somewhere to be written.
func MariaDBDSN(c Config) (string, error) { return mysqlish(c, MariaDB) }

func mysqlish(c Config, e Engine) (string, error) {
	c, dsn, err := prepare(c, e)
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
func SQLiteDSN(c Config) (string, error) {
	c, dsn, err := prepare(c, SQLite)
	if err != nil || dsn != "" {
		return dsn, err
	}
	q := url.Values{}
	for k, v := range c.Params {
		q.Set(k, v)
	}
	if len(q) == 0 {
		return "file:" + c.Path, nil
	}
	return "file:" + c.Path + "?" + q.Encode(), nil
}

// prepare settles the two questions every builder asks first: is this config
// usable at all, and did somebody hand us a finished string. A non-empty second
// result is that string and means the builder has nothing left to do.
func prepare(c Config, e Engine) (Config, string, error) {
	c.Engine = e
	if c.DSN != "" {
		if f := c.fieldsBesideDSN(); f != "" {
			return c, "", fmt.Errorf("%w: dsn is set and so is %s — one of them would be ignored", ErrConflict, f)
		}
		return c, c.DSN, nil
	}
	if err := c.validateFields(); err != nil {
		return c, "", err
	}
	return c, "", nil
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
