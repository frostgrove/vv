//go:build !integration

package auditpg

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestIntegrationProfileFailsClosedWithoutDSN(t *testing.T) {
	command := exec.Command("go", "test", "-tags=integration", "-run", "^$", "-count=1", ".")
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "FROSTGROVE_AUDITPG_TEST_DSN=") {
			command.Env = append(command.Env, value)
		}
	}
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("integration profile succeeded without its live DSN: %s", output)
	}
	if !strings.Contains(string(output), "FROSTGROVE_AUDITPG_TEST_DSN is required") {
		t.Fatalf("integration profile failed for the wrong reason: %s", output)
	}
}
