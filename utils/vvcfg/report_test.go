package vvcfg

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frostgrove/vv/utils/vvdb"
)

type provenanceConf struct {
	Name string `yaml:"name" env:"PROVENANCE_NAME"`
	Port int    `yaml:"port" env:"PROVENANCE_PORT" env-default:"8080"`
	Note string `yaml:"note"`
}

type secretConf struct {
	DB vvdb.Config `yaml:"db"`
}

type billingConf struct {
	Billing billingBlock `yaml:"billing" env-prefix:"BILLING_"`
}

type billingBlock struct {
	Host string `yaml:"host" env:"HOST"`
}

type analyticsConf struct {
	Analytics vvdb.Config `yaml:"analytics" env-prefix:"ANALYTICS_"`
}

type renamedConf struct {
	Addr   string `yaml:"addr"`
	Listen string `yaml:"listen" vvcfg:"deprecated=use addr"`
}

func TestAMistypedKeyStopsAStrictLoadAndNamesItself(t *testing.T) {
	path := write(t, "name: x\nprot: 8080\n")

	_, _, err := LoadStrict[provenanceConf](path)
	if err == nil {
		t.Fatal("a key no field declares was accepted by a strict load")
	}
	var unknown *UnknownKeysError
	if !errors.As(err, &unknown) {
		t.Fatalf("the refusal is not an *UnknownKeysError: %v", err)
	}
	if len(unknown.Keys) != 1 || unknown.Keys[0] != "prot" {
		t.Fatalf("keys = %v, want the misspelled one", unknown.Keys)
	}

	good := write(t, "name: x\nport: 8080\n")
	if _, _, err := LoadStrict[provenanceConf](good); err != nil {
		t.Fatalf("a file whose keys all exist was refused: %v", err)
	}
}

func TestAMistypedKeyIsReportedEvenWhenTheLoadIsNotStrict(t *testing.T) {
	path := write(t, "name: x\nprot: 8080\n")
	_, report, err := LoadFrom[provenanceConf](Source{Path: path})
	if err != nil {
		t.Fatalf("a lenient load should still succeed: %v", err)
	}
	if len(report.UnknownKeys) != 1 || report.UnknownKeys[0] != "prot" {
		t.Fatalf("unknown keys = %v", report.UnknownKeys)
	}
}

func TestANestedKeyNoFieldDeclaresIsNamedWithItsPath(t *testing.T) {
	path := write(t, "billing:\n  host: pay.internal\n  hsot: pay.internal\n")
	_, _, err := LoadStrict[billingConf](path)
	var unknown *UnknownKeysError
	if !errors.As(err, &unknown) || len(unknown.Keys) != 1 || unknown.Keys[0] != "billing.hsot" {
		t.Fatalf("a nested unknown key was not reported with its path: %v", err)
	}
}

func TestTheReportSaysWhereEachValueCameFrom(t *testing.T) {
	t.Setenv("PROVENANCE_NAME", "from-the-environment")
	path := write(t, "name: from-the-file\nnote: from-the-file\n")

	_, report, err := LoadFrom[provenanceConf](Source{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if origin, _ := report.OriginOf("name"); origin != OriginEnvironment {
		t.Fatalf("name came from %s, want the environment that overrode the file", origin)
	}
	if origin, _ := report.OriginOf("note"); origin != OriginFile {
		t.Fatalf("note came from %s, want the file", origin)
	}
	if origin, _ := report.OriginOf("port"); origin != OriginDefault {
		t.Fatalf("port came from %s, want the declared default", origin)
	}
	if !strings.Contains(report.String(), "name <- environment PROVENANCE_NAME") {
		t.Fatalf("the start-up block does not name the variable that won:\n%s", report)
	}
}

func TestTheReportNamesWhereAValueCameFromAndNeverTheValue(t *testing.T) {
	const filePassword = "sentinel-file-password"
	const environmentPassword = "sentinel-environment-password"
	t.Setenv("DB_PASSWORD", environmentPassword)
	path := write(t, "db:\n  engine: postgres\n  host: db.internal\n  user: orders\n  name: orders\n  password: "+filePassword+"\n")

	config, report, err := LoadFrom[secretConf](Source{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if string(config.DB.Password) != environmentPassword {
		t.Fatal("the report was built over a configuration the loader did not produce")
	}
	rendered := report.String()
	if strings.Contains(rendered, filePassword) || strings.Contains(rendered, environmentPassword) {
		t.Fatalf("the start-up block printed a credential:\n%s", rendered)
	}
	if !strings.Contains(rendered, "db.password <- environment DB_PASSWORD") {
		t.Fatalf("the start-up block does not say where the password came from:\n%s", rendered)
	}
}

func TestAVariableUnderADeclaredPrefixThatNoFieldReadsIsRefused(t *testing.T) {
	t.Setenv("BILLING_HSOT", "pay.internal")
	path := write(t, "billing:\n  host: pay.internal\n")

	_, _, err := LoadStrict[billingConf](path)
	var unused *UnusedEnvironmentError
	if !errors.As(err, &unused) {
		t.Fatalf("a variable under a declared prefix that no field reads was accepted: %v", err)
	}
	if len(unused.Variables) != 1 || unused.Variables[0] != "BILLING_HSOT" {
		t.Fatalf("variables = %v", unused.Variables)
	}
}

func TestABlockThatAppliesItsOwnEnvironmentIsNotSecondGuessed(t *testing.T) {
	t.Setenv("ANALYTICS_DB_HOST", "analytics.internal")
	t.Setenv("ANALYTICS_DB_REPLICA_HOST", "analytics-replica.internal")
	path := write(t, "analytics:\n  engine: postgres\n  name: analytics\n  replica:\n    port: 5433\n")

	config, _, err := LoadStrict[analyticsConf](path)
	if err != nil {
		t.Fatalf("a block that reads its own environment was refused for variables it owns: %v", err)
	}
	replica, ok := config.Analytics.ReadReplica()
	if !ok || replica.Host != "analytics-replica.internal" {
		t.Fatalf("the block did not read its own environment: %+v", replica)
	}
}

func TestAValueRequiredFromTheEnvironmentRefusesAFileThatCarriesIt(t *testing.T) {
	path := write(t, "db:\n  engine: postgres\n  host: db.internal\n  user: orders\n  name: orders\n  password: in-the-repository\n")
	source := Source{Path: path, RequireEnvironment: []string{"db.password"}}

	_, _, err := LoadFrom[secretConf](source)
	var misplaced *EnvironmentSourceError
	if !errors.As(err, &misplaced) || misplaced.Path != "db.password" {
		t.Fatalf("a password committed to the file was accepted: %v", err)
	}

	t.Setenv("DB_PASSWORD", "from-the-platform")
	if _, _, err := LoadFrom[secretConf](source); err != nil {
		t.Fatalf("the same requirement refused a password that did come from the environment: %v", err)
	}
}

func TestRequiringAnUndeclaredPathIsATypoAndRefusedAsOne(t *testing.T) {
	path := write(t, "db:\n  engine: postgres\n  host: db.internal\n  name: orders\n")
	source := Source{Path: path, RequireEnvironment: []string{"db.passwrod"}}

	if _, _, err := LoadFrom[secretConf](source); !errors.Is(err, ErrUndeclaredPath) {
		t.Fatalf("a requirement naming no field was accepted: %v", err)
	}
}

func TestADeprecatedKeyIsReportedAndStillLoads(t *testing.T) {
	path := write(t, "listen: :8080\n")
	config, report, err := LoadStrict[renamedConf](path)
	if err != nil {
		t.Fatalf("a deprecated key is a warning, not a refusal: %v", err)
	}
	if config.Listen != ":8080" {
		t.Fatalf("the deprecated key was not decoded: %+v", config)
	}
	if len(report.Deprecated) != 1 || report.Deprecated[0].Path != "listen" || report.Deprecated[0].Advice != "use addr" {
		t.Fatalf("deprecations = %+v", report.Deprecated)
	}

	silent := write(t, "addr: :8080\n")
	_, report, err = LoadStrict[renamedConf](silent)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Deprecated) != 0 {
		t.Fatalf("a key nobody set was reported as deprecated: %+v", report.Deprecated)
	}
}

func writeUninspectable(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.edn")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAFileFormatThatCannotBeInspectedIsRefusedOnlyWhenStrictnessNeedsIt(t *testing.T) {
	path := writeUninspectable(t, `{:name "from-the-file"}`)

	if _, _, err := LoadStrict[provenanceConf](path); !errors.Is(err, ErrUnreadableFormat) {
		t.Fatalf("a strict load of a format nobody can inspect should refuse: %v", err)
	}

	config, _, err := LoadFrom[provenanceConf](Source{Path: path})
	if err != nil {
		t.Fatalf("a lenient load of the same file should still load it: %v", err)
	}
	if config.Name != "from-the-file" {
		t.Fatalf("the lenient load did not read the file it could not inspect: %+v", config)
	}

	inspectable := write(t, "name: from-the-file\n")
	if _, _, err := LoadStrict[provenanceConf](inspectable); err != nil {
		t.Fatalf("a format the loader does inspect was refused: %v", err)
	}
}

func TestAFileTheLoaderCouldNotInspectIsSaidSoAndItsValuesAreNotCalledDefaults(t *testing.T) {
	path := writeUninspectable(t, `{:name "from-the-file" :note "from-the-file"}`)

	config, report, err := LoadFrom[provenanceConf](Source{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if config.Name != "from-the-file" || config.Note != "from-the-file" {
		t.Fatalf("the file did not reach the struct, so provenance proves nothing here: %+v", config)
	}
	if !errors.Is(report.NotInspected, ErrUnreadableFormat) {
		t.Fatalf("the report does not admit the file was never inspected: %v", report.NotInspected)
	}
	for _, path := range []string{"name", "note", "port"} {
		if origin, _ := report.OriginOf(path); origin != OriginUnknown {
			t.Fatalf("%s is reported as %s by a load that never opened the file as a document", path, origin)
		}
	}
	if !strings.Contains(report.String(), "! the file was not inspected") {
		t.Fatalf("the start-up block hides that nothing was inspected:\n%s", report)
	}

	inspectable := write(t, "name: from-the-file\nnote: from-the-file\n")
	_, control, err := LoadFrom[provenanceConf](Source{Path: inspectable})
	if err != nil {
		t.Fatal(err)
	}
	if control.NotInspected != nil {
		t.Fatalf("a file the loader did inspect was reported as uninspected: %v", control.NotInspected)
	}
	if origin, _ := control.OriginOf("name"); origin != OriginFile {
		t.Fatalf("name came from %s, want the file — the case above proves nothing otherwise", origin)
	}
	if origin, _ := control.OriginOf("port"); origin != OriginDefault {
		t.Fatalf("port came from %s, want the declared default", origin)
	}
}
