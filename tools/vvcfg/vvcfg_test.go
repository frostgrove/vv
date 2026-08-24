package vvcfg

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type conf struct {
	Name string `yaml:"name" env:"NAME"`
	Port int    `yaml:"port" env:"PORT"`
}

type strictConf struct {
	Port int `yaml:"port"`
}

func (c *strictConf) Validate() error {
	if c.Port == 0 {
		return errors.New("port is required")
	}
	return nil
}

func write(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadReadsThePathItIsGiven(t *testing.T) {
	// The defect this pins: MustLoad took a variadic path and ignored it,
	// reading --config-path instead. A caller passing a path got a different
	// file, silently.
	p := write(t, "name: from-the-argument\nport: 1\n")
	got, err := Load[conf](p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Name != "from-the-argument" {
		t.Fatalf("name = %q: Load did not read the path it was given", got.Name)
	}
}

func TestAMissingFileAndAnUnreadableOneAreDifferentMessages(t *testing.T) {
	_, missing := Load[conf](filepath.Join(t.TempDir(), "nope.yaml"))
	if missing == nil || !strings.Contains(missing.Error(), "no such file") {
		t.Fatalf("a missing file should say so, got %v", missing)
	}
	// The original checked only os.IsNotExist, so anything else fell through to
	// the decoder and surfaced as "failed to read config" — the wrong place to
	// go looking.
	dir := t.TempDir()
	_, isDir := Load[conf](dir)
	if isDir == nil {
		t.Fatal("a directory is not a configuration file")
	}
	if strings.Contains(isDir.Error(), "no such file") {
		t.Fatalf("a directory reported as missing: %v", isDir)
	}
}

func TestValidateRefusesTheProcessAtStartUp(t *testing.T) {
	p := write(t, "port: 0\n")
	_, err := Load[strictConf](p)
	if err == nil {
		t.Fatal("a config that fails its own Validate should not load")
	}
	if !strings.Contains(err.Error(), "port is required") {
		t.Fatalf("the validation error should reach the caller, got %v", err)
	}

	ok := write(t, "port: 8080\n")
	if _, err := Load[strictConf](ok); err != nil {
		t.Fatalf("a valid config should load: %v", err)
	}
}

func TestAConfigWithoutValidateIsLoadedAsIs(t *testing.T) {
	// The control for the test above: without it, a Validate that never runs
	// would look identical to a Validate that always passes.
	p := write(t, "name: x\nport: 0\n")
	if _, err := Load[conf](p); err != nil {
		t.Fatalf("a config with no Validate method has nothing to refuse it: %v", err)
	}
}

func TestFindPrefersTheFlagOverTheEnvironment(t *testing.T) {
	t.Setenv("CONFIG_PATH", "/from/env.yaml")

	got, err := Find([]string{"--config-path", "/from/flag.yaml"})
	if err != nil || got != "/from/flag.yaml" {
		t.Fatalf("the flag should win, got %q (%v)", got, err)
	}

	got, err = Find(nil)
	if err != nil || got != "/from/env.yaml" {
		t.Fatalf("the environment should be the fallback, got %q (%v)", got, err)
	}
}

func TestNoPathAtAllIsAnErrorRatherThanAGuess(t *testing.T) {
	t.Setenv("CONFIG_PATH", "")
	if _, err := Find(nil); !errors.Is(err, ErrNoPath) {
		t.Fatalf("with nothing configured, Find should refuse: %v", err)
	}
	if _, err := Load[conf](""); !errors.Is(err, ErrNoPath) {
		t.Fatalf("Load(\"\") should refuse rather than stat the empty path: %v", err)
	}
}

func TestAutoFindsThenLoads(t *testing.T) {
	p := write(t, "name: via-auto\nport: 2\n")
	got, err := Auto[conf]([]string{"--config-path", p})
	if err != nil {
		t.Fatalf("Auto: %v", err)
	}
	if got.Name != "via-auto" {
		t.Fatalf("name = %q, want via-auto", got.Name)
	}
}

func TestMustPanicsRatherThanReturningNil(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("Must should panic on an error, not hand back a nil config")
		}
	}()
	Must(Load[conf](filepath.Join(t.TempDir(), "nope.yaml")))
}
