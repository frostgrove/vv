package vvdb

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

const redacted = "[REDACTED]"

type Secret string

func (this Secret) display() string {
	if this == "" {
		return ""
	}
	return redacted
}

func (this Secret) String() string   { return this.display() }
func (this Secret) GoString() string { return fmt.Sprintf("vvdb.Secret(%q)", this.display()) }

func (this Secret) Format(state fmt.State, _ rune) { _, _ = io.WriteString(state, this.display()) }

func (this Secret) MarshalJSON() ([]byte, error) { return json.Marshal(this.display()) }
func (this Secret) MarshalText() ([]byte, error) { return []byte(this.display()), nil }
func (this Secret) LogValue() slog.Value         { return slog.StringValue(this.display()) }

func (this *Secret) UnmarshalText(raw []byte) error {
	if this == nil {
		return fmt.Errorf("vvdb: cannot decode a secret into nil")
	}
	*this = Secret(raw)
	return nil
}

func (this *Secret) SetValue(raw string) error { return this.UnmarshalText([]byte(raw)) }

func (this Params) String() string { return this.display() }

func (this Params) GoString() string { return "vvdb.Params" + this.display() }

func (this Params) Format(state fmt.State, _ rune) { _, _ = io.WriteString(state, this.display()) }

func (this Params) MarshalText() ([]byte, error) { return []byte(this.display()), nil }

func (this Params) display() string {
	keys := make([]string, 0, len(this))
	for key := range this {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteByte('{')
	for i, key := range keys {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(key)
		b.WriteByte(':')
		b.WriteString(redacted)
	}
	b.WriteByte('}')
	return b.String()
}

func (this Params) MarshalJSON() ([]byte, error) {
	return json.Marshal(this.redactedCopy())
}

func (this Params) MarshalYAML() (any, error) { return this.redactedCopy(), nil }

func (this Params) redactedCopy() map[string]string {
	copyOf := make(map[string]string, len(this))
	for key := range this {
		copyOf[key] = redacted
	}
	return copyOf
}

func (this Params) LogValue() slog.Value {
	keys := make([]string, 0, len(this))
	for key := range this {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	attrs := make([]slog.Attr, 0, len(keys))
	for _, key := range keys {
		attrs = append(attrs, slog.String(key, redacted))
	}
	return slog.GroupValue(attrs...)
}

func (this Config) String() string { return this.display() }

func (this Config) GoString() string { return this.display() }

func (this Config) Format(state fmt.State, _ rune) { _, _ = io.WriteString(state, this.display()) }

func (this Config) display() string {
	var b strings.Builder
	b.WriteString("vvdb.Config{Engine:")
	b.WriteString(strconv.Quote(string(this.Engine)))
	b.WriteString(" Driver:")
	b.WriteString(strconv.Quote(this.Driver))
	b.WriteString(" Host:")
	b.WriteString(strconv.Quote(this.Host))
	b.WriteString(" Port:")
	b.WriteString(strconv.Itoa(this.Port))
	b.WriteString(" User:")
	b.WriteString(strconv.Quote(this.User))
	b.WriteString(" Password:")
	b.WriteString(this.Password.display())
	b.WriteString(" Name:")
	b.WriteString(strconv.Quote(this.Name))
	b.WriteString(" SSLMode:")
	b.WriteString(strconv.Quote(this.SSLMode))
	b.WriteString(" Path:")
	b.WriteString(strconv.Quote(this.Path))
	b.WriteString(" Pragmas:")
	b.WriteString(strconv.Itoa(len(this.Pragmas)))
	b.WriteString(" Params:")
	b.WriteString(strconv.Itoa(len(this.Params)))
	b.WriteString(" values redacted Pool:{MaxOpen:")
	b.WriteString(strconv.Itoa(this.Pool.MaxOpen))
	b.WriteString(" MaxIdle:")
	b.WriteString(strconv.Itoa(this.Pool.MaxIdle))
	b.WriteString(" MaxLifetime:")
	b.WriteString(this.Pool.MaxLifetime.String())
	b.WriteString(" MaxIdleTime:")
	b.WriteString(this.Pool.MaxIdleTime.String())
	b.WriteString(" ConnectTimeout:")
	b.WriteString(this.Pool.ConnectTimeout.String())
	b.WriteString("} Replica:")
	b.WriteString(strconv.FormatBool(this.Replica != nil))
	b.WriteString(" DSN:")
	b.WriteString(this.DSN.display())
	b.WriteByte('}')
	return b.String()
}

func (this Config) LogValue() slog.Value { return slog.StringValue(this.display()) }

func RedactedDSN(c *Config) (string, error) {
	dsn, err := DSN(c)
	if err != nil {
		return "", err
	}
	switch c.Engine {
	case Postgres:
		target := redactURL(dsn, "postgres", "postgresql")
		if c.DSN == "" && strings.HasPrefix(c.Host, "/") && target != redacted {
			target += " [unix:" + c.Host + "]"
		}
		return target, nil
	case SQLite:
		if c.DSN == "" {
			return redactTypedSQLite(dsn), nil
		}
		return redactRawSQLite(dsn), nil
	case MySQL, MariaDB:
		return redactMySQL(dsn), nil
	default:
		return redacted, nil
	}
}

func redactRawSQLite(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || !strings.EqualFold(u.Scheme, "file") || u.Opaque != "" {
		return redacted
	}

	if u.User != nil || (u.Host != "" && !strings.EqualFold(u.Host, "localhost")) {
		return redacted
	}
	u.RawQuery = ""
	u.ForceQuery = false
	u.Fragment = ""
	u.RawFragment = ""
	return u.String()
}

func redactURL(raw string, allowedSchemes ...string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Opaque != "" {
		return redacted
	}
	allowed := false
	for _, scheme := range allowedSchemes {
		if strings.EqualFold(u.Scheme, scheme) {
			allowed = true
			break
		}
	}
	if !allowed {
		return redacted
	}

	u.User = nil

	u.RawQuery = ""
	u.ForceQuery = false
	u.Fragment = ""
	u.RawFragment = ""
	return u.String()
}

func redactMySQL(raw string) string {
	slash := strings.LastIndexByte(raw, '/')
	if slash < 0 {
		return redacted
	}
	queryAt := strings.IndexByte(raw[slash+1:], '?')
	if queryAt >= 0 {
		queryAt += slash + 1
	}
	beforeQuery := raw
	if queryAt >= 0 {
		beforeQuery = raw[:queryAt]
	}
	databaseName := beforeQuery[slash+1:]
	if _, err := url.PathUnescape(databaseName); err != nil {
		return redacted
	}
	prefix := beforeQuery[:slash]
	if at := strings.LastIndexByte(prefix, '@'); at >= 0 {
		prefix = prefix[at+1:]
		beforeQuery = prefix + beforeQuery[slash:]
	}
	if strings.Contains(prefix, "://") {
		return redacted
	}
	if !knownMySQLTransport(prefix) {
		return redacted
	}

	return beforeQuery
}

func knownMySQLTransport(raw string) bool {
	if raw == "" {
		return true
	}
	name := raw
	hasAddress := false
	if open := strings.IndexByte(raw, '('); open >= 0 {
		if open == 0 || raw[len(raw)-1] != ')' || strings.Contains(raw[open+1:len(raw)-1], "(") {
			return false
		}
		name = raw[:open]
		hasAddress = raw[open+1:len(raw)-1] != ""
	}
	switch name {
	case "tcp", "unix":
		return true
	case "tcp4", "tcp6":
		return hasAddress
	default:
		return false
	}
}

func redactTypedSQLite(raw string) string {
	if len(raw) < len("file:") || !strings.EqualFold(raw[:len("file:")], "file:") {
		return redacted
	}
	end := len(raw)
	if query := strings.IndexByte(raw, '?'); query >= 0 && query < end {
		end = query
	}
	if fragment := strings.IndexByte(raw, '#'); fragment >= 0 && fragment < end {
		end = fragment
	}
	return raw[:end]
}
