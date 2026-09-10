package scripts

import (
	"archive/zip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestOTelReleaseRunsLocalGatesBeforeAtomicTagsAndRemoteConsumerAfter(t *testing.T) {
	content, err := os.ReadFile("release.sh")
	if err != nil {
		t.Fatal(err)
	}
	source := string(content)
	if !strings.Contains(source, "git status --porcelain") {
		t.Fatal("release does not reject staged and untracked changes")
	}
	checks := []string{
		`"$SCRIPT_DIR/release-modules.sh" check`,
		"\"$SCRIPT_DIR/checks.sh\" otel-schema",
		"\"$SCRIPT_DIR/checks.sh\" otel-module",
		"GOWORK=off \"$GO\" test ./...",
		"\"$SCRIPT_DIR/modules.sh\" vet",
		"git push origin --atomic",
		"\"$SCRIPT_DIR/otel-consumer.sh\"",
	}
	positions := make([]int, len(checks))
	for i, check := range checks {
		positions[i] = strings.Index(source, check)
		if positions[i] < 0 {
			t.Fatalf("release gate %q missing", check)
		}
		if i > 0 && positions[i] <= positions[i-1] {
			t.Fatalf("release operation %q is out of order", check)
		}
	}
	if strings.Index(source, "git tag -a") > strings.Index(source, "git push origin --atomic") {
		t.Fatal("tag creation must precede the atomic push")
	}
}

func TestOTelVersionWorkflowUpdatesRegistryScope(t *testing.T) {
	content, err := os.ReadFile("modules.sh")
	if err != nil {
		t.Fatal(err)
	}
	source := string(content)
	if !strings.Contains(source, `"$SCRIPT_DIR/release-modules.sh" rewrite`) || !strings.Contains(source, "-write-scope-version \"$V\"") {
		t.Fatal("version workflow does not update registry scope version")
	}
}

func TestOTelConsumerFixtureIsUsedWithoutLocalReplace(t *testing.T) {
	content, err := os.ReadFile("otel-consumer.sh")
	if err != nil {
		t.Fatal(err)
	}
	source := string(content)
	if !strings.Contains(source, "GOWORK=off") || !strings.Contains(source, "GOENV=off") || !strings.Contains(source, "GOMODCACHE=") || !strings.Contains(source, "GOCACHE=") || !strings.Contains(source, "GOPATH=") || !strings.Contains(source, "GOFLAGS=-mod=mod") || !strings.Contains(source, "otel-consumer-fixture/main.go.txt") {
		t.Fatal("consumer gate is not strict or does not use its fixture")
	}
	if strings.Contains(source, "go mod edit -replace") {
		t.Fatal("consumer gate must not install a local replace")
	}
	if !strings.Contains(source, `.Replace.Path`) || !strings.Contains(source, `resolved_version == "$version"`) || !strings.Contains(source, `resolved_directory == "$module_cache"/*`) {
		t.Fatal("consumer gate does not verify exact version, no replace, and clean-cache provenance")
	}
}

func TestOTelReleaseChoreographyWithFakeAtomicVisibility(t *testing.T) {
	root := t.TempDir()
	scriptDirectory := filepath.Join(root, "scripts")
	stateDirectory := filepath.Join(root, "state")
	binDirectory := filepath.Join(root, "bin")
	for _, directory := range []string{scriptDirectory, stateDirectory, binDirectory, filepath.Join(root, "otel")} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	release, err := os.ReadFile("release.sh")
	if err != nil {
		t.Fatal(err)
	}
	writeExecutable(t, filepath.Join(scriptDirectory, "release.sh"), string(release))
	writeExecutable(t, filepath.Join(scriptDirectory, "common.sh"), `#!/usr/bin/env bash
SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
REPO_ROOT=$(cd -- "$SCRIPT_DIR/.." && pwd)
VV_MODULE=example.com/vv
GO=${GO:-go}
all_modules() { find . -name go.mod -exec dirname {} \; | LC_ALL=C sort; }
satellites() { all_modules | awk '$0 != "." && $0 != "./test" && $0 != "./_examples"'; }
`)
	writeExecutable(t, filepath.Join(scriptDirectory, "release-modules.sh"), `#!/usr/bin/env bash
printf 'preflight:%s:%s\n' "$1" "$V" >>"$VV_TEST_LOG"
`)
	writeExecutable(t, filepath.Join(scriptDirectory, "checks.sh"), `#!/usr/bin/env bash
printf 'check:%s\n' "$1" >>"$VV_TEST_LOG"
`)
	writeExecutable(t, filepath.Join(scriptDirectory, "modules.sh"), `#!/usr/bin/env bash
printf 'modules:%s\n' "$1" >>"$VV_TEST_LOG"
`)
	writeExecutable(t, filepath.Join(scriptDirectory, "i18n-consumer.sh"), `#!/usr/bin/env bash
printf 'i18n-local\n' >>"$VV_TEST_LOG"
`)
	writeExecutable(t, filepath.Join(scriptDirectory, "otel-consumer.sh"), `#!/usr/bin/env bash
[[ -f $VV_TEST_STATE/visible ]] || { echo 'remote consumer ran before visibility' >&2; exit 1; }
printf 'otel-remote:%s\n' "$V" >>"$VV_TEST_LOG"
`)
	writeExecutable(t, filepath.Join(binDirectory, "fake-go"), `#!/usr/bin/env bash
printf 'go:%s\n' "$*" >>"$VV_TEST_LOG"
`)
	writeExecutable(t, filepath.Join(binDirectory, "git"), `#!/usr/bin/env bash
case $1 in
status) exit 0 ;;
rev-parse)
	if [[ ${2:-} == -q ]]; then exit 1; fi
	printf 'fake-head\n'
	;;
rev-list) printf 'fake-head\n' ;;
tag) printf 'tag:%s\n' "$3" >>"$VV_TEST_LOG" ;;
push)
	[[ $* == *' --atomic '* && $* == *' v9.99.0'* && $* == *' otel/v9.99.0'* ]] || { echo "non-atomic or incomplete push: $*" >&2; exit 1; }
	printf 'push:%s\n' "$*" >>"$VV_TEST_LOG"
	: >"$VV_TEST_STATE/visible"
	;;
*) echo "unexpected git command: $*" >&2; exit 1 ;;
esac
`)
	writeReleaseFixture(t, root, "go.mod", "module example.com/vv\n\ngo 1.25\n")
	writeReleaseFixture(t, root, "otel/go.mod", "module example.com/vv/otel\n\ngo 1.25\n")
	writeReleaseFixture(t, root, "otel/schema_gen.go", "package otel\n\nconst (\n\tScopeVersion = \"v9.99.0\"\n)\n")
	logPath := filepath.Join(stateDirectory, "order.log")
	command := exec.Command("bash", filepath.Join(scriptDirectory, "release.sh"))
	command.Env = append(os.Environ(),
		"GO="+filepath.Join(binDirectory, "fake-go"),
		"PATH="+binDirectory+":"+os.Getenv("PATH"),
		"V=v9.99.0",
		"VV_TEST_LOG="+logPath,
		"VV_TEST_STATE="+stateDirectory,
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("fake release failed: %v\n%s", err, output)
	}
	order := readReleaseFixture(t, logPath)
	want := []string{"preflight:check:v9.99.0", "check:otel-schema", "check:otel-module", "go:test ./...", "modules:vet", "i18n-local", "tag:v9.99.0", "tag:otel/v9.99.0", "push:push origin --atomic v9.99.0 otel/v9.99.0", "otel-remote:v9.99.0"}
	position := -1
	for _, event := range want {
		next := strings.Index(order, event)
		if next <= position {
			t.Fatalf("event %q missing or out of order in:\n%s", event, order)
		}
		position = next
	}
}

func TestOTelRemoteConsumerRequiresNestedModuleVisibilityAtValidNeverPublishedVersion(t *testing.T) {
	const validNeverPublishedVersion = "v1.99.0"
	const rootModule = "github.com/frostgrove/vv"
	const otelModule = rootModule + "/otel"
	proxy := t.TempDir()
	writeReleaseProxyModule(t, proxy, rootModule, validNeverPublishedVersion,
		"module "+rootModule+"\n\ngo 1.26\n",
		map[string]string{"vv.go": "package vv\n\nconst Contract = \"root\"\n"},
	)
	consumer := t.TempDir()
	writeReleaseFixture(t, consumer, "go.mod", "module example.com/otel-consumer\n\ngo 1.26\n\nrequire "+otelModule+" "+validNeverPublishedVersion+"\n")
	writeReleaseFixture(t, consumer, "consumer.go", "package consumer\n\nimport vvotel \""+otelModule+"\"\n\nfunc ScopeVersion() string { return vvotel.ScopeVersion }\n")

	rootOnlyEnvironment := releaseProxyEnvironment(proxy, t.TempDir(), t.TempDir(), t.TempDir())
	downloadRoot := exec.Command("go", "mod", "download", rootModule+"@"+validNeverPublishedVersion)
	downloadRoot.Dir = consumer
	downloadRoot.Env = rootOnlyEnvironment
	if output, err := downloadRoot.CombinedOutput(); err != nil {
		t.Fatalf("root module is not visible in the hermetic proxy: %v\n%s", err, output)
	}
	rootOnly := exec.Command("go", "test", "./...")
	rootOnly.Dir = consumer
	rootOnly.Env = rootOnlyEnvironment
	output, err := rootOnly.CombinedOutput()
	if err == nil {
		t.Fatalf("no-replace nested consumer resolved from a root-only publication:\n%s", output)
	}
	if !strings.Contains(string(output), otelModule+"@"+validNeverPublishedVersion) {
		t.Fatalf("root-only failure does not identify the absent nested version: %v\n%s", err, output)
	}

	writeReleaseProxyModule(t, proxy, otelModule, validNeverPublishedVersion,
		"module "+otelModule+"\n\ngo 1.26\n\nrequire "+rootModule+" "+validNeverPublishedVersion+"\n",
		map[string]string{"otel.go": "package otel\n\nimport vv \"" + rootModule + "\"\n\nconst ScopeVersion = \"" + validNeverPublishedVersion + "\"\nconst RootContract = vv.Contract\n"},
	)
	publishedEnvironment := releaseProxyEnvironment(proxy, t.TempDir(), t.TempDir(), t.TempDir())
	published := exec.Command("go", "test", "./...")
	published.Dir = consumer
	published.Env = publishedEnvironment
	if output, err := published.CombinedOutput(); err != nil {
		t.Fatalf("consumer did not resolve after root and nested publication: %v\n%s", err, output)
	}
	modules := exec.Command("go", "list", "-m", "-f", `{{.Path}} {{.Version}} {{if .Replace}}replace={{.Replace.Path}}{{end}}`, "all")
	modules.Dir = consumer
	modules.Env = publishedEnvironment
	output, err = modules.CombinedOutput()
	if err != nil {
		t.Fatalf("cannot inspect hermetic consumer module provenance: %v\n%s", err, output)
	}
	resolved := string(output)
	for _, want := range []string{rootModule + " " + validNeverPublishedVersion, otelModule + " " + validNeverPublishedVersion} {
		if !strings.Contains(resolved, want) {
			t.Fatalf("resolved module graph lacks %q:\n%s", want, resolved)
		}
	}
	if strings.Contains(resolved, "replace=") {
		t.Fatalf("hermetic consumer used a replacement:\n%s", resolved)
	}
}

func writeReleaseProxyModule(t *testing.T, proxy, modulePath, version, goMod string, files map[string]string) {
	t.Helper()
	writeReleaseFixture(t, proxy, modulePath+"/@v/"+version+".mod", goMod)
	writeReleaseFixture(t, proxy, modulePath+"/@v/"+version+".info", `{"Version":"`+version+`","Time":"2026-01-01T00:00:00Z"}`)
	zipPath := filepath.Join(proxy, filepath.FromSlash(modulePath), "@v", version+".zip")
	archive, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(archive)
	files["go.mod"] = goMod
	for name, content := range files {
		entry, err := writer.Create(modulePath + "@" + version + "/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
}

func releaseProxyEnvironment(proxy, moduleCache, buildCache, goPath string) []string {
	blocked := map[string]bool{
		"GOCACHE": true, "GOENV": true, "GOFLAGS": true, "GOMODCACHE": true,
		"GONOPROXY": true, "GONOSUMDB": true, "GOPATH": true, "GOPRIVATE": true,
		"GOPROXY": true, "GOSUMDB": true, "GOTOOLCHAIN": true, "GOVCS": true, "GOWORK": true,
	}
	environment := make([]string, 0, len(os.Environ())+14)
	for _, variable := range os.Environ() {
		name, _, _ := strings.Cut(variable, "=")
		if !blocked[name] {
			environment = append(environment, variable)
		}
	}
	return append(environment,
		"GOCACHE="+buildCache,
		"GOENV=off",
		"GOFLAGS=-mod=mod -modcacherw",
		"GOMODCACHE="+moduleCache,
		"GONOPROXY=none",
		"GONOSUMDB=none",
		"GOPATH="+goPath,
		"GOPRIVATE=",
		"GOPROXY=file://"+filepath.ToSlash(proxy),
		"GOSUMDB=off",
		"GOTOOLCHAIN=local",
		"GOVCS=off",
		"GOWORK=off",
	)
}

func writeExecutable(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}
