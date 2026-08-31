package dsn

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/frostgrove/vv/utils/vvdb"
	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
	_ "modernc.org/sqlite"
)

const nasty = `p@ss/w:rd?&=#`

func TestPgxReadsBackWhatVvdbWrote(t *testing.T) {
	c := vvdb.Config{
		Engine: vvdb.Postgres, Host: "db.internal", Port: 6000,
		User: "vv", Password: nasty, Name: "app/one", SSLMode: "disable",
		Params: map[string]string{"application_name": "orders service"},
	}
	dsn, err := vvdb.PostgresDSN(&c)
	if err != nil {
		t.Fatal(err)
	}
	got, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("pgx cannot read the string vvdb built: %v", err)
	}
	if got.User != "vv" {
		t.Errorf("user came back as %q", got.User)
	}
	if got.Password != nasty {
		t.Errorf("the password came back mangled\n got: %q\nwant: %q", got.Password, nasty)
	}
	if got.Database != "app/one" {
		t.Errorf("database came back as %q", got.Database)
	}
	if got.Host != "db.internal" || got.Port != 6000 {
		t.Errorf("the server came back as %s:%d", got.Host, got.Port)
	}
	if got.RuntimeParams["application_name"] != "orders service" {
		t.Errorf("a parameter with a space came back as %q", got.RuntimeParams["application_name"])
	}
}

func TestPgxFindsTheSocketVvdbPutInTheQuery(t *testing.T) {
	c := vvdb.Config{Engine: vvdb.Postgres, Host: "/var/run/postgresql", User: "vv", Name: "app", SSLMode: "disable"}
	dsn, err := vvdb.PostgresDSN(&c)
	if err != nil {
		t.Fatal(err)
	}
	got, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("pgx cannot read the socket string vvdb built: %v", err)
	}
	if got.Host != "/var/run/postgresql" {
		t.Errorf("the socket directory came back as %q", got.Host)
	}
}

func TestPgxDoesNotMergeAmbientPostgresSettingsIntoATypedConfig(t *testing.T) {
	t.Setenv("PGHOST", "ambient.internal")
	t.Setenv("PGPORT", "6543")
	t.Setenv("PGDATABASE", "ambient")
	t.Setenv("PGUSER", "ambient-user")
	t.Setenv("PGPASSWORD", "ambient-password")
	t.Setenv("PGPASSFILE", filepath.Join(t.TempDir(), "pgpass"))
	t.Setenv("PGSSLCERT", filepath.Join(t.TempDir(), "ambient-client.crt"))
	t.Setenv("PGSSLKEY", filepath.Join(t.TempDir(), "ambient-client.key"))
	t.Setenv("PGSSLROOTCERT", filepath.Join(t.TempDir(), "ambient-root.crt"))
	t.Setenv("PGSSLPASSWORD", "ambient-certificate-password")
	t.Setenv("PGSSLSNI", "0")
	t.Setenv("PGTARGETSESSIONATTRS", "read-write")
	t.Setenv("PGSSLMODE", "disable")
	t.Setenv("PGCONNECT_TIMEOUT", "13")
	t.Setenv("PGAPPNAME", "ambient-app")
	t.Setenv("PGOPTIONS", "-c search_path=ambient")

	c := vvdb.Config{Engine: vvdb.Postgres, Host: "declared.internal", Port: 5432, User: "declared", Name: "orders"}
	dsn, err := vvdb.PostgresDSN(&c)
	if err != nil {
		t.Fatal(err)
	}
	got, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("pgx could not parse vvdb's URI: %v", err)
	}
	if got.Host != "declared.internal" || got.Port != 5432 || got.Database != "orders" || got.User != "declared" || got.Password != "" {
		t.Fatalf("ambient PG* values leaked into typed config: host=%q port=%d database=%q user=%q password=%q", got.Host, got.Port, got.Database, got.User, got.Password)
	}
	if got.TLSConfig == nil {
		t.Fatal("PGSSLMODE=disable leaked through; an empty sslmode is vvdb's verified-TLS default")
	}
	if got.TLSConfig.InsecureSkipVerify || got.TLSConfig.ServerName != "declared.internal" {
		t.Fatalf("typed PostgreSQL TLS is not hostname-verified: skip=%v server=%q", got.TLSConfig.InsecureSkipVerify, got.TLSConfig.ServerName)
	}
	if got.ConnectTimeout != 0 || got.RuntimeParams["application_name"] != "" || got.RuntimeParams["timezone"] != "" || got.RuntimeParams["options"] != "" {
		t.Fatalf("ambient PostgreSQL runtime settings leaked into parsed config: timeout=%s params=%#v", got.ConnectTimeout, got.RuntimeParams)
	}
	if got.ValidateConnect != nil {
		t.Fatal("ambient PGTARGETSESSIONATTRS changed the typed connection selection policy")
	}
	if got.MinProtocolVersion != "3.0" || got.MaxProtocolVersion != "3.0" || got.ChannelBinding != "prefer" || got.RequireAuth != "" {
		t.Fatalf("ambient protocol/auth policy leaked into typed config: min=%q max=%q channel_binding=%q require_auth=%q",
			got.MinProtocolVersion, got.MaxProtocolVersion, got.ChannelBinding, got.RequireAuth)
	}
}

func TestPgxAmbientRuntimePolicyNeedsAnExplicitTypedDeclaration(t *testing.T) {
	t.Setenv("PGTZ", "Europe/Almaty")
	t.Setenv("PGMINPROTOCOLVERSION", "3.2")
	t.Setenv("PGMAXPROTOCOLVERSION", "latest")
	t.Setenv("PGCHANNELBINDING", "disable")
	t.Setenv("PGREQUIREAUTH", "scram-sha-256")
	c := vvdb.Config{
		Engine: vvdb.Postgres, Host: "declared.internal", Name: "orders",
		Params: vvdb.Params{
			"timezone":             "UTC",
			"min_protocol_version": "3.0",
			"max_protocol_version": "3.0",
			"channel_binding":      "prefer",
			"require_auth":         "",
		},
	}
	dsn, err := vvdb.PostgresDSN(&c)
	if err != nil {
		t.Fatal(err)
	}
	got, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if got.MinProtocolVersion != "3.0" || got.MaxProtocolVersion != "3.0" || got.ChannelBinding != "prefer" || got.RequireAuth != "" {
		t.Fatalf("explicit protocol/auth policy lost to ambient values: min=%q max=%q channel_binding=%q require_auth=%q",
			got.MinProtocolVersion, got.MaxProtocolVersion, got.ChannelBinding, got.RequireAuth)
	}
	if got.RuntimeParams["timezone"] != "UTC" {
		t.Fatalf("explicit timezone lost to PGTZ: %q", got.RuntimeParams["timezone"])
	}
}

func TestPgxKeepsItsDependentProtocolDefaultWhenOnlyTheMinimumIsExplicit(t *testing.T) {
	c := vvdb.Config{
		Engine: vvdb.Postgres, Host: "db.internal", Name: "orders",
		Params: vvdb.Params{"min_protocol_version": "3.2"},
	}
	dsn, err := vvdb.PostgresDSN(&c)
	if err != nil {
		t.Fatal(err)
	}
	got, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("explicit minimum should let pgx choose its compatible maximum: %v", err)
	}
	if got.MinProtocolVersion != "3.2" || got.MaxProtocolVersion != "latest" {
		t.Fatalf("protocol range = %q..%q, want 3.2..latest", got.MinProtocolVersion, got.MaxProtocolVersion)
	}
}

func TestTheMySQLDriverReadsBackWhatVvdbWrote(t *testing.T) {
	c := vvdb.Config{
		Engine: vvdb.MySQL, Host: "db.internal", Port: 6000,
		User: "vv", Password: nasty, Name: "app",

		Params: map[string]string{"loc": "Europe/Moscow"},
	}
	dsn, err := vvdb.MySQLDSN(&c)
	if err != nil {
		t.Fatal(err)
	}
	got, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("go-sql-driver cannot read the string vvdb built: %v", err)
	}
	if got.User != "vv" {
		t.Errorf("user came back as %q", got.User)
	}
	if got.Passwd != nasty {
		t.Errorf("the password came back mangled\n got: %q\nwant: %q", got.Passwd, nasty)
	}
	if got.DBName != "app" {
		t.Errorf("database came back as %q — this is what an unescaped parameter does", got.DBName)
	}
	if got.Addr != "db.internal:6000" {
		t.Errorf("the server came back as %q", got.Addr)
	}
	if got.Loc == nil || got.Loc.String() != "Europe/Moscow" {
		t.Errorf("the location came back as %v", got.Loc)
	}
	if !got.ParseTime {
		t.Error("parseTime is a default vvdb writes, and without it a DATETIME arrives as bytes")
	}
	if got.TLSConfig != "true" || got.TLS == nil || got.TLS.InsecureSkipVerify || got.TLS.ServerName != "db.internal" {
		t.Errorf("empty sslmode should produce hostname-verified MySQL TLS; name=%q tls=%#v", got.TLSConfig, got.TLS)
	}
	if got.AllowFallbackToPlaintext {
		t.Error("typed verified TLS must not retry a failed handshake in plaintext")
	}
}

func TestTheMySQLDriverFindsTheSocketVvdbWrote(t *testing.T) {
	c := vvdb.Config{Engine: vvdb.MySQL, Host: "/tmp/mysql.sock", User: "vv", Name: "app", SSLMode: "disable"}
	dsn, err := vvdb.MySQLDSN(&c)
	if err != nil {
		t.Fatal(err)
	}
	got, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("go-sql-driver cannot read the socket string vvdb built: %v", err)
	}
	if got.Net != "unix" || got.Addr != "/tmp/mysql.sock" {
		t.Errorf("the socket came back as %s(%s)", got.Net, got.Addr)
	}
}

func TestRedactedMySQLTargetsFailClosedWhereTheDriverRejectsTheGrammar(t *testing.T) {
	for name, raw := range map[string]string{
		"invalid database escape":       "tcp(db.internal:3306)/orders%sentinel-password",
		"tcp4 without required address": "tcp4/orders-sentinel-password",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := mysql.ParseDSN(raw); err == nil {
				t.Fatal("control failed: go-sql-driver accepted the allegedly invalid DSN")
			}
			got, err := vvdb.RedactedDSN(&vvdb.Config{Engine: vvdb.MySQL, DSN: vvdb.Secret(raw)})
			if err != nil {
				t.Fatal(err)
			}
			if got != "[REDACTED]" {
				t.Fatalf("invalid driver grammar became a support target: %q", got)
			}
		})
	}
}

func TestSQLiteOpensTheEscapedFilenameItWasGiven(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report ?#%.db")
	dsn, err := vvdb.SQLiteDSN(&vvdb.Config{
		Engine:  vvdb.SQLite,
		Path:    path,
		Params:  vvdb.Params{"mode": "rwc"},
		Pragmas: vvdb.SQLitePragmas{"journal_mode=WAL", "busy_timeout=5000"},
	})
	if err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec("CREATE TABLE proof (id INTEGER PRIMARY KEY)"); err != nil {
		t.Fatalf("sqlite did not open vvdb's escaped URI %q: %v", dsn, err)
	}
	var journal string
	if err := database.QueryRow("PRAGMA journal_mode").Scan(&journal); err != nil || journal != "wal" {
		t.Fatalf("sqlite journal mode = %q (%v), want wal from the repeated vvdb pragma", journal, err)
	}
	var timeout int
	if err := database.QueryRow("PRAGMA busy_timeout").Scan(&timeout); err != nil || timeout != 5000 {
		t.Fatalf("sqlite busy timeout = %d (%v), want 5000 from the repeated vvdb pragma", timeout, err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("sqlite did not create the intended filename %q: %v", path, err)
	}
}

func TestRedactedSQLiteTargetFailsClosedOnAnAuthorityTheDriverRejects(t *testing.T) {
	raw := "file://sentinel-password/tmp/orders.db"
	database, err := sql.Open("sqlite", raw)
	if err == nil {
		err = database.Ping()
		_ = database.Close()
	}
	if err == nil {
		t.Fatal("control failed: SQLite accepted a non-local file URI authority")
	}
	got, err := vvdb.RedactedDSN(&vvdb.Config{Engine: vvdb.SQLite, DSN: vvdb.Secret(raw)})
	if err != nil {
		t.Fatal(err)
	}
	if got != "[REDACTED]" {
		t.Fatalf("invalid SQLite URI authority became a support target: %q", got)
	}
}

func TestAnUnescapedParameterIsWhyTheEscapingExists(t *testing.T) {
	broken := `vv:pw@tcp(db.internal:6000)/app?loc=Europe/Moscow&parseTime=true`
	if _, err := mysql.ParseDSN(broken); err == nil {
		t.Fatal("go-sql-driver accepted an unescaped '/' in a parameter; if this ever passes, the escaping test above proves nothing")
	}
}
