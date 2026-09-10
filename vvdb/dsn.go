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

func PostgresDSN(config *Config) (string, error) {
	c, dsn, err := prepare(config, Postgres)
	if err != nil || dsn != "" {
		return dsn, err
	}

	if os.Getenv("PGSERVICE") != "" {
		return "", fmt.Errorf("%w: PGSERVICE is set; use Config.DSN when a libpq service is intentional", ErrConflict)
	}
	if os.Getenv("PGSSLNEGOTIATION") != "" {
		return "", fmt.Errorf("%w: PGSSLNEGOTIATION is set; use Config.DSN when that driver-specific negotiation mode is intentional", ErrConflict)
	}

	if os.Getenv("PGTZ") != "" {
		if timezone, declared := c.Params["timezone"]; !declared || strings.TrimSpace(timezone) == "" {
			return "", fmt.Errorf("%w: PGTZ is set; unset it, declare a non-empty params.timezone, or use a complete Config.DSN that owns the setting", ErrConflict)
		}
	}

	for _, setting := range []struct{ environment, parameter string }{
		{"PGMINPROTOCOLVERSION", "min_protocol_version"},
		{"PGMAXPROTOCOLVERSION", "max_protocol_version"},
		{"PGCHANNELBINDING", "channel_binding"},
		{"PGREQUIREAUTH", "require_auth"},
	} {
		if os.Getenv(setting.environment) == "" {
			continue
		}
		if _, declared := c.Params[setting.parameter]; !declared {
			return "", fmt.Errorf("%w: %s is set; unset it, declare params.%s with a compatible pgx version, or use a complete Config.DSN that owns the setting",
				ErrConflict, setting.environment, setting.parameter)
		}
	}

	u := url.URL{Scheme: "postgres", Path: "/" + c.Name}
	switch {
	case c.User != "" && c.Password != "":
		u.User = url.UserPassword(c.User, string(c.Password))
	case c.User != "":
		u.User = url.User(c.User)
	}

	q := url.Values{}

	q.Set("user", c.User)
	q.Set("password", string(c.Password))
	q.Set("dbname", c.Name)
	sslmode := c.SSLMode
	if sslmode == "" {
		sslmode = "verify-full"
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
	q.Set("options", "")
	host, port := c.Host, c.Port
	if port == 0 {
		port = defaultPort(Postgres)
	}
	if strings.HasPrefix(host, "/") {
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

func MySQLDSN(c *Config) (string, error) { return mysqlish(c, MySQL) }

func MariaDBDSN(c *Config) (string, error) { return mysqlish(c, MariaDB) }

func mysqlish(config *Config, e Engine) (string, error) {
	c, dsn, err := prepare(config, e)
	if err != nil || dsn != "" {
		return dsn, err
	}

	var b strings.Builder
	if c.User != "" {
		b.WriteString(c.User)
		if c.Password != "" {
			b.WriteByte(':')
			b.WriteString(string(c.Password))
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

	b.WriteByte('/')
	b.WriteString(url.PathEscape(c.Name))

	q := url.Values{}

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

	b.WriteByte('?')
	b.WriteString(q.Encode())
	return b.String(), nil
}

func SQLiteDSN(config *Config) (string, error) {
	c, dsn, err := prepare(config, SQLite)
	if err != nil || dsn != "" {
		return dsn, err
	}
	q := url.Values{}
	for k, v := range c.Params {
		q.Set(k, v)
	}
	if len(c.Pragmas) > 0 {
		for _, raw := range c.Pragmas {
			name, value, _ := strings.Cut(raw, "=")
			name, value = strings.ToLower(strings.TrimSpace(name)), strings.TrimSpace(value)
			switch DriverName(&c) {
			case "", "sqlite":
				q.Add("_pragma", name+"("+value+")")
			case "sqlite3":
				q.Set("_"+name, value)
			default:
				return "", fmt.Errorf("%w: sqlite pragmas are mapped for drivers sqlite and sqlite3; use Params or a raw dsn for %q", ErrUnsupported, DriverName(&c))
			}
		}
	}

	u := url.URL{Path: c.Path}
	dsn = "file:" + u.EscapedPath()
	if encoded := q.Encode(); encoded != "" {
		dsn += "?" + encoded
	}
	return dsn, nil
}

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
		return prepared, string(prepared.DSN), nil
	}
	return prepared, "", nil
}

func tlsParam(e Engine, mode string) (string, error) {
	if mode == "" {
		mode = "verify-full"
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
	return "", fmt.Errorf("%w: %s cannot express sslmode verify-ca in typed fields — register a tls.Config with mysql.RegisterTLSConfig and use a raw Config.DSN that names it as tls=<name>",
		ErrUnsupported, e)
}

func seconds(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	return strconv.Itoa(int((d + time.Second - 1) / time.Second))
}
