// Package dsn holds the assertions that vvdb's connection strings are read
// back by the drivers the way they were written.
//
// vvdb is in the root module and takes no third-party dependency, so it cannot
// import pgx or go-sql-driver to check its own work; on its own it can only
// compare strings, and a string comparison agrees with a rule this repository
// invented. These tests parse with the real parsers, which is where the two
// engines' escaping rules actually live ([[FL-021]]).
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

// The password holds every character that means something to one of the two
// parsers: '@' ends the userinfo, '/' ends the address, ':' starts the
// password, and '?' starts the query.
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
	c := vvdb.Config{Engine: vvdb.Postgres, Host: "/var/run/postgresql", User: "vv", Name: "app"}
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
	t.Setenv("PGSSLMODE", "disable")
	t.Setenv("PGCONNECT_TIMEOUT", "13")
	t.Setenv("PGAPPNAME", "ambient-app")
	t.Setenv("PGTZ", "Europe/Almaty")
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
		t.Fatal("PGSSLMODE=disable leaked through; the declared empty sslmode is vvdb's explicit prefer default")
	}
	if got.ConnectTimeout != 0 || got.RuntimeParams["application_name"] != "" || got.RuntimeParams["timezone"] != "" || got.RuntimeParams["options"] != "" {
		t.Fatalf("ambient PostgreSQL runtime settings leaked into parsed config: timeout=%s params=%#v", got.ConnectTimeout, got.RuntimeParams)
	}
}

func TestTheMySQLDriverReadsBackWhatVvdbWrote(t *testing.T) {
	c := vvdb.Config{
		Engine: vvdb.MySQL, Host: "db.internal", Port: 6000,
		User: "vv", Password: nasty, Name: "app",
		// Europe/Moscow is the parameter that breaks an unescaped string
		// outright: the driver looks for the *last* '/' in the whole DSN to
		// find where the database name ends.
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
}

func TestTheMySQLDriverFindsTheSocketVvdbWrote(t *testing.T) {
	c := vvdb.Config{Engine: vvdb.MySQL, Host: "/tmp/mysql.sock", User: "vv", Name: "app"}
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
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("CREATE TABLE proof (id INTEGER PRIMARY KEY)"); err != nil {
		t.Fatalf("sqlite did not open vvdb's escaped URI %q: %v", dsn, err)
	}
	var journal string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&journal); err != nil || journal != "wal" {
		t.Fatalf("sqlite journal mode = %q (%v), want wal from the repeated vvdb pragma", journal, err)
	}
	var timeout int
	if err := db.QueryRow("PRAGMA busy_timeout").Scan(&timeout); err != nil || timeout != 5000 {
		t.Fatalf("sqlite busy timeout = %d (%v), want 5000 from the repeated vvdb pragma", timeout, err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("sqlite did not create the intended filename %q: %v", path, err)
	}
}

// The control: the escaping is not decoration. Written out plainly, the same
// parameter moves where the driver thinks the database name ends, and the
// error is a connection to a database nobody named.
func TestAnUnescapedParameterIsWhyTheEscapingExists(t *testing.T) {
	broken := `vv:pw@tcp(db.internal:6000)/app?loc=Europe/Moscow&parseTime=true`
	if _, err := mysql.ParseDSN(broken); err == nil {
		t.Fatal("go-sql-driver accepted an unescaped '/' in a parameter; if this ever passes, the escaping test above proves nothing")
	}
}
