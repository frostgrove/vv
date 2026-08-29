package vvdb_test

import (
	"errors"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/frostgrove/vv/utils/vvdb"
)

func base(e vvdb.Engine) vvdb.Config {
	return vvdb.Config{Engine: e, Host: "db.internal", Port: 6000, User: "vv", Password: "s3cret", Name: "app"}
}

func TestEachEngineIsBuiltInItsOwnSyntax(t *testing.T) {
	for _, tc := range []struct {
		name   string
		config vvdb.Config
		want   string
	}{
		{
			name:   "postgres is a uri",
			config: base(vvdb.Postgres),
			want:   "postgres://vv:s3cret@db.internal:6000/app",
		},
		{
			name:   "mysql is not a uri",
			config: base(vvdb.MySQL),
			want:   "vv:s3cret@tcp(db.internal:6000)/app?parseTime=true",
		},
		{
			name:   "mariadb is spelled like mysql",
			config: base(vvdb.MariaDB),
			want:   "vv:s3cret@tcp(db.internal:6000)/app?parseTime=true",
		},
		{
			name:   "sqlite is a file",
			config: vvdb.Config{Engine: vvdb.SQLite, Path: "/var/lib/app.db"},
			want:   "file:/var/lib/app.db",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := vvdb.DSN(&tc.config)
			if err != nil {
				t.Fatalf("building the %s string failed: %v", tc.config.Engine, err)
			}
			if tc.config.Engine == vvdb.Postgres {
				u, err := url.Parse(got)
				if err != nil {
					t.Fatal(err)
				}
				if u.Scheme != "postgres" || u.Host != "db.internal:6000" || u.Path != "/app" || u.User.Username() != "vv" || u.Query().Get("sslmode") != "prefer" || u.Query().Get("passfile") != "" || u.Query().Get("connect_timeout") != "0" {
					t.Errorf("the postgres URI does not carry its explicit typed defaults: %s", got)
				}
				return
			}
			if got != tc.want {
				t.Errorf("the %s string is not what the driver expects\n got: %s\nwant: %s", tc.config.Engine, got, tc.want)
			}
		})
	}
}

func TestAPortLeftUnsetIsTheEnginesOwn(t *testing.T) {
	for _, tc := range []struct {
		engine vvdb.Engine
		want   string
	}{
		{vvdb.Postgres, ":5432/"},
		{vvdb.MySQL, ":3306)/"},
		{vvdb.MariaDB, ":3306)/"},
	} {
		config := base(tc.engine)
		config.Port = 0
		got, err := vvdb.DSN(&config)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(got, tc.want) {
			t.Errorf("%s with no port should reach its own default; the string was %s", tc.engine, got)
		}
	}
}

func TestAPasswordSurvivesEveryPunctuationMark(t *testing.T) {
	const nasty = `p@ss/w:rd?&=#`

	t.Run("postgres percent-encodes it", func(t *testing.T) {
		config := base(vvdb.Postgres)
		config.Password = nasty
		got, err := vvdb.PostgresDSN(&config)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(got, "@ss") {
			t.Fatalf("an unescaped '@' in the password ends the userinfo early: %s", got)
		}
		if !strings.Contains(got, "p%40ss%2Fw%3Ard%3F&=%23") {
			t.Errorf("the password is not escaped the way a URI needs: %s", got)
		}
	})

	// The mysql driver does not unescape the password, so escaping it would be
	// the bug. It finds the field by taking the last '@' before the last '/',
	// and both of those are ours. test/vvdb_test.go proves it by parsing the
	// string back with the real driver.
	t.Run("mysql leaves it alone", func(t *testing.T) {
		config := base(vvdb.MySQL)
		config.Password = nasty
		got, err := vvdb.MySQLDSN(&config)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(got, ":"+nasty+"@tcp(") {
			t.Errorf("the password should reach the driver as it was typed: %s", got)
		}
	})
}

// The failure this pins is silent and total: with `loc=Europe/Moscow` written
// out plainly, the driver scans back to the last '/' in the whole string, finds
// the one inside the value, and reads "Moscow" as the database name.
func TestAParameterHoldingASlashIsEscapedForMySQL(t *testing.T) {
	config := base(vvdb.MySQL)
	config.Params = map[string]string{"loc": "Europe/Moscow"}
	got, err := vvdb.MySQLDSN(&config)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "Europe/Moscow") {
		t.Fatalf("a '/' after the database name moves where the driver thinks the name ends: %s", got)
	}
	if !strings.Contains(got, "loc=Europe%2FMoscow") {
		t.Errorf("the parameter should arrive percent-encoded: %s", got)
	}
	if name, _, _ := strings.Cut(got[strings.LastIndex(got, "/")+1:], "?"); name != "app" {
		t.Errorf("the last '/' should still be the one before the database name, and the name should be app: %s", got)
	}
}

func TestParseTimeIsOnUnlessTheConfigTurnsItOff(t *testing.T) {
	config := base(vvdb.MySQL)
	got, err := vvdb.MySQLDSN(&config)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "parseTime=true") {
		t.Fatalf("without parseTime a DATETIME arrives as bytes and scanning into time.Time fails: %s", got)
	}
	config = base(vvdb.MySQL)
	config.Params = map[string]string{"parseTime": "false"}
	if got, err = vvdb.MySQLDSN(&config); err != nil {
		t.Fatal(err)
	} else if !strings.Contains(got, "parseTime=false") {
		t.Errorf("params should win over the default: %s", got)
	}
}

func TestAUnixSocketIsNotAHost(t *testing.T) {
	pg := base(vvdb.Postgres)
	pg.Host = "/var/run/postgresql"
	got, err := vvdb.PostgresDSN(&pg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "host=%2Fvar%2Frun%2Fpostgresql") || !strings.Contains(got, "@/app") {
		t.Errorf("a socket directory belongs in the query, not in the authority: %s", got)
	}

	my := base(vvdb.MySQL)
	my.Host = "/tmp/mysql.sock"
	if got, err = vvdb.MySQLDSN(&my); err != nil {
		t.Fatal(err)
	} else if !strings.Contains(got, "unix(/tmp/mysql.sock)") {
		t.Errorf("mysql spells a socket unix(...), not tcp(...): %s", got)
	}
}

func TestAnIPv6HostKeepsItsBrackets(t *testing.T) {
	config := base(vvdb.Postgres)
	config.Host = "::1"
	got, err := vvdb.PostgresDSN(&config)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "[::1]:6000") {
		t.Errorf("an IPv6 address without brackets makes the port part of the address: %s", got)
	}
}

func TestASubSecondConnectTimeoutDoesNotBecomeForever(t *testing.T) {
	config := base(vvdb.Postgres)
	config.Pool.ConnectTimeout = 500 * time.Millisecond
	got, err := vvdb.PostgresDSN(&config)
	if err != nil {
		t.Fatal(err)
	}
	// connect_timeout is whole seconds and 0 means no timeout at all, so
	// rounding down is the one answer that must not happen.
	if !strings.Contains(got, "connect_timeout=1") {
		t.Errorf("half a second should round up to one, not down to none: %s", got)
	}
}

func TestSSLModeIsSpelledOnceAndTranslated(t *testing.T) {
	for _, tc := range []struct {
		engine vvdb.Engine
		mode   string
		want   string
	}{
		{vvdb.Postgres, "verify-full", "sslmode=verify-full"},
		{vvdb.MySQL, "verify-full", "tls=true"},
		{vvdb.MySQL, "require", "tls=skip-verify"},
		{vvdb.MySQL, "disable", "tls=false"},
		{vvdb.MariaDB, "prefer", "tls=preferred"},
	} {
		config := base(tc.engine)
		config.SSLMode = tc.mode
		got, err := vvdb.DSN(&config)
		if err != nil {
			t.Fatalf("%s %s: %v", tc.engine, tc.mode, err)
		}
		if !strings.Contains(got, tc.want) {
			t.Errorf("%s should carry sslmode %s as %s; the string was %s", tc.engine, tc.mode, tc.want, got)
		}
	}
}

func TestWhatAnEngineCannotExpressIsRefusedRatherThanDowngraded(t *testing.T) {
	config := base(vvdb.MySQL)
	config.SSLMode = "verify-ca"
	_, err := vvdb.MySQLDSN(&config)
	if !errors.Is(err, vvdb.ErrUnsupported) {
		t.Fatalf("verify-ca has no mysql spelling; quietly sending skip-verify would claim a verification nobody does. got %v", err)
	}
}

func TestADSNIsUsedAsGivenAndRefusesToShareTheJob(t *testing.T) {
	const raw = "postgres://someone:else@elsewhere:5432/other?sslmode=require"

	got, err := vvdb.DSN(&vvdb.Config{Engine: vvdb.Postgres, DSN: raw})
	if err != nil {
		t.Fatal(err)
	}
	if got != raw {
		t.Errorf("a DSN handed in should come back untouched\n got: %s\nwant: %s", got, raw)
	}

	_, err = vvdb.DSN(&vvdb.Config{Engine: vvdb.Postgres, DSN: raw, Host: "db.internal", Name: "app"})
	if !errors.Is(err, vvdb.ErrConflict) {
		t.Fatalf("a dsn beside a host means one of the two is ignored and nobody is told which; got %v", err)
	}

	_, err = vvdb.DSN(&vvdb.Config{Engine: vvdb.Postgres, DSN: raw, Pool: vvdb.Pool{ConnectTimeout: time.Second}})
	if !errors.Is(err, vvdb.ErrConflict) || !strings.Contains(err.Error(), "pool.connect_timeout") {
		t.Fatalf("a raw DSN and a pool timeout pick different sources in different adapters; got %v", err)
	}
}

func TestSQLiteEscapesFilenameSyntaxBeforeAddingParameters(t *testing.T) {
	config := vvdb.Config{Engine: vvdb.SQLite, Path: "/tmp/report?draft#1.db", Params: map[string]string{"mode": "rwc"}}
	got, err := vvdb.SQLiteDSN(&config)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	if u.Path != config.Path || u.Query().Get("mode") != "rwc" {
		t.Fatalf("SQLiteDSN() = %q parses as path %q, query %q; want original filename and parameters", got, u.Path, u.RawQuery)
	}
}

func TestSQLitePragmasKeepBothDurabilityAndLockSettings(t *testing.T) {
	config := vvdb.Config{
		Engine:  vvdb.SQLite,
		Path:    "/tmp/vv.db",
		Pragmas: vvdb.SQLitePragmas{"journal_mode=WAL", "busy_timeout=5000"},
	}
	got, err := vvdb.SQLiteDSN(&config)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	if values := u.Query()["_pragma"]; !reflect.DeepEqual(values, []string{"journal_mode(WAL)", "busy_timeout(5000)"}) {
		t.Fatalf("modernc pragmas = %#v, want both repeated settings", values)
	}

	config.Driver = "sqlite3"
	got, err = vvdb.SQLiteDSN(&config)
	if err != nil {
		t.Fatal(err)
	}
	u, err = url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	if u.Query().Get("_journal_mode") != "WAL" || u.Query().Get("_busy_timeout") != "5000" {
		t.Fatalf("mattn pragmas = %q, want its two driver settings", got)
	}

	for _, bad := range []vvdb.Config{
		{Engine: vvdb.SQLite, Path: "/tmp/vv.db", Pragmas: vvdb.SQLitePragmas{"journal_mode=WAL; DROP TABLE x"}},
		{Engine: vvdb.SQLite, Path: "/tmp/vv.db", Pragmas: vvdb.SQLitePragmas{"journal_mode=not_a_mode"}},
		{Engine: vvdb.SQLite, Path: "/tmp/vv.db", Pragmas: vvdb.SQLitePragmas{"busy_timeout=banana"}},
		{Engine: vvdb.SQLite, Path: "/tmp/vv.db", Params: vvdb.Params{"_pragma": "writable_schema(ON)"}},
		{Engine: vvdb.Postgres, Host: "db", Name: "app", Pragmas: vvdb.SQLitePragmas{"busy_timeout=5000"}},
	} {
		if _, err := vvdb.DSN(&bad); !errors.Is(err, vvdb.ErrUnsupported) {
			t.Fatalf("DSN(%+v) = %v, want its invalid pragma named", bad, err)
		}
	}

	// Validation is case-insensitive, and rendering has to be too: mattn reads
	// lower-case URI names, so preserving JOURNAL_MODE would silently drop it.
	config = vvdb.Config{Engine: vvdb.SQLite, Driver: "sqlite3", Path: "/tmp/vv.db", Pragmas: vvdb.SQLitePragmas{"JOURNAL_MODE=WAL"}}
	if got, err := vvdb.SQLiteDSN(&config); err != nil || !strings.Contains(got, "_journal_mode=WAL") {
		t.Fatalf("case-normalized sqlite pragma = %q, %v", got, err)
	}
}

func TestDSNUsesTheSameFullValidationAsOpen(t *testing.T) {
	config := base(vvdb.Postgres)
	config.Pool = vvdb.Pool{MaxOpen: 1, MaxIdle: 2}
	if _, err := vvdb.DSN(&config); !errors.Is(err, vvdb.ErrConflict) {
		t.Fatalf("DSN() = %v, want the impossible pool refused before it becomes a handle", err)
	}

	config = base(vvdb.Postgres)
	config.Replica = &vvdb.Config{Host: "replica", SSLMode: "not-a-mode"}
	if _, err := vvdb.DSN(&config); err == nil || !strings.Contains(err.Error(), "replica") {
		t.Fatalf("DSN() = %v, want a named invalid replica refusal", err)
	}
	if _, err := vvdb.PostgresDSN(&config); err == nil || !strings.Contains(err.Error(), "replica") {
		t.Fatalf("PostgresDSN() = %v, want the same invalid replica refusal", err)
	}
}

func TestParamsCannotOverrideTypedConnectionSettings(t *testing.T) {
	for _, tc := range []struct {
		name   string
		config vvdb.Config
	}{
		{"postgres TLS", func() vvdb.Config {
			c := base(vvdb.Postgres)
			c.SSLMode = "require"
			c.Params = map[string]string{"sslmode": "disable"}
			return c
		}()},
		{"postgres default TLS", func() vvdb.Config {
			c := base(vvdb.Postgres)
			c.Params = map[string]string{"sslmode": "disable"}
			return c
		}()},
		{"postgres default connect timeout", func() vvdb.Config {
			c := base(vvdb.Postgres)
			c.Params = map[string]string{"connect_timeout": "5"}
			return c
		}()},
		{"postgres socket host", func() vvdb.Config {
			c := base(vvdb.Postgres)
			c.Host = "/var/run/postgresql"
			c.Params = map[string]string{"host": "other.internal"}
			return c
		}()},
		{"mysql TLS", func() vvdb.Config {
			c := base(vvdb.MySQL)
			c.SSLMode = "require"
			c.Params = map[string]string{"tls": "skip-verify"}
			return c
		}()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := vvdb.DSN(&tc.config); !errors.Is(err, vvdb.ErrConflict) {
				t.Fatalf("DSN() = %v, want two sources of truth refused", err)
			}
		})
	}
}

func TestInvalidTCPPortAndBracketedIPv6AreRefusedBeforeTheDriver(t *testing.T) {
	for _, tc := range []vvdb.Config{
		func() vvdb.Config { c := base(vvdb.Postgres); c.Port = -1; return c }(),
		func() vvdb.Config { c := base(vvdb.MySQL); c.Port = 65536; return c }(),
		func() vvdb.Config { c := base(vvdb.Postgres); c.Host = "[2001:db8::1]"; return c }(),
	} {
		if _, err := vvdb.DSN(&tc); !errors.Is(err, vvdb.ErrUnsupported) {
			t.Fatalf("DSN(%+v) = %v, want a named boundary refusal", tc, err)
		}
	}
}

func TestAnUnknownEngineIsRefused(t *testing.T) {
	for _, e := range []vvdb.Engine{"", "postgresql", "PostgreSQL", "cockroach"} {
		if _, err := vvdb.DSN(&vvdb.Config{Engine: e, Name: "app"}); !errors.Is(err, vvdb.ErrEngine) {
			t.Errorf("engine %q should be refused rather than read as the default; got %v", e, err)
		}
	}
}

func TestAFieldThatBelongsToAnotherEngineIsRefused(t *testing.T) {
	for _, tc := range []struct {
		name   string
		config vvdb.Config
	}{
		{"sqlite has no host", vvdb.Config{Engine: vvdb.SQLite, Path: "/tmp/a.db", Host: "db.internal"}},
		{"sqlite has no sslmode", vvdb.Config{Engine: vvdb.SQLite, Path: "/tmp/a.db", SSLMode: "require"}},
		{"postgres has no path", vvdb.Config{Engine: vvdb.Postgres, Name: "app", Path: "/tmp/a.db"}},
		{"sqlite needs its path", vvdb.Config{Engine: vvdb.SQLite}},
		{"the others need a name", vvdb.Config{Engine: vvdb.Postgres, Host: "db.internal"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := vvdb.DSN(&tc.config); err == nil {
				t.Error("this configuration cannot mean what it says and was accepted anyway")
			}
		})
	}
}

// A colon in the user is the one thing the mysql driver reads wrongly and
// cannot be worked around, because it splits at the *first* one.
func TestAMySQLUserCannotHoldAColon(t *testing.T) {
	config := base(vvdb.MySQL)
	config.User = "svc:reader"
	if _, err := vvdb.MySQLDSN(&config); !errors.Is(err, vvdb.ErrUnsupported) {
		t.Fatalf("the driver would read \"reader\" and everything after it as the password; got %v", err)
	}
	pg := base(vvdb.Postgres)
	pg.User = "svc:reader"
	if _, err := vvdb.PostgresDSN(&pg); err != nil {
		t.Errorf("postgres escapes it and has no such limit: %v", err)
	}
}
