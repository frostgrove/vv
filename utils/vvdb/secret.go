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

// Secret is configuration text that must remain usable by a connector while
// being safe to hand to ordinary formatters and structured loggers. Converting
// it explicitly with string(secret) is the deliberate escape hatch at the
// connection boundary; every display-oriented interface returns a marker.
type Secret string

func (this Secret) display() string {
	if this == "" {
		return ""
	}
	return redacted
}

func (this Secret) String() string   { return this.display() }
func (this Secret) GoString() string { return fmt.Sprintf("vvdb.Secret(%q)", this.display()) }

// Format owns every value-rendering fmt verb. Stringer alone is not enough:
// fmt falls back to the underlying string for verbs such as %d and includes
// that value in its bad-verb diagnostic. The non-value directives remain
// fmt's own contract: %p needs a pointer-like operand, and %w is valid only in
// fmt.Errorf with an error operand.
func (this Secret) Format(state fmt.State, _ rune) { _, _ = io.WriteString(state, this.display()) }

func (this Secret) MarshalJSON() ([]byte, error) { return json.Marshal(this.display()) }
func (this Secret) MarshalText() ([]byte, error) { return []byte(this.display()), nil }
func (this Secret) LogValue() slog.Value         { return slog.StringValue(this.display()) }

// UnmarshalText keeps Secret compatible with YAML and environment decoders
// that honour encoding.TextUnmarshaler.
func (this *Secret) UnmarshalText(raw []byte) error {
	if this == nil {
		return fmt.Errorf("vvdb: cannot decode a secret into nil")
	}
	*this = Secret(raw)
	return nil
}

// SetValue is the cleanenv-compatible spelling of UnmarshalText.
func (this *Secret) SetValue(raw string) error { return this.UnmarshalText([]byte(raw)) }

// String renders Params deterministically and redacts every value. The key
// vocabulary is driver-owned and open, so no name is proof that its value is
// public. This is display output, not a connection-string encoder.
func (this Params) String() string { return this.display() }

func (this Params) GoString() string { return "vvdb.Params" + this.display() }

// Format prevents a value-rendering fmt verb from falling through to the map values.
func (this Params) Format(state fmt.State, _ rune) { _, _ = io.WriteString(state, this.display()) }

// MarshalText is the dependency-free escape hatch used by text-oriented
// serializers such as TOML. Without it, a serializer that does not understand
// MarshalJSON or MarshalYAML would reflect over the map and expose its values.
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

// MarshalJSON prevents a params credential from leaking when Config is nested
// in an application-owned configuration and that outer value is marshalled.
func (this Params) MarshalJSON() ([]byte, error) {
	return json.Marshal(this.redactedCopy())
}

// MarshalYAML has the dependency-free signature understood by yaml.v3. The
// loader remains in the optional vvcfg module; vvdb itself still imports only
// the standard library.
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

// String renders a support-useful Config without its three secret-bearing
// fields. Replica is reported only as topology: recursively formatting it
// would make a self-referential configuration loop forever.
func (this Config) String() string { return this.display() }

func (this Config) GoString() string { return this.display() }

// Format owns every value-rendering fmt verb so fmt's bad-verb fallback cannot
// reflect over Password, DSN or Params. Flags, width and precision are
// intentionally ignored: truncating a redaction marker is less useful and no
// safer. As with Secret, %p retains pointer semantics and %w is only the
// fmt.Errorf error-wrapping directive.
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

// RedactedDSN returns the connection target in a support-safe form. It first
// uses the same validation and rendering path as DSN, then removes userinfo,
// every query value and fragments according to the selected engine. The real
// connection string is never placed in an error by this package.
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
	// SQLite file URIs accept either no authority or localhost. Userinfo is not
	// part of that grammar at all. Treating an arbitrary authority as a support
	// target would echo unknown input that the connector itself will reject.
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
	// Userinfo is authentication context too. Support needs the endpoint and
	// database, not the account name or a password marker.
	u.User = nil
	// A raw DSN can put a secret under any driver-defined query key, including
	// keys ordinarily considered harmless such as sslmode. Keep the endpoint
	// and database, and fail closed by dropping the query and fragment.
	u.RawQuery = ""
	u.ForceQuery = false
	u.Fragment = ""
	u.RawFragment = ""
	return u.String()
}

func redactMySQL(raw string) string {
	// Passwords in the MySQL grammar are deliberately not escaped and may
	// contain '?' or '/'. The database separator is the final slash, and only a
	// question mark after that slash starts the query.
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
		// The last @ before the database separator is the MySQL credentials
		// boundary. Drop the entire userinfo, not only the password.
		prefix = prefix[at+1:]
		beforeQuery = prefix + beforeQuery[slash:]
	}
	if strings.Contains(prefix, "://") {
		return redacted
	}
	if !knownMySQLTransport(prefix) {
		return redacted
	}
	// MySQL has an open-ended query vocabulary too. The address and database
	// are enough for support; every query value is omitted, not classified.
	return beforeQuery
}

func knownMySQLTransport(raw string) bool {
	if raw == "" {
		return true // the driver's default tcp transport and address
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
		// go-sql-driver can choose a default address only for tcp and unix.
		// A protocol-specific network therefore needs an explicit address.
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
