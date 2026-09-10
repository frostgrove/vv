package scripts

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestAPIGenerationPublishesACompleteResultAtomically(t *testing.T) {
	root, command, baseline, tool := apiGenerationFixture(t)
	if err := os.WriteFile(tool, []byte("#!/usr/bin/env bash\nprintf '## example.test/api\\n```go\\nconst Answer untyped int = 42\\n```\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if output, err := command(root, tool).CombinedOutput(); err != nil {
		t.Fatalf("api generation failed: %v\n%s", err, output)
	}
	content, err := os.ReadFile(baseline)
	if err != nil {
		t.Fatal(err)
	}
	surface := string(content)
	if !strings.Contains(surface, "# Exported surface at the first tag") || !strings.Contains(surface, "const Answer untyped int = 42") {
		t.Fatalf("published API surface is incomplete:\n%s", surface)
	}
	if !strings.HasSuffix(surface, "```\n") || strings.HasSuffix(surface, "```\n\n") {
		t.Fatalf("published API surface has a non-canonical ending %q", surface[max(0, len(surface)-8):])
	}
}

func TestAPIGenerationFailurePreservesThePublishedBaseline(t *testing.T) {
	root, command, baseline, tool := apiGenerationFixture(t)
	const original = "approved surface\n"
	if err := os.WriteFile(baseline, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tool, []byte("#!/usr/bin/env bash\nexit 41\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if output, err := command(root, tool).CombinedOutput(); err == nil {
		t.Fatalf("failing API inspection succeeded:\n%s", output)
	}
	content, err := os.ReadFile(baseline)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != original {
		t.Fatalf("failed generation replaced the approved baseline with %q", content)
	}
}

func apiGenerationFixture(t *testing.T) (string, func(string, string) *exec.Cmd, string, string) {
	t.Helper()
	root := t.TempDir()
	scriptDirectory := filepath.Join(root, "scripts")
	if err := os.MkdirAll(filepath.Join(root, "docs", "api"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(scriptDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	modules, err := os.ReadFile("modules.sh")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(scriptDirectory, "modules.sh"), modules, 0o755); err != nil {
		t.Fatal(err)
	}
	common := `#!/usr/bin/env bash
SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
REPO_ROOT=$(cd -- "$SCRIPT_DIR/.." && pwd)
VV_MODULE=example.test/api
GO=${GO:-go}
satellites() { return 0; }
`
	if err := os.WriteFile(filepath.Join(scriptDirectory, "common.sh"), []byte(common), 0o755); err != nil {
		t.Fatal(err)
	}
	fakeGo := filepath.Join(root, "fake-go")
	if err := os.WriteFile(fakeGo, []byte("#!/usr/bin/env bash\n[[ $1 == build && $2 == -o && -n ${3:-} ]] || exit 2\ncp -- \"$VV_API_TOOL\" \"$3\"\nchmod +x \"$3\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	tool := filepath.Join(root, "api-tool")
	command := func(directory, apiTool string) *exec.Cmd {
		cmd := exec.Command("bash", filepath.Join(directory, "scripts", "modules.sh"), "api")
		cmd.Env = append(os.Environ(), "GO="+fakeGo, "VV_API_TOOL="+apiTool)
		return cmd
	}
	return root, command, filepath.Join(root, "docs", "api", "surface.md"), tool
}
