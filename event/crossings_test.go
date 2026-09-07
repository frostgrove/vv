package event

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const crossings = "testdata/crossings"

func stageCrossings(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("the module root is not addressable: %v", err)
	}
	staged := t.TempDir()
	module := "module crossings\n\ngo 1.26\n\nrequire github.com/frostgrove/vv v0.0.0\n\nreplace github.com/frostgrove/vv => " + root + "\n"
	if err := os.WriteFile(filepath.Join(staged, "go.mod"), []byte(module), 0o644); err != nil {
		t.Fatalf("the fixture module cannot be written: %v", err)
	}
	fixtures, err := os.ReadDir(crossings)
	if err != nil {
		t.Fatalf("the crossing fixtures cannot be read: %v", err)
	}
	for _, fixture := range fixtures {
		source := filepath.Join(crossings, fixture.Name())
		target := filepath.Join(staged, fixture.Name())
		if err := os.MkdirAll(target, 0o755); err != nil {
			t.Fatalf("%s cannot be staged: %v", fixture.Name(), err)
		}
		files, err := os.ReadDir(source)
		if err != nil {
			t.Fatalf("%s cannot be read: %v", fixture.Name(), err)
		}
		for _, file := range files {
			content, err := os.ReadFile(filepath.Join(source, file.Name()))
			if err != nil {
				t.Fatalf("%s cannot be read: %v", file.Name(), err)
			}
			if err := os.WriteFile(filepath.Join(target, file.Name()), content, 0o644); err != nil {
				t.Fatalf("%s cannot be staged: %v", file.Name(), err)
			}
		}
	}
	return staged
}

func buildCrossing(t *testing.T, staged, fixture string) (string, error) {
	t.Helper()
	build := exec.Command("go", "build", "./"+fixture)
	build.Dir = staged
	build.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod", "GOPROXY=off")
	response, err := build.CombinedOutput()
	return string(response), err
}

func TestTheCrossingsThatMustNotCompile(t *testing.T) {
	staged := stageCrossings(t)

	if response, err := buildCrossing(t, staged, "control"); err != nil {
		t.Fatalf("the matching combination does not build, so every refusal below is \"nothing compiles\":\n%s", response)
	}

	for _, crossing := range []struct {
		fixture string
		what    string
	}{
		{"change", "a change decided for an aggregate over one state type, folded through an aggregate over another"},
		{"identity", "a fact minted with another aggregate's identity type"},
	} {
		response, err := buildCrossing(t, staged, crossing.fixture)
		if err == nil {
			t.Fatalf("%s compiles, so the crossing has to be caught at a call rather than at the build:\n%s", crossing.what, response)
		}
	}
}
