package scripts

import (
	"os"
	"strings"
	"testing"
)

func TestOTelReleaseRunsAllGatesBeforeTags(t *testing.T) {
	content, err := os.ReadFile("release.sh")
	if err != nil {
		t.Fatal(err)
	}
	source := string(content)
	checks := []string{
		"\"$SCRIPT_DIR/checks.sh\" otel-schema",
		"GOWORK=off \"$GO\" test ./...",
		"\"$SCRIPT_DIR/modules.sh\" vet",
		"\"$SCRIPT_DIR/otel-consumer.sh\"",
		"git tag -a",
	}
	positions := make([]int, len(checks))
	for i, check := range checks {
		positions[i] = strings.Index(source, check)
		if positions[i] < 0 {
			t.Fatalf("release gate %q missing", check)
		}
		if i > 0 && positions[i] <= positions[i-1] {
			t.Fatalf("release gate %q is not ordered before tag creation", check)
		}
	}
}

func TestOTelVersionWorkflowUpdatesRegistryScope(t *testing.T) {
	content, err := os.ReadFile("modules.sh")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "-write-scope-version \"$V\"") {
		t.Fatal("version workflow does not update registry scope version")
	}
}

func TestOTelConsumerFixtureIsUsedWithoutLocalReplace(t *testing.T) {
	content, err := os.ReadFile("otel-consumer.sh")
	if err != nil {
		t.Fatal(err)
	}
	source := string(content)
	if !strings.Contains(source, "GOWORK=off") || !strings.Contains(source, "otel-consumer-fixture/main.go.txt") {
		t.Fatal("consumer gate is not strict or does not use its fixture")
	}
	if strings.Contains(source, "go mod edit -replace") {
		t.Fatal("consumer gate must not install a local replace")
	}
}
