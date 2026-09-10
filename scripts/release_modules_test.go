package scripts

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestReleaseModulesRewriteAllPublishedRequirementsAndPreserveFixtures(t *testing.T) {
	root := t.TempDir()
	writeReleaseFixture(t, root, "go.mod", "module example.com/vv\n\ngo 1.25\n")
	writeReleaseFixture(t, root, "root.go", "package root\n\nfunc Value() int { return 1 }\n")
	writeReleaseFixture(t, root, "leaf/go.mod", "module example.com/vv/leaf\n\ngo 1.25\n")
	writeReleaseFixture(t, root, "leaf/leaf.go", "package leaf\n\nfunc Value() int { return 1 }\n")
	writeReleaseFixture(t, root, "nested/go.mod", "module example.com/vv/nested\n\ngo 1.25\n\nrequire (\n\texample.com/vv v0.0.0-00010101000000-000000000000\n\texample.com/vv/leaf v0.0.0 // indirect\n)\n\nreplace example.com/vv => ..\n\nreplace example.com/vv/leaf => ../leaf\n")
	writeReleaseFixture(t, root, "nested/nested.go", "package nested\n\nimport (\n\troot \"example.com/vv\"\n\t\"example.com/vv/leaf\"\n)\n\nfunc Value() int { return root.Value() + leaf.Value() }\n")
	writeReleaseFixture(t, root, "client/go.mod", "module example.com/vv/client\n\ngo 1.25\n\nrequire (\n\texample.com/vv/nested v0.0.0\n\texample.com/vv/leaf v0.0.0 // indirect\n)\n\nreplace (\n\texample.com/vv/nested => ../nested\n\texample.com/vv/leaf => ../leaf\n)\n")
	writeReleaseFixture(t, root, "client/client.go", "package client\n\nimport \"example.com/vv/nested\"\n\nfunc Value() int { return nested.Value() }\n")
	unpublished := "module example.com/vv/test\n\ngo 1.25\n\nrequire example.com/vv v0.0.0\n\nreplace example.com/vv => ..\n"
	writeReleaseFixture(t, root, "test/go.mod", unpublished)
	writeReleaseFixture(t, root, "_examples/go.mod", strings.Replace(unpublished, "/test", "/_examples", 1))

	output, err := runReleaseModules(t, root, "v0.9.0", "rewrite")
	if err != nil {
		t.Fatalf("rewrite failed: %v\n%s", err, output)
	}
	for _, module := range []string{"nested", "client"} {
		manifest := readReleaseFixture(t, filepath.Join(root, module, "go.mod"))
		if strings.Contains(manifest, "v0.0.0") || strings.Contains(manifest, "replace ") {
			t.Fatalf("%s was not release-normalized:\n%s", module, manifest)
		}
		for _, line := range strings.Split(manifest, "\n") {
			if strings.Contains(line, "example.com/vv") && strings.Contains(line, " v") && !strings.Contains(line, " v0.9.0") {
				t.Fatalf("%s retains a non-exact first-party requirement: %s", module, line)
			}
		}
	}
	if got := readReleaseFixture(t, filepath.Join(root, "test/go.mod")); got != unpublished {
		t.Fatalf("test manifest changed:\n%s", got)
	}
	if got := readReleaseFixture(t, filepath.Join(root, "_examples/go.mod")); got != strings.Replace(unpublished, "/test", "/_examples", 1) {
		t.Fatalf("example manifest changed:\n%s", got)
	}
}

func TestReleaseModulesPreflightRejectsIndirectStaleVersionAndEveryReplace(t *testing.T) {
	root := t.TempDir()
	writeReleaseFixture(t, root, "go.mod", "module example.com/vv\n\ngo 1.25\n\nrequire example.com/vv/nested v0.8.0 // indirect\n\nreplace example.org/upstream => ./fork\n")
	writeReleaseFixture(t, root, "nested/go.mod", "module example.com/vv/nested\n\ngo 1.25\n")
	output, err := runReleaseModules(t, root, "v0.9.0", "check")
	if err == nil {
		t.Fatalf("preflight accepted stale indirect requirement and replace:\n%s", output)
	}
	if !strings.Contains(output, "first-party example.com/vv/nested@v0.8.0") || !strings.Contains(output, "release-forbidden replace") {
		t.Fatalf("preflight did not identify both violations:\n%s", output)
	}
}

func runReleaseModules(t *testing.T, root, version, task string) (string, error) {
	t.Helper()
	command := exec.Command("bash", "./release-modules.sh", task)
	command.Env = append(os.Environ(),
		"GO=go",
		"GOPROXY=off",
		"V="+version,
		"VV_RELEASE_ROOT="+root,
		"VV_RELEASE_MODULE=example.com/vv",
	)
	output, err := command.CombinedOutput()
	return string(output), err
}

func writeReleaseFixture(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readReleaseFixture(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}
